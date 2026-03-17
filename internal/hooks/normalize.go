// Package hooks contains webhook normalization logic that converts GitLab
// project, group, system, and CI trigger inputs into a single internal event
// model.
package hooks

import (
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"strings"
)

// NormalizedEvent is the canonical internal representation of any MR-related
// trigger, regardless of whether it arrived as a project, group, or system
// webhook or as a CI-originated trigger. All downstream processing operates
// on this struct rather than on raw webhook payloads.
type NormalizedEvent struct {
	// GitLabInstanceURL is the base URL of the GitLab instance that sent the
	// event. For webhooks this is extracted from the project's web_url; for CI
	// triggers it is supplied directly.
	GitLabInstanceURL string `json:"gitlab_instance_url"`

	// ProjectID is the numeric GitLab project ID.
	ProjectID int64 `json:"project_id"`

	// ProjectPath is the path_with_namespace of the project (e.g. "group/repo").
	ProjectPath string `json:"project_path"`

	// MRIID is the merge request internal ID (the number visible in the UI).
	MRIID int64 `json:"mr_iid"`

	// Action is the MR lifecycle action (e.g. "open", "update", "close", "merge").
	Action string `json:"action"`

	// HeadSHA is the SHA of the HEAD commit on the MR source branch. When the
	// payload does not include last_commit.id, this is empty and
	// HeadSHADeferred is set to true.
	HeadSHA string `json:"head_sha"`

	// HeadSHADeferred is true when the webhook payload did not contain the
	// head SHA and a deferred lookup from the GitLab API is required.
	HeadSHADeferred bool `json:"head_sha_deferred"`

	// IsDraft indicates whether the MR is a draft/WIP. Draft status is
	// preserved but does not block review run creation.
	IsDraft bool `json:"is_draft"`

	// HookSource indicates the webhook source: "project", "group", or "system".
	// For CI triggers this is "ci".
	HookSource string `json:"hook_source"`

	// TriggerType distinguishes webhook-originated from CI-originated triggers.
	// Values: "webhook" (default) or "ci".
	TriggerType string `json:"trigger_type"`

	// EventType is the raw event type from the header or payload (e.g.
	// "merge_request", "Merge Request Hook", "System Hook").
	EventType string `json:"event_type"`

	// IdempotencyKey is a deterministic key computed from
	// gitlab_instance_url + project_id + mr_iid + head_sha + trigger_type.
	// For events with deferred head_sha, the key uses "deferred" as the SHA
	// component.
	IdempotencyKey string `json:"idempotency_key"`

	// Title is the MR title, if present in the payload.
	Title string `json:"title"`

	// SourceBranch is the MR source branch name.
	SourceBranch string `json:"source_branch"`

	// TargetBranch is the MR target branch name.
	TargetBranch string `json:"target_branch"`

	// Author is the MR author username.
	Author string `json:"author"`

	// WebURL is the web URL of the MR.
	WebURL string `json:"web_url"`

	// State is the MR state (e.g. "opened", "closed", "merged").
	State string `json:"state"`
}

// webhookPayload is the union struct for extracting fields from project, group,
// and system webhook payloads. GitLab uses the same JSON structure for all
// three hook types.
type webhookPayload struct {
	ObjectKind string `json:"object_kind"`
	EventType  string `json:"event_type"`
	GroupID    *int64 `json:"group_id"`
	GroupPath  string `json:"group_path"`
	GroupName  string `json:"group_name"`
	Group      struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		FullPath string `json:"full_path"`
	} `json:"group"`
	ObjectAttributes struct {
		IID            int64  `json:"iid"`
		Action         string `json:"action"`
		Title          string `json:"title"`
		SourceBranch   string `json:"source_branch"`
		TargetBranch   string `json:"target_branch"`
		State          string `json:"state"`
		CreatedAt      string `json:"created_at"`
		UpdatedAt      string `json:"updated_at"`
		ActionedAt     string `json:"actioned_at"`
		OldRev         string `json:"oldrev"`
		WorkInProgress bool   `json:"work_in_progress"`
		Draft          bool   `json:"draft"`
		URL            string `json:"url"`
		LastCommit     struct {
			ID string `json:"id"`
		} `json:"last_commit"`
	} `json:"object_attributes"`
	Project struct {
		ID                int64  `json:"id"`
		PathWithNamespace string `json:"path_with_namespace"`
		WebURL            string `json:"web_url"`
	} `json:"project"`
	User struct {
		Username string `json:"username"`
	} `json:"user"`
}

