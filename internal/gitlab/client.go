package gitlab

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/mreviewer/mreviewer/internal/timeutil"
)

const (
	defaultPerPage               = 100
	defaultHTTPTimeout           = 30 * time.Second
	defaultDiffNotReadyRetries   = 3
	defaultRateLimitRetries      = 3
	defaultDiffNotReadyBaseDelay = time.Second
	defaultRateLimitBaseDelay    = time.Second
	defaultRateLimitMaxDelay     = 30 * time.Second
	defaultJitterFraction        = 0.5
	errorBodyLimit               = 4 << 10
)

var ErrDiffNotReady = errors.New("gitlab: diff not ready")
var ErrFileNotFound = errors.New("gitlab: repository file not found")

type HTTPClient interface {
	Do(req *http.Request) (*http.Response, error)
}

type Option func(*Client)

type Client struct {
	baseURL                string
	token                  string
	httpClient             HTTPClient
	limiter                RateLimiter
	sleep                  func(context.Context, time.Duration) error
	now                    func() time.Time
	diffNotReadyMaxRetries int
	diffNotReadyDelay      func(int) time.Duration
	rateLimitMaxRetries    int
	rateLimitDelay         func(int) time.Duration
	rateLimitJitter        func(time.Duration) time.Duration
}

type RateLimiter interface {
	Wait(context.Context, string) error
}

type DiffRefs struct {
	BaseSHA  string `json:"base_sha"`
	HeadSHA  string `json:"head_sha"`
	StartSHA string `json:"start_sha"`
}

type MergeRequest struct {
	GitLabID            int64     `json:"id"`
	IID                 int64     `json:"iid"`
	ProjectID           int64     `json:"project_id"`
	Title               string    `json:"title"`
	Description         string    `json:"description"`
	State               string    `json:"state"`
	Draft               bool      `json:"draft"`
	SourceBranch        string    `json:"source_branch"`
	TargetBranch        string    `json:"target_branch"`
	HeadSHA             string    `json:"sha"`
	DetailedMergeStatus string    `json:"detailed_merge_status"`
	HasConflicts        bool      `json:"has_conflicts"`
	WebURL              string    `json:"web_url"`
	DiffRefs            *DiffRefs `json:"diff_refs"`
	Author              struct {
		Username string `json:"username"`
	} `json:"author"`
}

type MergeRequestVersion struct {
	GitLabVersionID int64     `json:"id"`
	HeadSHA         string    `json:"head_commit_sha"`
	BaseSHA         string    `json:"base_commit_sha"`
	StartSHA        string    `json:"start_commit_sha"`
	PatchIDSHA      string    `json:"patch_id_sha"`
	CreatedAt       time.Time `json:"created_at"`
	MergeRequestID  int64     `json:"merge_request_id"`
	State           string    `json:"state"`
	RealSize        string    `json:"real_size"`
}

type MergeRequestDiff struct {
	OldPath       string `json:"old_path"`
	NewPath       string `json:"new_path"`
	Diff          string `json:"diff"`
	AMode         string `json:"a_mode"`
	BMode         string `json:"b_mode"`
	NewFile       bool   `json:"new_file"`
	RenamedFile   bool   `json:"renamed_file"`
	DeletedFile   bool   `json:"deleted_file"`
	GeneratedFile bool   `json:"generated_file"`
	Collapsed     bool   `json:"collapsed"`
	TooLarge      bool   `json:"too_large"`
}

type MergeRequestSnapshot struct {
	MergeRequest MergeRequest
	Version      MergeRequestVersion
	Diffs        []MergeRequestDiff
}

type MergeRequestNote struct {
	ID   int64  `json:"id"`
	Body string `json:"body"`
	Author struct {
		Username string `json:"username"`
	} `json:"author"`
}

type MergeRequestDiscussionPosition struct {
	OldPath string `json:"old_path"`
	NewPath string `json:"new_path"`
	OldLine int    `json:"old_line"`
	NewLine int    `json:"new_line"`
}

