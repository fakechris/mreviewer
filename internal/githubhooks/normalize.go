package githubhooks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/hooks"
)

type webhookPayload struct {
	Action      string `json:"action"`
	Number      int64  `json:"number"`
	Repository   struct {
		ID       int64  `json:"id"`
		FullName string `json:"full_name"`
		HTMLURL  string `json:"html_url"`
	} `json:"repository"`
	PullRequest struct {
		ID      int64  `json:"id"`
		Number  int64  `json:"number"`
		Title   string `json:"title"`
		Body    string `json:"body"`
		State   string `json:"state"`
		Draft   bool   `json:"draft"`
		HTMLURL string `json:"html_url"`
		Merged  bool   `json:"merged"`
		User    struct {
			Login string `json:"login"`
		} `json:"user"`
		Base struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				HTMLURL string `json:"html_url"`
			} `json:"repo"`
		} `json:"base"`
		Head struct {
			Ref  string `json:"ref"`
			SHA  string `json:"sha"`
			Repo struct {
				HTMLURL string `json:"html_url"`
			} `json:"repo"`
		} `json:"head"`
	} `json:"pull_request"`
}

func NormalizeWebhook(payload json.RawMessage, eventType string) (hooks.NormalizedEvent, error) {
	var raw webhookPayload
	if err := json.Unmarshal(payload, &raw); err != nil {
		return hooks.NormalizedEvent{}, fmt.Errorf("normalize github webhook: unmarshal payload: %w", err)
	}
	instanceURL := extractInstanceURL(raw.Repository.HTMLURL)
	action := normalizeAction(raw.Action, raw.PullRequest.Merged)
	scopeJSON := json.RawMessage(`{"platform":"github"}`)
	ev := hooks.NormalizedEvent{
		GitLabInstanceURL: instanceURL,
		ProjectID:         raw.Repository.ID,
		ProjectPath:       strings.TrimSpace(raw.Repository.FullName),
		MRIID:             raw.Number,
		Action:            action,
		HeadSHA:           strings.TrimSpace(raw.PullRequest.Head.SHA),
		HeadSHADeferred:   strings.TrimSpace(raw.PullRequest.Head.SHA) == "",
		IsDraft:           raw.PullRequest.Draft,
		HookSource:        "project",
		TriggerType:       "webhook",
		EventType:         strings.TrimSpace(eventType),
		Title:             strings.TrimSpace(raw.PullRequest.Title),
		SourceBranch:      strings.TrimSpace(raw.PullRequest.Head.Ref),
		TargetBranch:      strings.TrimSpace(raw.PullRequest.Base.Ref),
		Author:            strings.TrimSpace(raw.PullRequest.User.Login),
		WebURL:            strings.TrimSpace(raw.PullRequest.HTMLURL),
		State:             strings.TrimSpace(raw.PullRequest.State),
		ScopeJSON:         scopeJSON,
	}
	ev.IdempotencyKey = computeIdempotencyKey(instanceURL, ev.ProjectID, ev.MRIID, ev.HeadSHA, ev.HeadSHADeferred, ev.TriggerType)
	return ev, nil
}

func normalizeAction(action string, merged bool) string {
	switch strings.ToLower(strings.TrimSpace(action)) {
	case "opened":
		return "open"
	case "reopened":
		return "reopen"
	case "synchronize":
		return "update"
	case "closed":
		if merged {
			return "merge"
		}
		return "close"
	default:
		return strings.ToLower(strings.TrimSpace(action))
	}
}

func computeIdempotencyKey(instanceURL string, projectID, mrIID int64, headSHA string, headSHADeferred bool, triggerType string) string {
	shaComponent := headSHA
	if headSHADeferred {
		shaComponent = "deferred"
	}
	input := fmt.Sprintf("%s|%d|%d|%s|%s",
		strings.TrimRight(instanceURL, "/"),
		projectID,
		mrIID,
		shaComponent,
		triggerType,
	)
	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash[:16])
}

func extractInstanceURL(webURL string) string {
	webURL = strings.TrimSpace(webURL)
	if webURL == "" {
		return ""
	}
	protoEnd := strings.Index(webURL, "://")
	if protoEnd < 0 {
		return ""
	}
	rest := webURL[protoEnd+3:]
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		return webURL
	}
	return webURL[:protoEnd+3+slashIdx]
}