// NormalizeWebhook converts a raw GitLab MR webhook payload (from project,
// group, or system hooks) into a NormalizedEvent. The hookSource parameter
// should be "project", "group", or "system" as detected from headers.
//
// The function extracts the GitLab instance URL from the project's web_url
// field. All three hook types produce the same normalized output for the
// same logical MR event, ensuring cross-source idempotency.
func NormalizeWebhook(payload json.RawMessage, headerEventType, hookSource string) (NormalizedEvent, error) {
	var raw webhookPayload
	if err := json.Unmarshal(payload, &raw); err != nil {
		return NormalizedEvent{}, fmt.Errorf("normalize: unmarshal payload: %w", err)
	}

	// Determine the event type with fallback chain:
	// header > payload event_type > payload object_kind
	eventType := headerEventType
	if eventType == "" {
		eventType = raw.EventType
	}
	if eventType == "" {
		eventType = raw.ObjectKind
	}

	// Extract GitLab instance URL from project's web_url.
	instanceURL := extractInstanceURL(raw.Project.WebURL)

	// Determine head SHA; mark deferred if missing.
	headSHA := raw.ObjectAttributes.LastCommit.ID
	headSHADeferred := headSHA == ""

	// Draft detection: GitLab sends either work_in_progress (older) or draft (newer).
	isDraft := raw.ObjectAttributes.Draft || raw.ObjectAttributes.WorkInProgress

	// The hook source from detection may be overridden:
	// - System hooks send the same MR payload structure as project hooks.
	// - Group hooks share the same MR event header as project hooks, so runtime
	//   intake must inspect payload markers when present.
	// We normalize hook_source but it does NOT affect the idempotency key.
	normalizedHookSource := resolveWebhookSource(raw, hookSource)

	deferredDiscriminator := ""
	if headSHADeferred {
		deferredDiscriminator = computeDeferredHeadSHAKey(raw)
	}

	ev := NormalizedEvent{
		GitLabInstanceURL: instanceURL,
		ProjectID:         raw.Project.ID,
		ProjectPath:       raw.Project.PathWithNamespace,
		MRIID:             raw.ObjectAttributes.IID,
		Action:            raw.ObjectAttributes.Action,
		HeadSHA:           headSHA,
		HeadSHADeferred:   headSHADeferred,
		IsDraft:           isDraft,
		HookSource:        normalizedHookSource,
		TriggerType:       "webhook",
		EventType:         eventType,
		Title:             raw.ObjectAttributes.Title,
		SourceBranch:      raw.ObjectAttributes.SourceBranch,
		TargetBranch:      raw.ObjectAttributes.TargetBranch,
		Author:            raw.User.Username,
		WebURL:            raw.ObjectAttributes.URL,
		State:             raw.ObjectAttributes.State,
	}

	// Compute idempotency key.
	ev.IdempotencyKey = computeIdempotencyKey(instanceURL, raw.Project.ID, raw.ObjectAttributes.IID, headSHA, headSHADeferred, "webhook", deferredDiscriminator)

	return ev, nil
}

type hookSourceProbe struct {
	GroupID   *int64 `json:"group_id"`
	GroupPath string `json:"group_path"`
	GroupName string `json:"group_name"`
	Group     struct {
		ID       int64  `json:"id"`
		Name     string `json:"name"`
		Path     string `json:"path"`
		FullPath string `json:"full_path"`
	} `json:"group"`
}

func inferWebhookSource(payload json.RawMessage, fallback string) string {
	var probe hookSourceProbe
	if err := json.Unmarshal(payload, &probe); err != nil {
		return normalizeWebhookSourceFallback(fallback)
	}
	return resolveWebhookSourceProbe(probe, fallback)
}

func resolveWebhookSource(raw webhookPayload, fallback string) string {
	probe := hookSourceProbe{
		GroupID:   raw.GroupID,
		GroupPath: raw.GroupPath,
		GroupName: raw.GroupName,
	}
	probe.Group.ID = raw.Group.ID
	probe.Group.Name = raw.Group.Name
	probe.Group.Path = raw.Group.Path
	probe.Group.FullPath = raw.Group.FullPath
	return resolveWebhookSourceProbe(probe, fallback)
}

func resolveWebhookSourceProbe(probe hookSourceProbe, fallback string) string {
	normalizedFallback := normalizeWebhookSourceFallback(fallback)
	if normalizedFallback == "system" || normalizedFallback == "group" {
		return normalizedFallback
	}
	if hasGroupWebhookScope(probe) {
		return "group"
	}
	return normalizedFallback
}

func normalizeWebhookSourceFallback(fallback string) string {
	switch strings.ToLower(strings.TrimSpace(fallback)) {
	case "system":
		return "system"
	case "group":
		return "group"
	default:
		return "project"
	}
}

func hasGroupWebhookScope(probe hookSourceProbe) bool {
	return probe.GroupID != nil ||
		probe.GroupPath != "" ||
		probe.GroupName != "" ||
		probe.Group.ID > 0 ||
		probe.Group.Path != "" ||
		probe.Group.FullPath != "" ||
		probe.Group.Name != ""
}

func computeDeferredHeadSHAKey(raw webhookPayload) string {
	payload, _ := json.Marshal(struct {
		Action       string `json:"action"`
		OldRev       string `json:"oldrev,omitempty"`
		State        string `json:"state,omitempty"`
		CreatedAt    string `json:"created_at,omitempty"`
		UpdatedAt    string `json:"updated_at,omitempty"`
		ActionedAt   string `json:"actioned_at,omitempty"`
		SourceBranch string `json:"source_branch,omitempty"`
		TargetBranch string `json:"target_branch,omitempty"`
		URL          string `json:"url,omitempty"`
	}{
		Action:       raw.ObjectAttributes.Action,
		OldRev:       raw.ObjectAttributes.OldRev,
		State:        raw.ObjectAttributes.State,
		CreatedAt:    raw.ObjectAttributes.CreatedAt,
		UpdatedAt:    raw.ObjectAttributes.UpdatedAt,
		ActionedAt:   raw.ObjectAttributes.ActionedAt,
		SourceBranch: raw.ObjectAttributes.SourceBranch,
		TargetBranch: raw.ObjectAttributes.TargetBranch,
		URL:          raw.ObjectAttributes.URL,
	})

	hash := sha256.Sum256(payload)
	return fmt.Sprintf("%x", hash[:16])
}

