package reviewcore

type Platform string

const (
	PlatformGitHub Platform = "github"
	PlatformGitLab Platform = "gitlab"
)

type ReviewTarget struct {
	Platform     Platform `json:"platform"`
	URL          string   `json:"url"`
	Repository   string   `json:"repository,omitempty"`
	ProjectID    int64    `json:"project_id,omitempty"`
	ChangeNumber int64    `json:"change_number"`
	BaseURL      string   `json:"base_url,omitempty"`
}