type MergeRequestDiscussionNote struct {
	ID       int64                          `json:"id"`
	Body     string                         `json:"body"`
	Position *MergeRequestDiscussionPosition `json:"position"`
	Author   struct {
		Username string `json:"username"`
	} `json:"author"`
}

type MergeRequestDiscussion struct {
	ID    string                      `json:"id"`
	Notes []MergeRequestDiscussionNote `json:"notes"`
}

type mergeRequestChangesResponse struct {
	Changes []MergeRequestDiff `json:"changes"`
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
		return fmt.Sprintf("gitlab: %s %s returned status %d", e.Method, e.URL, e.StatusCode)
	}
	return fmt.Sprintf("gitlab: %s %s returned status %d: %s", e.Method, e.URL, e.StatusCode, e.Body)
}

func (e *HTTPStatusError) HTTPStatus() int {
	if e == nil {
		return 0
	}
	return e.StatusCode
}

type DiffNotReadyError struct {
	ProjectID       int64
	MergeRequestIID int64
	Attempts        int
	Cause           error
}

func (e *DiffNotReadyError) Error() string {
	if e == nil {
		return ""
	}
	if e.Cause != nil {
		return fmt.Sprintf("gitlab: merge request %d in project %d diffs not ready after %d attempts: %v", e.MergeRequestIID, e.ProjectID, e.Attempts, e.Cause)
	}
	return fmt.Sprintf("gitlab: merge request %d in project %d diffs not ready after %d attempts", e.MergeRequestIID, e.ProjectID, e.Attempts)
}

func (e *DiffNotReadyError) Unwrap() error {
	return ErrDiffNotReady
}

func NewClient(baseURL, token string, opts ...Option) (*Client, error) {
	if strings.TrimSpace(baseURL) == "" {
		return nil, fmt.Errorf("gitlab: base URL is required")
	}

	parsed, err := url.Parse(baseURL)
	if err != nil {
		return nil, fmt.Errorf("gitlab: parse base URL: %w", err)
	}

	client := &Client{
		baseURL:                strings.TrimRight(parsed.String(), "/"),
		token:                  token,
		httpClient:             &http.Client{Timeout: defaultHTTPTimeout},
		sleep:                  timeutil.SleepContext,
		now:                    time.Now,
		diffNotReadyMaxRetries: defaultDiffNotReadyRetries,
		diffNotReadyDelay:      cappedExponentialDelay(defaultDiffNotReadyBaseDelay, defaultRateLimitMaxDelay),
		rateLimitMaxRetries:    defaultRateLimitRetries,
		rateLimitDelay:         cappedExponentialDelay(defaultRateLimitBaseDelay, defaultRateLimitMaxDelay),
		rateLimitJitter:        defaultRateLimitJitter,
	}

	for _, opt := range opts {
		if opt != nil {
			opt(client)
		}
	}

	if client.httpClient == nil {
		client.httpClient = &http.Client{Timeout: defaultHTTPTimeout}
	}
	if client.sleep == nil {
		client.sleep = timeutil.SleepContext
	}
	if client.now == nil {
		client.now = time.Now
	}
	if client.diffNotReadyDelay == nil {
		client.diffNotReadyDelay = cappedExponentialDelay(defaultDiffNotReadyBaseDelay, defaultRateLimitMaxDelay)
	}
	if client.rateLimitDelay == nil {
		client.rateLimitDelay = cappedExponentialDelay(defaultRateLimitBaseDelay, defaultRateLimitMaxDelay)
	}
	if client.rateLimitJitter == nil {
		client.rateLimitJitter = defaultRateLimitJitter
	}

	return client, nil
}

func WithHTTPClient(httpClient HTTPClient) Option {
	return func(c *Client) {
		c.httpClient = httpClient
	}
}

func WithRateLimiter(limiter RateLimiter) Option {
	return func(c *Client) {
		c.limiter = limiter
	}
}