// CITriggerInput holds the parameters for a CI-originated trigger.
type CITriggerInput struct {
	GitLabInstanceURL string `json:"gitlab_instance_url"`
	ProjectID         int64  `json:"project_id"`
	ProjectPath       string `json:"project_path"`
	MRIID             int64  `json:"mr_iid"`
	HeadSHA           string `json:"head_sha"`
}

// NormalizeCITrigger converts a CI-originated trigger into a NormalizedEvent.
// CI triggers produce a distinct trigger_type ("ci") which ensures they do not
// collide with webhook-originated idempotency keys for the same HEAD.
func NormalizeCITrigger(input CITriggerInput) NormalizedEvent {
	headSHADeferred := input.HeadSHA == ""

	ev := NormalizedEvent{
		GitLabInstanceURL: input.GitLabInstanceURL,
		ProjectID:         input.ProjectID,
		ProjectPath:       input.ProjectPath,
		MRIID:             input.MRIID,
		Action:            "ci_trigger",
		HeadSHA:           input.HeadSHA,
		HeadSHADeferred:   headSHADeferred,
		HookSource:        "ci",
		TriggerType:       "ci",
		EventType:         "ci_trigger",
	}

	ev.IdempotencyKey = computeIdempotencyKey(input.GitLabInstanceURL, input.ProjectID, input.MRIID, input.HeadSHA, headSHADeferred, "ci", "")

	return ev
}

// computeIdempotencyKey generates a deterministic SHA-256 based key from the
// components that uniquely identify a logical review trigger:
// gitlab_instance_url + project_id + mr_iid + head_sha + trigger_type.
//
// When head_sha is missing (deferred), a stable deferred discriminator can be
// supplied so logically distinct missing-SHA events do not collapse onto the
// same synthetic key.
func computeIdempotencyKey(instanceURL string, projectID, mrIID int64, headSHA string, headSHADeferred bool, triggerType, deferredDiscriminator string) string {
	shaComponent := headSHA
	if headSHADeferred {
		shaComponent = "deferred"
		if deferredDiscriminator != "" {
			shaComponent = "deferred:" + deferredDiscriminator
		}
	}

	input := fmt.Sprintf("%s|%d|%d|%s|%s",
		strings.TrimRight(instanceURL, "/"),
		projectID,
		mrIID,
		shaComponent,
		triggerType,
	)

	hash := sha256.Sum256([]byte(input))
	return fmt.Sprintf("%x", hash[:16]) // 32 hex chars (128 bits) — compact but collision-safe.
}

// extractInstanceURL derives the GitLab instance base URL from a project's
// web_url by stripping the project path. For example:
//
//	"https://gitlab.example.com/group/repo" → "https://gitlab.example.com"
//	"https://gitlab.example.com/a/b/c"     → "https://gitlab.example.com"
//
// Returns empty string if the URL is empty or cannot be parsed.
func extractInstanceURL(webURL string) string {
	if webURL == "" {
		return ""
	}

	// Find the protocol separator.
	protoEnd := strings.Index(webURL, "://")
	if protoEnd < 0 {
		return ""
	}

	// After "://", take everything up to the first "/" as the host.
	rest := webURL[protoEnd+3:]
	slashIdx := strings.Index(rest, "/")
	if slashIdx < 0 {
		// No path component, entire URL is the instance URL.
		return webURL
	}

	return webURL[:protoEnd+3+slashIdx]
}

// IsMergeRequestEventType returns true for GitLab merge request event type
// headers. For "System Hook" headers, the caller must additionally check
// the payload's object_kind because system hooks carry all event types under
// the same header value.
func IsMergeRequestEventType(headerEventType string) bool {
	normalized := strings.ToLower(strings.TrimSpace(headerEventType))
	switch normalized {
	case "merge_request", "merge request hook":
		return true
	default:
		return false
	}
}

// IsSystemHookHeader returns true when the X-Gitlab-Event header is "System Hook".
func IsSystemHookHeader(headerEventType string) bool {
	return strings.EqualFold(strings.TrimSpace(headerEventType), "system hook")
}

// IsMergeRequestPayload checks whether the raw payload's object_kind is
// "merge_request". This is needed for system hooks where the header does not
// distinguish event types.
func IsMergeRequestPayload(payload json.RawMessage) bool {
	var probe struct {
		ObjectKind string `json:"object_kind"`
	}
	if err := json.Unmarshal(payload, &probe); err != nil {
		return false
	}
	return strings.EqualFold(probe.ObjectKind, "merge_request")
}
