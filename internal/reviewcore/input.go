package reviewcore

import "encoding/json"

type ReviewInputSection struct {
	ID       string            `json:"id"`
	Kind     string            `json:"kind,omitempty"`
	CacheKey string            `json:"cache_key,omitempty"`
	Volatile bool              `json:"volatile,omitempty"`
	Content  string            `json:"content,omitempty"`
	Metadata map[string]string `json:"metadata,omitempty"`
}

type ReviewInput struct {
	Target         ReviewTarget         `json:"target"`
	Snapshot       PlatformSnapshot     `json:"snapshot"`
	Metadata       map[string]string    `json:"metadata,omitempty"`
	Policy         map[string]any       `json:"policy,omitempty"`
	SystemPrompt   string               `json:"system_prompt,omitempty"`
	RequestPayload json.RawMessage      `json:"request_payload,omitempty"`
	ContextText    string               `json:"context_text,omitempty"`
	Sections       []ReviewInputSection `json:"sections,omitempty"`
}