func WithSleep(sleep func(context.Context, time.Duration) error) Option {
	return func(c *Client) {
		c.sleep = sleep
	}
}

func WithNow(now func() time.Time) Option {
	return func(c *Client) {
		c.now = now
	}
}

func WithDiffNotReadyMaxRetries(maxRetries int) Option {
	return func(c *Client) {
		if maxRetries >= 0 {
			c.diffNotReadyMaxRetries = maxRetries
		}
	}
}

func WithDiffNotReadyBackoff(delay func(int) time.Duration) Option {
	return func(c *Client) {
		c.diffNotReadyDelay = delay
	}
}

func WithRateLimitMaxRetries(maxRetries int) Option {
	return func(c *Client) {
		if maxRetries >= 0 {
			c.rateLimitMaxRetries = maxRetries
		}
	}
}

func WithRateLimitBackoff(delay func(int) time.Duration) Option {
	return func(c *Client) {
		c.rateLimitDelay = delay
	}
}

func WithRateLimitJitter(jitter func(time.Duration) time.Duration) Option {
	return func(c *Client) {
		c.rateLimitJitter = jitter
	}
}

func (c *Client) GetMergeRequest(ctx context.Context, projectID, mergeRequestIID int64) (MergeRequest, error) {
	return c.GetMergeRequestByProjectRef(ctx, strconv.FormatInt(projectID, 10), mergeRequestIID)
}

func (c *Client) GetMergeRequestByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) (MergeRequest, error) {
	var mr MergeRequest
	_, err := c.doJSON(ctx, http.MethodGet, mergeRequestPathByProjectRef(projectRef, mergeRequestIID, ""), nil, &mr)
	if err != nil {
		return MergeRequest{}, err
	}
	return mr, nil
}

func (c *Client) GetMergeRequestVersions(ctx context.Context, projectID, mergeRequestIID int64) (MergeRequestVersion, error) {
	var versions []MergeRequestVersion
	_, err := c.doJSON(ctx, http.MethodGet, mergeRequestPath(projectID, mergeRequestIID, "/versions"), nil, &versions)
	if err != nil {
		return MergeRequestVersion{}, err
	}
	if len(versions) == 0 {
		return MergeRequestVersion{}, ErrDiffNotReady
	}

	latest := versions[0]
	for _, candidate := range versions[1:] {
		if candidate.newerThan(latest) {
			latest = candidate
		}
	}
	if !latest.ready() {
		return MergeRequestVersion{}, ErrDiffNotReady
	}

	return latest, nil
}

func (c *Client) GetMergeRequestDiffs(ctx context.Context, projectID, mergeRequestIID int64) ([]MergeRequestDiff, error) {
	diffs, err := c.getMergeRequestDiffsPaginated(ctx, projectID, mergeRequestIID)
	if err == nil {
		return diffs, nil
	}
	if !isRetriableDiffEndpointError(err) {
		return nil, err
	}

	diffs, plainErr := c.getMergeRequestDiffsPlain(ctx, projectID, mergeRequestIID)
	if plainErr == nil {
		return diffs, nil
	}
	if !isRetriableDiffEndpointError(plainErr) {
		return nil, plainErr
	}

	diffs, changesErr := c.getMergeRequestDiffsFromChanges(ctx, projectID, mergeRequestIID)
	if changesErr == nil {
		return diffs, nil
	}
	return nil, changesErr
}

func (c *Client) getMergeRequestDiffsPaginated(ctx context.Context, projectID, mergeRequestIID int64) ([]MergeRequestDiff, error) {
	path := mergeRequestPath(projectID, mergeRequestIID, "/diffs")
	page := 1
	allDiffs := make([]MergeRequestDiff, 0)

	for {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", strconv.Itoa(defaultPerPage))

		var pageDiffs []MergeRequestDiff
		headers, err := c.doJSON(ctx, http.MethodGet, path, query, &pageDiffs)
		if err != nil {
			return nil, err
		}

		allDiffs = append(allDiffs, pageDiffs...)
		nextPage := strings.TrimSpace(headers.Get("X-Next-Page"))
		if nextPage == "" {
			return allDiffs, nil
		}

		next, err := strconv.Atoi(nextPage)
		if err != nil || next <= page {
			return nil, fmt.Errorf("gitlab: invalid next page %q for %s", nextPage, path)
		}
		page = next
	}
}

