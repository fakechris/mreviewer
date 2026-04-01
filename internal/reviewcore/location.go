package reviewcore

type LocationSide string

const (
	LocationSideOld LocationSide = "old"
	LocationSideNew LocationSide = "new"
)

type CanonicalLocation struct {
	Path             string         `json:"path,omitempty"`
	Side             LocationSide   `json:"side,omitempty"`
	Line             int            `json:"line,omitempty"`
	EndLine          int            `json:"end_line,omitempty"`
	PlatformMetadata map[string]any `json:"platform_metadata,omitempty"`
}
