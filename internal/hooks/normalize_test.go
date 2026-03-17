package hooks

import (
	"encoding/json"
	"testing"
)

// --- Fixture payloads ---

// projectHookPayload returns a project-level MR webhook payload.
func projectHookPayload(action, headSHA string, isDraft bool) json.RawMessage {
	draft := "false"
	if isDraft {
		draft = "true"
	}
	lastCommit := ""
	if headSHA != "" {
		lastCommit = `,"last_commit":{"id":"` + headSHA + `"}`
	}
	return json.RawMessage(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"object_attributes": {
			"iid": 42,
			"action": "` + action + `",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"state": "opened",
			"draft": ` + draft + `,
			"url": "https://gitlab.example.com/mygroup/myrepo/-/merge_requests/42"` + lastCommit + `
		},
		"project": {
			"id": 100,
			"path_with_namespace": "mygroup/myrepo",
			"web_url": "https://gitlab.example.com/mygroup/myrepo"
		},
		"user": {"username": "johndoe"}
	}`)
}

// groupHookPayload returns a group-level MR webhook payload. GitLab group hooks
// use the same JSON structure but arrive with a different X-Gitlab-Event header
// (still "Merge Request Hook" for MR events).
func groupHookPayload(action, headSHA string, isDraft bool) json.RawMessage {
	draft := "false"
	if isDraft {
		draft = "true"
	}
	lastCommit := ""
	if headSHA != "" {
		lastCommit = `,"last_commit":{"id":"` + headSHA + `"}`
	}
	return json.RawMessage(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"group_id": 200,
		"group_path": "mygroup",
		"group_name": "My Group",
		"object_attributes": {
			"iid": 42,
			"action": "` + action + `",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"state": "opened",
			"draft": ` + draft + `,
			"url": "https://gitlab.example.com/mygroup/myrepo/-/merge_requests/42"` + lastCommit + `
		},
		"project": {
			"id": 100,
			"path_with_namespace": "mygroup/myrepo",
			"web_url": "https://gitlab.example.com/mygroup/myrepo"
		},
		"user": {"username": "johndoe"}
	}`)
}