func (c *Client) getMergeRequestDiffsPlain(ctx context.Context, projectID, mergeRequestIID int64) ([]MergeRequestDiff, error) {
	var diffs []MergeRequestDiff
	_, err := c.doJSON(ctx, http.MethodGet, mergeRequestPath(projectID, mergeRequestIID, "/diffs"), nil, &diffs)
	if err != nil {
		return nil, err
	}
	return diffs, nil
}

func (c *Client) getMergeRequestDiffsFromChanges(ctx context.Context, projectID, mergeRequestIID int64) ([]MergeRequestDiff, error) {
	var resp mergeRequestChangesResponse
	_, err := c.doJSON(ctx, http.MethodGet, mergeRequestPath(projectID, mergeRequestIID, "/changes"), nil, &resp)
	if err != nil {
		return nil, err
	}
	return resp.Changes, nil
}

func isRetriableDiffEndpointError(err error) bool {
	var statusErr *HTTPStatusError
	if !errors.As(err, &statusErr) {
		return false
	}
	return statusErr.StatusCode >= http.StatusInternalServerError
}

func (c *Client) GetMergeRequestSnapshot(ctx context.Context, projectID, mergeRequestIID int64) (MergeRequestSnapshot, error) {
	return c.GetMergeRequestSnapshotByProjectRef(ctx, strconv.FormatInt(projectID, 10), mergeRequestIID)
}

func (c *Client) GetMergeRequestSnapshotByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) (MergeRequestSnapshot, error) {
	var lastErr error

	for attempt := 0; ; attempt++ {
		mr, err := c.GetMergeRequestByProjectRef(ctx, projectRef, mergeRequestIID)
		if err != nil {
			return MergeRequestSnapshot{}, err
		}
		projectID := mr.ProjectID

		version, err := c.GetMergeRequestVersions(ctx, projectID, mergeRequestIID)
		if err != nil && !errors.Is(err, ErrDiffNotReady) {
			return MergeRequestSnapshot{}, err
		}

		if err == nil && mr.diffRefsReady() {
			diffs, err := c.GetMergeRequestDiffs(ctx, projectID, mergeRequestIID)
			if err != nil {
				return MergeRequestSnapshot{}, err
			}
			return MergeRequestSnapshot{
				MergeRequest: mr,
				Version:      version,
				Diffs:        diffs,
			}, nil
		}

		lastErr = err
		if lastErr == nil {
			lastErr = ErrDiffNotReady
		}

		if attempt >= c.diffNotReadyMaxRetries {
			return MergeRequestSnapshot{}, &DiffNotReadyError{
				ProjectID:       projectID,
				MergeRequestIID: mergeRequestIID,
				Attempts:        attempt + 1,
				Cause:           lastErr,
			}
		}

		if err := c.sleep(ctx, c.diffNotReadyDelay(attempt)); err != nil {
			return MergeRequestSnapshot{}, err
		}
	}
}

func (c *Client) GetRepositoryFile(ctx context.Context, projectID int64, filePath, ref string) (string, error) {
	return c.GetRepositoryFileByRepositoryRef(ctx, strconv.FormatInt(projectID, 10), filePath, ref)
}

