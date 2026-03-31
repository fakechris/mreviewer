package github

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type fileNotFoundError struct{}

func (fileNotFoundError) Error() string   { return "github: repository file not found" }
func (fileNotFoundError) HTTPStatus() int { return http.StatusNotFound }

var ErrFileNotFound error = fileNotFoundError{}

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Option func(*Client)

type Client struct {
	baseURL    string
	token      string
	httpClient HTTPClient
}

type HTTPStatusError struct {
	Method     string
	URL        string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return ""
	}
	if e.Body == "" {
		return fmt.Sprintf("github: %s %s returned status %d", e.Method, e.URL, e.StatusCode)
	}
	return fmt.Sprintf("github: %s %s returned status %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

func (e *HTTPStatusError) HTTPStatus() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

func NewClient(baseURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("github: base URL is required")
	}
	parsed, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil {
		return nil, fmt.Errorf("github: parse base URL: %w", err)
	}
	client := &Client{
		baseURL:    strings.TrimRight(parsed.String(), "/"),
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}
	if client.httpClient == nil {
		client.httpClient = &http.Client{Timeout: 30 * time.Second}
	}
	return client, nil
}

func WithHTTPClient(httpClient HTTPClient) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

func (c *Client) GetPullRequestSnapshotByRepositoryRef(ctx context.Context, repositoryRef string, pullNumber int64) (PullRequestSnapshot, error) {
	var prResp struct {
		ID      int64  `json:"id"`
		Number  int64  `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		Draft   bool   `json:"draft"`
		HTMLURL string `json:"html_url"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Base struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
	}
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d", repositoryRef, pullNumber), nil, &prResp); err != nil {
		return PullRequestSnapshot{}, err
	}

	files, err := c.listPullRequestFiles(ctx, repositoryRef, pullNumber)
	if err != nil {
		return PullRequestSnapshot{}, err
	}

	return PullRequestSnapshot{
		PullRequest: PullRequest{
			ID:          prResp.ID,
			Number:      prResp.Number,
			Title:       prResp.Title,
			Body:        prResp.Body,
			State:       prResp.State,
			Draft:       prResp.Draft,
			HTMLURL:     prResp.HTMLURL,
			BaseRefName: prResp.Base.Ref,
			BaseSHA:     prResp.Base.SHA,
			HeadRefName: prResp.Head.Ref,
			HeadSHA:     prResp.Head.SHA,
			User: PullRequestUser{
				Login: prResp.User.Login,
			},
		},
		Files: files,
	}, nil
}

func (c *Client) listPullRequestFiles(ctx context.Context, repositoryRef string, pullNumber int64) ([]PullRequestFile, error) {
	var files []PullRequestFile
	for page := 1; ; page++ {
		var batch []PullRequestFile
		if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/pulls/%d/files", repositoryRef, pullNumber), url.Values{
			"per_page": []string{"100"},
			"page":     []string{fmt.Sprintf("%d", page)},
		}, &batch); err != nil {
			return nil, err
		}
		files = append(files, batch...)
		if len(batch) == 0 {
			return files, nil
		}
	}
}

func (c *Client) GetRepositoryFileByRepositoryRef(ctx context.Context, repositoryRef, filePath, ref string) (string, error) {
	query := url.Values{}
	if strings.TrimSpace(ref) != "" {
		query.Set("ref", ref)
	}
	var payload struct {
		Type     string `json:"type"`
		Encoding string `json:"encoding"`
		Content  string `json:"content"`
	}
	if err := c.doJSON(ctx, http.MethodGet, fmt.Sprintf("/repos/%s/contents/%s", repositoryRef, filePath), query, &payload); err != nil {
		var statusErr *HTTPStatusError
		if errors.As(err, &statusErr) && statusErr.StatusCode == http.StatusNotFound {
			return "", ErrFileNotFound
		}
		return "", err
	}
	if payload.Encoding != "base64" {
		return "", fmt.Errorf("github: unsupported content encoding %q", payload.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("github: decode file content: %w", err)
	}
	return string(decoded), nil
}

func (c *Client) CreateIssueComment(ctx context.Context, req CreateIssueCommentRequest) error {
	payload := map[string]any{"body": req.Body}
	return c.doJSONWithBody(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/issues/%d/comments", req.Repository, req.PullNumber), nil, payload, nil)
}

func (c *Client) CreateReviewComment(ctx context.Context, req CreateReviewCommentRequest) error {
	payload := map[string]any{
		"body":      req.Body,
		"commit_id": req.CommitID,
		"path":      req.Path,
		"line":      req.Line,
		"side":      req.Side,
	}
	if req.StartLine > 0 {
		payload["start_line"] = req.StartLine
	}
	if strings.TrimSpace(req.StartSide) != "" {
		payload["start_side"] = req.StartSide
	}
	return c.doJSONWithBody(ctx, http.MethodPost, fmt.Sprintf("/repos/%s/pulls/%d/comments", req.Repository, req.PullNumber), nil, payload, nil)
}

func (c *Client) doJSON(ctx context.Context, method, apiPath string, query url.Values, dest any) error {
	return c.doJSONWithBody(ctx, method, apiPath, query, nil, dest)
}

func (c *Client) doJSONWithBody(ctx context.Context, method, apiPath string, query url.Values, body any, dest any) error {
	requestURL, err := c.buildURL(apiPath, query)
	if err != nil {
		return err
	}
	var reqBody io.Reader
	if body != nil {
		data, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("github: marshal request body: %w", err)
		}
		reqBody = bytes.NewReader(data)
	}
	req, err := http.NewRequestWithContext(ctx, method, requestURL, reqBody)
	if err != nil {
		return fmt.Errorf("github: build %s %s: %w", method, requestURL, err)
	}
	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: %s %s: %w", method, requestURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return &HTTPStatusError{
			Method:     method,
			URL:        requestURL,
			StatusCode: resp.StatusCode,
			Body:       readBodyPreview(resp.Body),
		}
	}
	if dest == nil {
		_, _ = io.Copy(io.Discard, resp.Body)
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(dest)
}

func (c *Client) buildURL(apiPath string, query url.Values) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("github: parse base url: %w", err)
	}
	rel, err := url.Parse(apiPath)
	if err != nil {
		return "", fmt.Errorf("github: parse api path: %w", err)
	}
	full := base.ResolveReference(rel)
	if len(query) > 0 {
		full.RawQuery = query.Encode()
	}
	return full.String(), nil
}

func readBodyPreview(r io.Reader) string {
	const limit = 4096
	body, err := io.ReadAll(io.LimitReader(r, limit))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(body))
}
