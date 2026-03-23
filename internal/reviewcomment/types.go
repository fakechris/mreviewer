package reviewcomment

type CreateDiscussionRequest struct {
	ProjectID       int64    `json:"project_id"`
	MergeRequestIID int64    `json:"merge_request_iid"`
	Body            string   `json:"body"`
	Position        Position `json:"position"`
	ReviewFindingID int64    `json:"review_finding_id"`
	IdempotencyKey  string   `json:"idempotency_key"`
}

type CreateNoteRequest struct {
	ProjectID       int64  `json:"project_id"`
	MergeRequestIID int64  `json:"merge_request_iid"`
	Body            string `json:"body"`
	ReviewFindingID int64  `json:"review_finding_id"`
	IdempotencyKey  string `json:"idempotency_key"`
}

type ResolveDiscussionRequest struct {
	ProjectID       int64  `json:"project_id"`
	MergeRequestIID int64  `json:"merge_request_iid"`
	DiscussionID    string `json:"discussion_id"`
	Resolved        bool   `json:"resolved"`
}

type Position struct {
	PositionType string     `json:"position_type"`
	BaseSHA      string     `json:"base_sha"`
	StartSHA     string     `json:"start_sha"`
	HeadSHA      string     `json:"head_sha"`
	OldPath      string     `json:"old_path"`
	NewPath      string     `json:"new_path"`
	OldLine      *int32     `json:"old_line,omitempty"`
	NewLine      *int32     `json:"new_line,omitempty"`
	LineRange    *LineRange `json:"line_range,omitempty"`
}

type LineRange struct {
	Start RangeLine `json:"start"`
	End   RangeLine `json:"end"`
}

type RangeLine struct {
	LineCode string `json:"line_code"`
	LineType string `json:"type,omitempty"`
	OldLine  *int32 `json:"old_line,omitempty"`
	NewLine  *int32 `json:"new_line,omitempty"`
}

type Discussion struct {
	ID string `json:"id"`
}