func (c *Client) GetRepositoryFileByRepositoryRef(ctx context.Context, repositoryRef, filePath, ref string) (string, error) {
	query := url.Values{}
	if strings.TrimSpace(ref) != "" {
		query.Set("ref", ref)
	}
	requestURL, err := c.buildURL(fmt.Sprintf("/api/v4/projects/%s/repository/files/%s/raw", url.PathEscape(repositoryRef), url.PathEscape(filePath)), query)
	if err != nil {
		return "", err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, requestURL, nil)
	if err != nil {
		return "", fmt.Errorf("gitlab: build GET %s: %w", requestURL, err)
	}
	if c.token != "" {
		req.Header.Set("PRIVATE-TOKEN", c.token)
	}

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return "", fmt.Errorf("gitlab: GET %s: %w", requestURL, err)
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		_, _ = io.Copy(io.Discard, resp.Body)
		return "", ErrFileNotFound
	}
	if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
		return "", &HTTPStatusError{Method: http.MethodGet, URL: requestURL, StatusCode: resp.StatusCode, Body: readBodyPreview(resp.Body)}
	}

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", fmt.Errorf("gitlab: read GET %s response: %w", requestURL, err)
	}
	return string(data), nil
}

func (c *Client) ListMergeRequestNotesByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) ([]MergeRequestNote, error) {
	return c.listMergeRequestNotes(ctx, mergeRequestPathByProjectRef(projectRef, mergeRequestIID, "/notes"))
}

func (c *Client) ListMergeRequestDiscussionsByProjectRef(ctx context.Context, projectRef string, mergeRequestIID int64) ([]MergeRequestDiscussion, error) {
	return c.listMergeRequestDiscussions(ctx, mergeRequestPathByProjectRef(projectRef, mergeRequestIID, "/discussions"))
}

func (c *Client) listMergeRequestNotes(ctx context.Context, path string) ([]MergeRequestNote, error) {
	page := 1
	var notes []MergeRequestNote
	for {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", strconv.Itoa(defaultPerPage))

		var batch []MergeRequestNote
		headers, err := c.doJSON(ctx, http.MethodGet, path, query, &batch)
		if err != nil {
			return nil, err
		}
		notes = append(notes, batch...)
		nextPage := strings.TrimSpace(headers.Get("X-Next-Page"))
		if nextPage == "" {
			return notes, nil
		}
		next, err := strconv.Atoi(nextPage)
		if err != nil || next <= page {
			return nil, fmt.Errorf("gitlab: invalid next page %q for %s", nextPage, path)
		}
		page = next
	}
}

func (c *Client) listMergeRequestDiscussions(ctx context.Context, path string) ([]MergeRequestDiscussion, error) {
	page := 1
	var discussions []MergeRequestDiscussion
	for {
		query := url.Values{}
		query.Set("page", strconv.Itoa(page))
		query.Set("per_page", strconv.Itoa(defaultPerPage))

		var batch []MergeRequestDiscussion
		headers, err := c.doJSON(ctx, http.MethodGet, path, query, &batch)
		if err != nil {
			return nil, err
		}
		discussions = append(discussions, batch...)
		nextPage := strings.TrimSpace(headers.Get("X-Next-Page"))
		if nextPage == "" {
			return discussions, nil
		}
		next, err := strconv.Atoi(nextPage)
		if err != nil || next <= page {
			return nil, fmt.Errorf("gitlab: invalid next page %q for %s", nextPage, path)
		}
		page = next
	}
}

