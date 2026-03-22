package gitlab

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"github.com/mreviewer/mreviewer/internal/writer"
)

type discussionResponse struct {
	ID any `json:"id"`
}

func (c *Client) CreateDiscussion(ctx context.Context, req writer.CreateDiscussionRequest) (writer.Discussion, error) {
	var response discussionResponse
	_, err := c.doJSONWithBody(ctx, http.MethodPost, mergeRequestPath(req.ProjectID, req.MergeRequestIID, "/discussions"), nil, map[string]any{
		"body":     req.Body,
		"position": req.Position,
	}, &response)
	if err != nil {
		return writer.Discussion{}, err
	}
	return writer.Discussion{ID: stringifyDiscussionID(response.ID)}, nil
}

func (c *Client) CreateNote(ctx context.Context, req writer.CreateNoteRequest) (writer.Discussion, error) {
	var response discussionResponse
	_, err := c.doJSONWithBody(ctx, http.MethodPost, mergeRequestPath(req.ProjectID, req.MergeRequestIID, "/notes"), nil, map[string]any{
		"body": req.Body,
	}, &response)
	if err != nil {
		return writer.Discussion{}, err
	}
	return writer.Discussion{ID: stringifyDiscussionID(response.ID)}, nil
}

func (c *Client) ResolveDiscussion(ctx context.Context, req writer.ResolveDiscussionRequest) error {
	_, err := c.doJSONWithBody(ctx, http.MethodPut, mergeRequestPath(req.ProjectID, req.MergeRequestIID, "/discussions/"+url.PathEscape(strings.TrimSpace(req.DiscussionID))), nil, map[string]any{
		"resolved": req.Resolved,
	}, nil)
	return err
}

func (c *Client) doJSONWithBody(ctx context.Context, method, apiPath string, query url.Values, payload any, dest any) (http.Header, error) {
	if c.limiter != nil {
		if err := c.limiter.Wait(ctx, rateLimitScopeKey(apiPath)); err != nil {
			return nil, err
		}
	}
	requestURL, err := c.buildURL(apiPath, query)
	if err != nil {
		return nil, err
	}

	body, err := marshalRequestBody(payload)
	if err != nil {
		return nil, fmt.Errorf("gitlab: encode %s %s request: %w", method, requestURL, err)
	}

	for attempt := 0; ; attempt++ {
		req, err := http.NewRequestWithContext(ctx, method, requestURL, bytes.NewReader(body))
		if err != nil {
			return nil, fmt.Errorf("gitlab: build %s %s: %w", method, requestURL, err)
		}
		if c.token != "" {
			req.Header.Set("PRIVATE-TOKEN", c.token)
		}
		req.Header.Set("Accept", "application/json")
		req.Header.Set("Content-Type", "application/json")

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

func marshalRequestBody(payload any) ([]byte, error) {
	if payload == nil {
		return nil, nil
	}
	return json.Marshal(payload)
}

func stringifyDiscussionID(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case json.Number:
		return typed.String()
	case float64:
		return strconv.FormatInt(int64(typed), 10)
	case int:
		return strconv.Itoa(typed)
	case int32:
		return strconv.FormatInt(int64(typed), 10)
	case int64:
		return strconv.FormatInt(typed, 10)
	case uint64:
		return strconv.FormatUint(typed, 10)
	default:
		return strings.TrimSpace(fmt.Sprint(value))
	}
}
