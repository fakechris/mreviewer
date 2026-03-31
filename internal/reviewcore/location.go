package reviewcore

import "encoding/json"

type DiffSide string

const (
	DiffSideOld DiffSide = "old"
	DiffSideNew DiffSide = "new"
)

type CanonicalLocation struct {
	Path             string          `json:"path"`
	Side             DiffSide        `json:"side,omitempty"`
	StartLine        int             `json:"start_line,omitempty"`
	EndLine          int             `json:"end_line,omitempty"`
	Snippet          string          `json:"snippet,omitempty"`
	PlatformMetadata json.RawMessage `json:"platform_metadata,omitempty"`
}