func (c *Client) doJSON(ctx context.Context, method, apiPath string, query url.Values, dest any) (http.Header, error) {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx, rateLimitScopeKey(apiPath)); err != nil {
			return nil, err
		}
	}
	requestURL, err := c.buildURL(apiPath, query)
	if err != nil {
		return nil, err
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, requestURL, nil)
		if err != nil {
			return nil, fmt.Errorf("gitlab: build %s %s: %w", method, requestURL, err)
		}
		if c.token != "" {
			req.Header.Set("PRIVATE-TOKEN", c.token)
		}
		req.Header.Set("Accept", "application/json")

		resp, err := c.httpClient.Do(req)
		if err != nil {
			return nil, fmt.Errorf("gitlab: %s %s: %w", method, requestURL, err)
		}

		if resp.StatusCode == http.StatusTooManyRequests {
			responseHeaders := resp.Header.Clone()
			bodyPreview := readBodyPreview(resp.Body)
			_ = resp.Body.Close()

			if attempt >= c.rateLimitMaxRetries {
				return nil, &HTTPStatusError{
					Method:     method,
					URL:        requestURL,
					StatusCode: resp.StatusCode,
					Body:       bodyPreview,
				}
			}

			delay := c.rateLimitDelay(attempt)
			delay += c.rateLimitJitter(delay)
			if retryAfter := parseRetryAfter(responseHeaders.Get("Retry-After"), c.now()); retryAfter > delay {
				delay = retryAfter
			}

			if err := c.sleep(ctx, delay); err != nil {
				return nil, err
			}
			continue
		}

		responseHeaders := resp.Header.Clone()
		if resp.StatusCode < http.StatusOK || resp.StatusCode >= http.StatusMultipleChoices {
			bodyPreview := readBodyPreview(resp.Body)
			_ = resp.Body.Close()
			return nil, &HTTPStatusError{
				Method:     method,
				URL:        requestURL,
				StatusCode: resp.StatusCode,
				Body:       bodyPreview,
			}
		}

		if dest == nil {
			_, copyErr := io.Copy(io.Discard, resp.Body)
			closeErr := resp.Body.Close()
			if copyErr != nil {
				return nil, fmt.Errorf("gitlab: discard %s %s response body: %w", method, requestURL, copyErr)
			}
			if closeErr != nil {
				return nil, fmt.Errorf("gitlab: close %s %s response body: %w", method, requestURL, closeErr)
			}
			return responseHeaders, nil
		}

		decodeErr := json.NewDecoder(resp.Body).Decode(dest)
		closeErr := resp.Body.Close()
		if decodeErr != nil {
			return nil, fmt.Errorf("gitlab: decode %s %s response: %w", method, requestURL, decodeErr)
		}
		if closeErr != nil {
			return nil, fmt.Errorf("gitlab: close %s %s response body: %w", method, requestURL, closeErr)
		}

		return responseHeaders, nil
	}
}

func rateLimitScopeKey(apiPath string) string {
	parts := strings.Split(strings.Trim(apiPath, "/"), "/")
	for i := 0; i < len(parts)-1; i++ {
		if parts[i] == "projects" && parts[i+1] != "" {
			return parts[i+1]
		}
	}
	return "global"
}

type InMemoryRateLimiter struct {
	mu         sync.Mutex
	now        func() time.Time
	sleep      func(context.Context, time.Duration) error
	limits     map[string]RateLimitConfig
	states     map[string]rateLimitState
	defaultCfg RateLimitConfig
}

type RateLimitConfig struct {
	Requests int
	Window   time.Duration
}

type rateLimitState struct {
	windowStart time.Time
	count       int
}

func NewInMemoryRateLimiter(defaultCfg RateLimitConfig, now func() time.Time, sleep func(context.Context, time.Duration) error) *InMemoryRateLimiter {
	if now == nil {
		now = time.Now
	}
	if sleep == nil {
		sleep = timeutil.SleepContext
	}
	return &InMemoryRateLimiter{now: now, sleep: sleep, limits: make(map[string]RateLimitConfig), states: make(map[string]rateLimitState), defaultCfg: defaultCfg}
}

