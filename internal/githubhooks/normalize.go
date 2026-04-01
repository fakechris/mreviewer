package githubhooks

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/mreviewer/mreviewer/internal/hooks"
)

type webhookPayload struct {
	Action     string `json:"action"`
	Repository struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	PullRequest struct {
		Number  int64  `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		HTMLURL string `json:"html_url"`
		Merged  bool   `json:"merged"`
		State   string `json:"state"`
		Draft   bool   `json:"draft"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Head struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"head"`
		Base struct {
			Ref string `json:"ref"`
			SHA string `json:"sha"`
		} `json:"base"`
	} `json:"pull_request"`
}

func NormalizeWebhook(payload json.RawMessage, eventType string) (hooks.NormalizedEvent, error) {
	var raw webhookPayload
	if err := json.Unmarshal(payload, &raw); err != nil {
		return hooks.NormalizedEvent{}, fmt.Errorf("github normalize: unmarshal payload: %w", err)
	}
	if strings.TrimSpace(eventType) == "" {
		eventType = "pull_request"
	}

	instanceURL := extractInstanceURL(raw.Repository.HTMLURL)
	action, state := normalizeAction(raw.Action, raw.PullRequest.Merged, raw.PullRequest.State)
	headSHA := strings.TrimSpace(raw.PullRequest.Head.SHA)

	return hooks.NormalizedEvent{
		GitLabInstanceURL: instanceURL,
		ProjectID:         raw.Repository.ID,
		ProjectPath:       strings.TrimSpace(raw.Repository.FullName),
		MRIID:             raw.PullRequest.Number,
		Action:            action,
		HeadSHA:           headSHA,
		HeadSHADeferred:   headSHA == "",
		IsDraft:           raw.PullRequest.Draft,
		HookSource:        "github",
		TriggerType:       "webhook",
		EventType:         strings.TrimSpace(eventType),
		IdempotencyKey:    computeIdempotencyKey(instanceURL, raw.Repository.ID, raw.PullRequest.Number, headSHA, "github"),
		Title:             strings.TrimSpace(raw.PullRequest.Title),
		SourceBranch:      strings.TrimSpace(raw.PullRequest.Head.Ref),
		TargetBranch:      strings.TrimSpace(raw.PullRequest.Base.Ref),
		Author:            strings.TrimSpace(raw.PullRequest.User.Login),
		WebURL:            strings.TrimSpace(raw.PullRequest.HTMLURL),
		State:             state,
	}, nil
}

func normalizeAction(action string, merged bool, state string) (string, string) {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "opened":
		return "open", "opened"
	case "reopened":
		return "reopen", "opened"
	case "synchronize":
		return "update", "opened"
	case "closed":
		if merged {
			return "merge", "merged"
		}
		return "close", "closed"
	default:
		normalized := strings.ToLower(strings.TrimSpace(action))
		if normalized == "" {
			normalized = "update"
		}
		if strings.TrimSpace(state) == "" {
			state = "opened"
		}
		return normalized, strings.ToLower(strings.TrimSpace(state))
	}
}

func extractInstanceURL(repositoryURL string) string {
	parsed, err := url.Parse(strings.TrimSpace(repositoryURL))
	if err != nil {
		return ""
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

func computeIdempotencyKey(instanceURL string, projectID, number int64, headSHA, source string) string {
	sum := sha256.Sum256([]byte(fmt.Sprintf("%s|%d|%d|%s|%s", strings.TrimSpace(instanceURL), projectID, number, strings.TrimSpace(headSHA), strings.TrimSpace(source))))
	return hex.EncodeToString(sum[:])
}
