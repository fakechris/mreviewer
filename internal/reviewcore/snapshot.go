package reviewcore

import "encoding/json"

type PlatformAuthor struct {
	Username string `json:"username,omitempty"`
}

type PlatformChange struct {
	PlatformID    string                 `json:"platform_id,omitempty"`
	ProjectID     int64                  `json:"project_id,omitempty"`
	Number        int64                  `json:"number"`
	Title         string                 `json:"title,omitempty"`
	Description   string                 `json:"description,omitempty"`
	State         string                 `json:"state,omitempty"`
	Draft         bool                   `json:"draft,omitempty"`
	SourceBranch  string                 `json:"source_branch,omitempty"`
	TargetBranch  string                 `json:"target_branch,omitempty"`
	HeadSHA       string                 `json:"head_sha,omitempty"`
	WebURL        string                 `json:"web_url,omitempty"`
	Author        PlatformAuthor         `json:"author,omitempty"`
	BaseMetadata  json.RawMessage        `json:"base_metadata,omitempty"`
	ExtraMetadata map[string]interface{} `json:"extra_metadata,omitempty"`
}

type PlatformVersion struct {
	PlatformVersionID string          `json:"platform_version_id,omitempty"`
	BaseSHA           string          `json:"base_sha,omitempty"`
	StartSHA          string          `json:"start_sha,omitempty"`
	HeadSHA           string          `json:"head_sha,omitempty"`
	PatchIDSHA        string          `json:"patch_id_sha,omitempty"`
	BaseMetadata      json.RawMessage `json:"base_metadata,omitempty"`
}

type PlatformDiff struct {
	OldPath       string          `json:"old_path,omitempty"`
	NewPath       string          `json:"new_path,omitempty"`
	Diff          string          `json:"diff,omitempty"`
	AMode         string          `json:"a_mode,omitempty"`
	BMode         string          `json:"b_mode,omitempty"`
	NewFile       bool            `json:"new_file,omitempty"`
	RenamedFile   bool            `json:"renamed_file,omitempty"`
	DeletedFile   bool            `json:"deleted_file,omitempty"`
	GeneratedFile bool            `json:"generated_file,omitempty"`
	Collapsed     bool            `json:"collapsed,omitempty"`
	TooLarge      bool            `json:"too_large,omitempty"`
	BaseMetadata  json.RawMessage `json:"base_metadata,omitempty"`
}

type PlatformSnapshot struct {
	Target  ReviewTarget    `json:"target"`
	Change  PlatformChange  `json:"change"`
	Version PlatformVersion `json:"version"`
	Diffs   []PlatformDiff  `json:"diffs,omitempty"`
}
