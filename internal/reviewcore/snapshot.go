package reviewcore

type PlatformSnapshot struct {
	BaseSHA          string            `json:"base_sha,omitempty"`
	HeadSHA          string            `json:"head_sha,omitempty"`
	StartSHA         string            `json:"start_sha,omitempty"`
	Title            string            `json:"title,omitempty"`
	Description      string            `json:"description,omitempty"`
	SourceBranch     string            `json:"source_branch,omitempty"`
	TargetBranch     string            `json:"target_branch,omitempty"`
	RepositoryWebURL string            `json:"repository_web_url,omitempty"`
	Metadata         map[string]string `json:"metadata,omitempty"`
	Opaque           any               `json:"-"`
}