// systemHookPayload returns a system-level MR webhook payload. System hooks
// arrive with X-Gitlab-Event: "System Hook" but for MR events contain the
// same object_kind/object_attributes structure.
func systemHookPayload(action, headSHA string, isDraft bool) json.RawMessage {
	draft := "false"
	if isDraft {
		draft = "true"
	}
	lastCommit := ""
	if headSHA != "" {
		lastCommit = `,"last_commit":{"id":"` + headSHA + `"}`
	}
	// System hooks may include additional fields but the MR-relevant fields
	// are the same as project hooks.
	return json.RawMessage(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"object_attributes": {
			"iid": 42,
			"action": "` + action + `",
			"title": "Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"state": "opened",
			"draft": ` + draft + `,
			"url": "https://gitlab.example.com/mygroup/myrepo/-/merge_requests/42"` + lastCommit + `
		},
		"project": {
			"id": 100,
			"path_with_namespace": "mygroup/myrepo",
			"web_url": "https://gitlab.example.com/mygroup/myrepo"
		},
		"user": {"username": "johndoe"}
	}`)
}

// wipPayload returns an MR payload with the older work_in_progress field
// set to true.
func wipPayload() json.RawMessage {
	return json.RawMessage(`{
		"object_kind": "merge_request",
		"event_type": "merge_request",
		"object_attributes": {
			"iid": 42,
			"action": "open",
			"title": "WIP: Add feature X",
			"source_branch": "feature-x",
			"target_branch": "main",
			"state": "opened",
			"work_in_progress": true,
			"draft": false,
			"url": "https://gitlab.example.com/mygroup/myrepo/-/merge_requests/42",
			"last_commit": {"id": "abc123def456"}
		},
		"project": {
			"id": 100,
			"path_with_namespace": "mygroup/myrepo",
			"web_url": "https://gitlab.example.com/mygroup/myrepo"
		},
		"user": {"username": "johndoe"}
	}`)
}

// --- Normalization unit tests ---

// TestNormalizeProjectHook verifies that a project-level webhook is normalized
// correctly (VAL-INGRESS-007 analog for project hooks).
func TestNormalizeProjectHook(t *testing.T) {
	payload := projectHookPayload("open", "abc123def456", false)

	ev, err := NormalizeWebhook(payload, "Merge Request Hook", "project")
	if err != nil {
		t.Fatalf("NormalizeWebhook: %v", err)
	}

	assertEventFields(t, ev, expectedFields{
		instanceURL:     "https://gitlab.example.com",
		projectID:       100,
		projectPath:     "mygroup/myrepo",
		mrIID:           42,
		action:          "open",
		headSHA:         "abc123def456",
		headSHADeferred: false,
		isDraft:         false,
		hookSource:      "project",
		triggerType:     "webhook",
		title:           "Add feature X",
	})
}

// TestNormalizeGroupHook verifies that a group-level webhook normalizes into
// the same shape as a project hook (VAL-INGRESS-008).
func TestNormalizeGroupHook(t *testing.T) {
	payload := groupHookPayload("open", "abc123def456", false)

	ev, err := NormalizeWebhook(payload, "Merge Request Hook", "project")
	if err != nil {
		t.Fatalf("NormalizeWebhook: %v", err)
	}

	assertEventFields(t, ev, expectedFields{
		instanceURL:     "https://gitlab.example.com",
		projectID:       100,
		projectPath:     "mygroup/myrepo",
		mrIID:           42,
		action:          "open",
		headSHA:         "abc123def456",
		headSHADeferred: false,
		isDraft:         false,
		hookSource:      "group",
		triggerType:     "webhook",
		title:           "Add feature X",
	})
}

// TestNormalizeSystemHook verifies that a system hook with "System Hook" header
// normalizes into the same internal event shape (VAL-INGRESS-007).
func TestNormalizeSystemHook(t *testing.T) {
	payload := systemHookPayload("open", "abc123def456", false)

	ev, err := NormalizeWebhook(payload, "System Hook", "system")
	if err != nil {
		t.Fatalf("NormalizeWebhook: %v", err)
	}

	assertEventFields(t, ev, expectedFields{
		instanceURL:     "https://gitlab.example.com",
		projectID:       100,
		projectPath:     "mygroup/myrepo",
		mrIID:           42,
		action:          "open",
		headSHA:         "abc123def456",
		headSHADeferred: false,
		isDraft:         false,
		hookSource:      "system",
		triggerType:     "webhook",
		title:           "Add feature X",
	})
}

// TestDraftMetadata verifies that draft status is preserved in normalized events
// and does not affect normalization (VAL-INGRESS-011).
func TestDraftMetadata(t *testing.T) {
	tests := []struct {
		name      string
		payload   json.RawMessage
		wantDraft bool
	}{
		{
			name:      "draft=true",
			payload:   projectHookPayload("open", "abc123", true),
			wantDraft: true,
		},
		{
			name:      "draft=false",
			payload:   projectHookPayload("open", "abc123", false),
			wantDraft: false,
		},
		{
			name:      "work_in_progress=true (legacy)",
			payload:   wipPayload(),
			wantDraft: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			ev, err := NormalizeWebhook(tc.payload, "Merge Request Hook", "project")
			if err != nil {
				t.Fatalf("NormalizeWebhook: %v", err)
			}

			if ev.IsDraft != tc.wantDraft {
				t.Errorf("IsDraft = %v, want %v", ev.IsDraft, tc.wantDraft)
			}

			// Draft status should not affect the idempotency key.
			if ev.IdempotencyKey == "" {
				t.Error("IdempotencyKey should not be empty")
			}

			// Draft events should still produce valid fields.
			if ev.ProjectID != 100 {
				t.Errorf("ProjectID = %d, want 100", ev.ProjectID)
			}
			if ev.MRIID != 42 {
				t.Errorf("MRIID = %d, want 42", ev.MRIID)
			}
		})
	}
}

// TestHeadSHAFallback verifies that when object_attributes.last_commit.id is
// missing, the event marks head_sha for deferred lookup (VAL-INGRESS-012).
func TestHeadSHAFallback(t *testing.T) {
	tests := []struct {
		name         string
		headSHA      string
		wantSHA      string
		wantDeferred bool
	}{
		{
			name:         "head_sha present",
			headSHA:      "abc123def456",
			wantSHA:      "abc123def456",
			wantDeferred: false,
		},
		{
			name:         "head_sha missing",
			headSHA:      "",
			wantSHA:      "",
			wantDeferred: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			payload := projectHookPayload("open", tc.headSHA, false)
			ev, err := NormalizeWebhook(payload, "Merge Request Hook", "project")
			if err != nil {
				t.Fatalf("NormalizeWebhook: %v", err)
			}

			if ev.HeadSHA != tc.wantSHA {
				t.Errorf("HeadSHA = %q, want %q", ev.HeadSHA, tc.wantSHA)
			}
			if ev.HeadSHADeferred != tc.wantDeferred {
				t.Errorf("HeadSHADeferred = %v, want %v", ev.HeadSHADeferred, tc.wantDeferred)
			}

			// Even with deferred SHA, the idempotency key should be non-empty.
			if ev.IdempotencyKey == "" {
				t.Error("IdempotencyKey should not be empty for deferred SHA")
			}
		})
	}
}

// TestCrossSourceIdempotency verifies that equivalent triggers from different
// webhook sources (project, group, system) deduplicate to one normalized run
// key (VAL-INGRESS-015).
func TestCrossSourceIdempotency(t *testing.T) {
	headSHA := "abc123def456"

	projectEv, err := NormalizeWebhook(
		projectHookPayload("open", headSHA, false),
		"Merge Request Hook", "project",
	)
	if err != nil {
		t.Fatalf("project: %v", err)
	}

	groupEv, err := NormalizeWebhook(
		groupHookPayload("open", headSHA, false),
		"Merge Request Hook", "group",
	)
	if err != nil {
		t.Fatalf("group: %v", err)
	}

	systemEv, err := NormalizeWebhook(
		systemHookPayload("open", headSHA, false),
		"System Hook", "system",
	)
	if err != nil {
		t.Fatalf("system: %v", err)
	}

	// All three should produce the same idempotency key.
	if projectEv.IdempotencyKey != groupEv.IdempotencyKey {
		t.Errorf("project key (%s) != group key (%s)", projectEv.IdempotencyKey, groupEv.IdempotencyKey)
	}
	if projectEv.IdempotencyKey != systemEv.IdempotencyKey {
		t.Errorf("project key (%s) != system key (%s)", projectEv.IdempotencyKey, systemEv.IdempotencyKey)
	}

	// But hook sources should be different.
	if projectEv.HookSource == groupEv.HookSource {
		t.Errorf("hook sources should differ: project=%s, group=%s", projectEv.HookSource, groupEv.HookSource)
	}
	if projectEv.HookSource == systemEv.HookSource {
		t.Errorf("hook sources should differ: project=%s, system=%s", projectEv.HookSource, systemEv.HookSource)
	}
}

// TestCITriggerType verifies that a CI-originated trigger produces a distinct
// run with trigger_type=ci and does not collide with webhook triggers for the
// same HEAD (VAL-INGRESS-016).
func TestCITriggerType(t *testing.T) {
	headSHA := "abc123def456"

	// Webhook event for the same MR/HEAD.
	webhookEv, err := NormalizeWebhook(
		projectHookPayload("open", headSHA, false),
		"Merge Request Hook", "project",
	)
	if err != nil {
		t.Fatalf("webhook: %v", err)
	}

	// CI trigger for the same MR/HEAD.
	ciEv := NormalizeCITrigger(CITriggerInput{
		GitLabInstanceURL: "https://gitlab.example.com",
		ProjectID:         100,
		ProjectPath:       "mygroup/myrepo",
		MRIID:             42,
		HeadSHA:           headSHA,
	})

	// Idempotency keys MUST differ.
	if webhookEv.IdempotencyKey == ciEv.IdempotencyKey {
		t.Errorf("webhook and CI trigger should have different idempotency keys, both = %s",
			webhookEv.IdempotencyKey)
	}

	// CI event should have correct trigger type.
	if ciEv.TriggerType != "ci" {
		t.Errorf("CI trigger TriggerType = %q, want 'ci'", ciEv.TriggerType)
	}

	if ciEv.HookSource != "ci" {
		t.Errorf("CI trigger HookSource = %q, want 'ci'", ciEv.HookSource)
	}

	if ciEv.Action != "ci_trigger" {
		t.Errorf("CI trigger Action = %q, want 'ci_trigger'", ciEv.Action)
	}

	// CI event should have the correct fields.
	if ciEv.ProjectID != 100 {
		t.Errorf("CI trigger ProjectID = %d, want 100", ciEv.ProjectID)
	}
	if ciEv.MRIID != 42 {
		t.Errorf("CI trigger MRIID = %d, want 42", ciEv.MRIID)
	}
	if ciEv.HeadSHA != headSHA {
		t.Errorf("CI trigger HeadSHA = %q, want %q", ciEv.HeadSHA, headSHA)
	}
}

// TestCITriggerDeferredSHA verifies that a CI trigger with empty head_sha
// marks the event for deferred lookup.
func TestCITriggerDeferredSHA(t *testing.T) {
	ev := NormalizeCITrigger(CITriggerInput{
		GitLabInstanceURL: "https://gitlab.example.com",
		ProjectID:         100,
		ProjectPath:       "mygroup/myrepo",
		MRIID:             42,
		HeadSHA:           "",
	})

	if !ev.HeadSHADeferred {
		t.Error("HeadSHADeferred should be true when HeadSHA is empty")
	}
	if ev.IdempotencyKey == "" {
		t.Error("IdempotencyKey should not be empty for deferred SHA")
	}
}

// TestIdempotencyKeyDeterminism verifies that the same inputs always produce
// the same idempotency key.
func TestIdempotencyKeyDeterminism(t *testing.T) {
	payload := projectHookPayload("open", "deadbeef", false)

	ev1, err := NormalizeWebhook(payload, "Merge Request Hook", "project")
	if err != nil {
		t.Fatalf("first: %v", err)
	}

	ev2, err := NormalizeWebhook(payload, "Merge Request Hook", "project")
	if err != nil {
		t.Fatalf("second: %v", err)
	}

	if ev1.IdempotencyKey != ev2.IdempotencyKey {
		t.Errorf("idempotency keys should be identical: %s vs %s",
			ev1.IdempotencyKey, ev2.IdempotencyKey)
	}
}

// TestIdempotencyKeyDifferentSHA verifies that different head SHAs produce
// different idempotency keys.
func TestIdempotencyKeyDifferentSHA(t *testing.T) {
	ev1, _ := NormalizeWebhook(
		projectHookPayload("open", "sha111", false),
		"Merge Request Hook", "project",
	)
	ev2, _ := NormalizeWebhook(
		projectHookPayload("open", "sha222", false),
		"Merge Request Hook", "project",
	)

	if ev1.IdempotencyKey == ev2.IdempotencyKey {
		t.Errorf("different SHAs should produce different keys: both = %s", ev1.IdempotencyKey)
	}
}

// TestExtractInstanceURL verifies GitLab instance URL extraction from project web URLs.
func TestExtractInstanceURL(t *testing.T) {
	tests := []struct {
		webURL string
		want   string
	}{
		{"https://gitlab.example.com/group/repo", "https://gitlab.example.com"},
		{"https://gitlab.example.com/a/b/c", "https://gitlab.example.com"},
		{"https://gitlab.example.com", "https://gitlab.example.com"},
		{"http://localhost:8080/mygroup/myrepo", "http://localhost:8080"},
		{"", ""},
		{"not-a-url", ""},
	}

	for _, tc := range tests {
		t.Run(tc.webURL, func(t *testing.T) {
			got := extractInstanceURL(tc.webURL)
			if got != tc.want {
				t.Errorf("extractInstanceURL(%q) = %q, want %q", tc.webURL, got, tc.want)
			}
		})
	}
}

// TestNormalizeWebhookMalformedJSON verifies that malformed JSON returns an error.
func TestNormalizeWebhookMalformedJSON(t *testing.T) {
	_, err := NormalizeWebhook(json.RawMessage(`{bad json}`), "Merge Request Hook", "project")
	if err == nil {
		t.Error("expected error for malformed JSON")
	}
}

// TestIsMergeRequestEventTypeNormalization verifies the event type header
// classification.
func TestIsMergeRequestEventTypeNormalization(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"merge_request", true},
		{"Merge Request Hook", true},
		{"merge request hook", true},
		{"System Hook", false}, // System Hook requires payload inspection.
		{"system hook", false},
		{"Pipeline Hook", false},
		{"Push Hook", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := IsMergeRequestEventType(tc.input)
			if got != tc.want {
				t.Errorf("IsMergeRequestEventType(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestIsSystemHookHeader verifies system hook header detection.
func TestIsSystemHookHeader(t *testing.T) {
	tests := []struct {
		input string
		want  bool
	}{
		{"System Hook", true},
		{"system hook", true},
		{"SYSTEM HOOK", true},
		{"Merge Request Hook", false},
		{"", false},
	}

	for _, tc := range tests {
		t.Run(tc.input, func(t *testing.T) {
			got := IsSystemHookHeader(tc.input)
			if got != tc.want {
				t.Errorf("IsSystemHookHeader(%q) = %v, want %v", tc.input, got, tc.want)
			}
		})
	}
}

// TestIsMergeRequestPayload verifies payload-based MR event detection.
func TestIsMergeRequestPayload(t *testing.T) {
	tests := []struct {
		name    string
		payload json.RawMessage
		want    bool
	}{
		{
			name:    "merge_request object_kind",
			payload: json.RawMessage(`{"object_kind":"merge_request"}`),
			want:    true,
		},
		{
			name:    "pipeline object_kind",
			payload: json.RawMessage(`{"object_kind":"pipeline"}`),
			want:    false,
		},
		{
			name:    "push object_kind",
			payload: json.RawMessage(`{"object_kind":"push"}`),
			want:    false,
		},
		{
			name:    "malformed JSON",
			payload: json.RawMessage(`{bad}`),
			want:    false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := IsMergeRequestPayload(tc.payload)
			if got != tc.want {
				t.Errorf("IsMergeRequestPayload = %v, want %v", got, tc.want)
			}
		})
	}
}

// TestDraftDoesNotAffectIdempotencyKey verifies that draft status has no
// effect on the idempotency key.
func TestDraftDoesNotAffectIdempotencyKey(t *testing.T) {
	draftEv, _ := NormalizeWebhook(
		projectHookPayload("open", "abc123", true),
		"Merge Request Hook", "project",
	)
	nonDraftEv, _ := NormalizeWebhook(
		projectHookPayload("open", "abc123", false),
		"Merge Request Hook", "project",
	)

	if draftEv.IdempotencyKey != nonDraftEv.IdempotencyKey {
		t.Errorf("draft status should not affect idempotency key: draft=%s, non-draft=%s",
			draftEv.IdempotencyKey, nonDraftEv.IdempotencyKey)
	}
}

// TestActionDoesNotAffectIdempotencyKey verifies that different actions (open/update)
// for the same MR + SHA produce the same idempotency key. The action is a lifecycle
// event, not part of the idempotency scope.
func TestActionDoesNotAffectIdempotencyKey(t *testing.T) {
	openEv, _ := NormalizeWebhook(
		projectHookPayload("open", "abc123", false),
		"Merge Request Hook", "project",
	)
	updateEv, _ := NormalizeWebhook(
		projectHookPayload("update", "abc123", false),
		"Merge Request Hook", "project",
	)

	if openEv.IdempotencyKey != updateEv.IdempotencyKey {
		t.Errorf("action should not affect idempotency key: open=%s, update=%s",
			openEv.IdempotencyKey, updateEv.IdempotencyKey)
	}
}

// --- Helpers ---

type expectedFields struct {
	instanceURL     string
	projectID       int64
	projectPath     string
	mrIID           int64
	action          string
	headSHA         string
	headSHADeferred bool
	isDraft         bool
	hookSource      string
	triggerType     string
	title           string
}

func assertEventFields(t *testing.T, ev NormalizedEvent, want expectedFields) {
	t.Helper()

	if ev.GitLabInstanceURL != want.instanceURL {
		t.Errorf("GitLabInstanceURL = %q, want %q", ev.GitLabInstanceURL, want.instanceURL)
	}
	if ev.ProjectID != want.projectID {
		t.Errorf("ProjectID = %d, want %d", ev.ProjectID, want.projectID)
	}
	if ev.ProjectPath != want.projectPath {
		t.Errorf("ProjectPath = %q, want %q", ev.ProjectPath, want.projectPath)
	}
	if ev.MRIID != want.mrIID {
		t.Errorf("MRIID = %d, want %d", ev.MRIID, want.mrIID)
	}
	if ev.Action != want.action {
		t.Errorf("Action = %q, want %q", ev.Action, want.action)
	}
	if ev.HeadSHA != want.headSHA {
		t.Errorf("HeadSHA = %q, want %q", ev.HeadSHA, want.headSHA)
	}
	if ev.HeadSHADeferred != want.headSHADeferred {
		t.Errorf("HeadSHADeferred = %v, want %v", ev.HeadSHADeferred, want.headSHADeferred)
	}
	if ev.IsDraft != want.isDraft {
		t.Errorf("IsDraft = %v, want %v", ev.IsDraft, want.isDraft)
	}
	if ev.HookSource != want.hookSource {
		t.Errorf("HookSource = %q, want %q", ev.HookSource, want.hookSource)
	}
	if ev.TriggerType != want.triggerType {
		t.Errorf("TriggerType = %q, want %q", ev.TriggerType, want.triggerType)
	}
	if want.title != "" && ev.Title != want.title {
		t.Errorf("Title = %q, want %q", ev.Title, want.title)
	}
	if ev.IdempotencyKey == "" {
		t.Error("IdempotencyKey should not be empty")
	}
}
