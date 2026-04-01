package github

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	legacygitlab "github.com/mreviewer/mreviewer/internal/gitlab"
)

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Client struct {
	baseURL    string
	token      string
	httpClient HTTPClient
}

type ReviewSnapshot struct {
	Title            string
	Description      string
	SourceBranch     string
	TargetBranch     string
	BaseSHA          string
	HeadSHA          string
	RepositoryWebURL string
	Diffs            []legacygitlab.MergeRequestDiff
}

func (s ReviewSnapshot) ReviewDiffs() []legacygitlab.MergeRequestDiff {
	return append([]legacygitlab.MergeRequestDiff(nil), s.Diffs...)
}

type pullRequestResponse struct {
	Title   string `json:"title"`
	Body    string `json:"body"`
	HTMLURL string `json:"html_url"`
	Head    struct {
		Ref string `json:"ref"`
		SHA string `json:"sha"`
	} `json:"head"`
	Base struct {
		Ref  string `json:"ref"`
		SHA  string `json:"sha"`
		Repo struct {
			HTMLURL string `json:"html_url"`
		} `json:"repo"`
	} `json:"base"`
}

type pullRequestFileResponse struct {
	Filename  string `json:"filename"`
	Status    string `json:"status"`
	Patch     string `json:"patch"`
	Previous  string `json:"previous_filename"`
	Additions int    `json:"additions"`
	Deletions int    `json:"deletions"`
}

type repositoryContentResponse struct {
	Encoding string `json:"encoding"`
	Content  string `json:"content"`
}

type CommentUser struct {
	Login string `json:"login"`
}

type IssueComment struct {
	ID      int64       `json:"id"`
	Body    string      `json:"body"`
	HTMLURL string      `json:"html_url"`
	User    CommentUser `json:"user"`
}

type ReviewComment struct {
	ID      int64       `json:"id"`
	Body    string      `json:"body"`
	HTMLURL string      `json:"html_url"`
	Path    string      `json:"path"`
	Line    int         `json:"line"`
	Side    string      `json:"side"`
	User    CommentUser `json:"user"`
}

type issueCommentPayload struct {
	Body string `json:"body"`
}

type reviewCommentPayload struct {
	Body string `json:"body"`
	Path string `json:"path"`
	Line int    `json:"line"`
	Side string `json:"side"`
}

type commitStatusPayload struct {
	State       string `json:"state"`
	Context     string `json:"context,omitempty"`
	Description string `json:"description,omitempty"`
	TargetURL   string `json:"target_url,omitempty"`
}

type CommitStatusRequest struct {
	Owner       string
	Repo        string
	SHA         string
	State       string
	Context     string
	Description string
	TargetURL   string
}

func NewClient(baseURL, token string) (*Client, error) {
	trimmed := strings.TrimRight(strings.TrimSpace(baseURL), "/")
	if trimmed == "" {
		return nil, fmt.Errorf("github: base URL is required")
	}
	return &Client{
		baseURL:    trimmed,
		token:      strings.TrimSpace(token),
		httpClient: &http.Client{Timeout: 30 * time.Second},
	}, nil
}

func (c *Client) GetPullRequestSnapshot(ctx context.Context, owner, repo string, number int64) (ReviewSnapshot, error) {
	var pr pullRequestResponse
	if err := c.doJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d", url.PathEscape(owner), url.PathEscape(repo), number), &pr); err != nil {
		return ReviewSnapshot{}, err
	}

	var files []pullRequestFileResponse
	page := 1
	for {
		var current []pullRequestFileResponse
		if err := c.doJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/files?page=%d&per_page=100", url.PathEscape(owner), url.PathEscape(repo), number, page), &current); err != nil {
			return ReviewSnapshot{}, err
		}
		files = append(files, current...)
		if len(current) < 100 {
			break
		}
		page++
	}

	diffs := make([]legacygitlab.MergeRequestDiff, 0, len(files))
	for _, file := range files {
		diff := legacygitlab.MergeRequestDiff{
			OldPath: file.Filename,
			NewPath: file.Filename,
			Diff:    file.Patch,
		}
		switch file.Status {
		case "added":
			diff.NewFile = true
		case "removed":
			diff.DeletedFile = true
		case "renamed":
			diff.RenamedFile = true
			diff.OldPath = file.Previous
		}
		diffs = append(diffs, diff)
	}

	return ReviewSnapshot{
		Title:            pr.Title,
		Description:      pr.Body,
		SourceBranch:     pr.Head.Ref,
		TargetBranch:     pr.Base.Ref,
		BaseSHA:          pr.Base.SHA,
		HeadSHA:          pr.Head.SHA,
		RepositoryWebURL: pr.Base.Repo.HTMLURL,
		Diffs:            diffs,
	}, nil
}