func (l *InMemoryRateLimiter) SetLimit(scope string, cfg RateLimitConfig) {
	if l == nil {
		return
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	l.limits[scope] = cfg
}

func (l *InMemoryRateLimiter) Wait(ctx context.Context, scope string) error {
	if l == nil {
		return nil
	}
	scope = strings.TrimSpace(scope)
	if scope == "" {
		scope = "global"
	}
	for {
		now := l.now()
		waitFor := time.Duration(0)

		l.mu.Lock()
		cfg := l.defaultCfg
		if scoped, ok := l.limits[scope]; ok {
			cfg = scoped
		}
		if cfg.Requests <= 0 || cfg.Window <= 0 {
			l.mu.Unlock()
			return nil
		}
		state := l.states[scope]
		if state.windowStart.IsZero() || now.Sub(state.windowStart) >= cfg.Window {
			state = rateLimitState{windowStart: now, count: 0}
		}
		if state.count < cfg.Requests {
			state.count++
			l.states[scope] = state
			l.mu.Unlock()
			return nil
		}
		waitFor = state.windowStart.Add(cfg.Window).Sub(now)
		l.mu.Unlock()

		if waitFor <= 0 {
			continue
		}
		if err := l.sleep(ctx, waitFor); err != nil {
			return err
		}
	}
}

func (c *Client) buildURL(apiPath string, query url.Values) (string, error) {
	base, err := url.Parse(c.baseURL)
	if err != nil {
		return "", fmt.Errorf("gitlab: parse base URL: %w", err)
	}

	relative, err := url.Parse(apiPath)
	if err != nil {
		return "", fmt.Errorf("gitlab: parse api path %q: %w", apiPath, err)
	}

	resolved := base.ResolveReference(relative)
	if len(query) > 0 {
		values := resolved.Query()
		for key, items := range query {
			for _, item := range items {
				values.Add(key, item)
			}
		}
		resolved.RawQuery = values.Encode()
	}

	return resolved.String(), nil
}

func mergeRequestPath(projectID, mergeRequestIID int64, suffix string) string {
	return mergeRequestPathByProjectRef(strconv.FormatInt(projectID, 10), mergeRequestIID, suffix)
}

func mergeRequestPathByProjectRef(projectRef string, mergeRequestIID int64, suffix string) string {
	base := fmt.Sprintf("/api/v4/projects/%s/merge_requests/%d", url.PathEscape(strings.TrimSpace(projectRef)), mergeRequestIID)
	return base + suffix
}

func projectCommitStatusPath(projectID int64, sha string) string {
	return fmt.Sprintf("/api/v4/projects/%s/statuses/%s",
		url.PathEscape(strconv.FormatInt(projectID, 10)),
		url.PathEscape(strings.TrimSpace(sha)),
	)
}

func (mr MergeRequest) diffRefsReady() bool {
	return mr.DiffRefs != nil && mr.DiffRefs.BaseSHA != "" && mr.DiffRefs.HeadSHA != "" && mr.DiffRefs.StartSHA != ""
}

func (v MergeRequestVersion) newerThan(other MergeRequestVersion) bool {
	if !v.CreatedAt.Equal(other.CreatedAt) {
		return v.CreatedAt.After(other.CreatedAt)
	}

	return v.GitLabVersionID > other.GitLabVersionID
}

func (v MergeRequestVersion) ready() bool {
	return v.BaseSHA != "" && v.StartSHA != "" && v.HeadSHA != ""
}

func cappedExponentialDelay(base, max time.Duration) func(int) time.Duration {
	return func(attempt int) time.Duration {
		if base <= 0 {
			return 0
		}

		delay := base
		for i := 0; i < attempt; i++ {
			if max > 0 && delay >= max/2 {
				return max
			}
			delay *= 2
		}
		if max > 0 && delay > max {
			return max
		}
		return delay
	}
}

func defaultRateLimitJitter(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	return time.Duration(rand.Float64() * float64(base) * defaultJitterFraction)
}

func parseRetryAfter(value string, now time.Time) time.Duration {
	value = strings.TrimSpace(value)
	if value == "" {
		return 0
	}

	seconds, err := strconv.Atoi(value)
	if err == nil {
		if seconds < 0 {
			return 0
		}
		return time.Duration(seconds) * time.Second
	}

	when, err := http.ParseTime(value)
	if err != nil {
		return 0
	}
	if when.Before(now) {
		return 0
	}
	return when.Sub(now)
}

func readBodyPreview(body io.Reader) string {
	if body == nil {
		return ""
	}

	data, err := io.ReadAll(io.LimitReader(body, errorBodyLimit))
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(data))
}