func (c *Client) GetRepositoryFile(_ context.Context, _ int64, _ string, _ string) (string, error) {
	return "", fmt.Errorf("github: project-id based repository file lookup is unsupported")
}

func (c *Client) GetRepositoryFileByRef(ctx context.Context, repositoryRef, filePath, ref string) (string, error) {
	owner, repo, ok := strings.Cut(strings.TrimSpace(repositoryRef), "/")
	if !ok || owner == "" || repo == "" {
		return "", fmt.Errorf("github: repository ref must be owner/repo")
	}

	requestPath := fmt.Sprintf("/repos/%s/%s/contents/%s?ref=%s",
		url.PathEscape(owner),
		url.PathEscape(repo),
		url.PathEscape(filePath),
		url.QueryEscape(strings.TrimSpace(ref)),
	)
	var payload repositoryContentResponse
	if err := c.doJSON(ctx, requestPath, &payload); err != nil {
		return "", err
	}
	if payload.Encoding != "base64" {
		return "", fmt.Errorf("github: unsupported content encoding %q", payload.Encoding)
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(payload.Content, "\n", ""))
	if err != nil {
		return "", fmt.Errorf("github: decode repository file: %w", err)
	}
	return string(decoded), nil
}

func (c *Client) CreateIssueComment(ctx context.Context, req IssueCommentRequest) error {
	return c.doPOST(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d/comments",
		url.PathEscape(req.Owner),
		url.PathEscape(req.Repo),
		req.Number,
	), issueCommentPayload{Body: req.Body})
}

func (c *Client) CreateReviewComment(ctx context.Context, req ReviewCommentRequest) error {
	return c.doPOST(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments",
		url.PathEscape(req.Owner),
		url.PathEscape(req.Repo),
		req.Number,
	), reviewCommentPayload{
		Body: req.Body,
		Path: req.Path,
		Line: req.Line,
		Side: req.Side,
	})
}

func (c *Client) SetCommitStatus(ctx context.Context, req CommitStatusRequest) error {
	return c.doPOST(ctx, fmt.Sprintf("/repos/%s/%s/statuses/%s",
		url.PathEscape(req.Owner),
		url.PathEscape(req.Repo),
		url.PathEscape(strings.TrimSpace(req.SHA)),
	), commitStatusPayload{
		State:       strings.TrimSpace(req.State),
		Context:     strings.TrimSpace(req.Context),
		Description: strings.TrimSpace(req.Description),
		TargetURL:   strings.TrimSpace(req.TargetURL),
	})
}

func (c *Client) ListIssueComments(ctx context.Context, owner, repo string, number int64) ([]IssueComment, error) {
	var comments []IssueComment
	for page := 1; ; page++ {
		var current []IssueComment
		if err := c.doJSON(ctx, fmt.Sprintf("/repos/%s/%s/issues/%d/comments?page=%d&per_page=100",
			url.PathEscape(owner), url.PathEscape(repo), number, page), &current); err != nil {
			return nil, err
		}
		comments = append(comments, current...)
		if len(current) < 100 {
			break
		}
	}
	return comments, nil
}

func (c *Client) ListReviewComments(ctx context.Context, owner, repo string, number int64) ([]ReviewComment, error) {
	var comments []ReviewComment
	for page := 1; ; page++ {
		var current []ReviewComment
		if err := c.doJSON(ctx, fmt.Sprintf("/repos/%s/%s/pulls/%d/comments?page=%d&per_page=100",
			url.PathEscape(owner), url.PathEscape(repo), number, page), &current); err != nil {
			return nil, err
		}
		comments = append(comments, current...)
		if len(current) < 100 {
			break
		}
	}
	return comments, nil
}

func (c *Client) doJSON(ctx context.Context, requestPath string, dest any) error {
	requestURL := c.baseURL + requestPath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return fmt.Errorf("github: build GET %s: %w", requestURL, err)
	}
	req.Header.Set("Accept", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github: GET %s returned status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	if err := json.NewDecoder(resp.Body).Decode(dest); err != nil {
		return fmt.Errorf("github: decode %s: %w", requestURL, err)
	}
	return nil
}

func (c *Client) doPOST(ctx context.Context, requestPath string, payload any) error {
	requestURL := c.baseURL + requestPath
	body, err := json.Marshal(payload)
	if err != nil {
		return fmt.Errorf("github: encode POST %s: %w", requestURL, err)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, requestURL, strings.NewReader(string(body)))
	if err != nil {
		return fmt.Errorf("github: build POST %s: %w", requestURL, err)
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Content-Type", "application/json")
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("github: POST %s: %w", requestURL, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
		return fmt.Errorf("github: POST %s returned status %d: %s", requestURL, resp.StatusCode, strings.TrimSpace(string(body)))
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}
