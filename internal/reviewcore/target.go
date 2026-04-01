package reviewcore

type Platform string

const (
	PlatformGitHub Platform = "github"
	PlatformGitLab Platform = "gitlab"
)

type ReviewTarget struct {
	Platform   Platform `json:"platform"`
	Repository string   `json:"repository"`
	Number     int64    `json:"number"`
	URL        string   `json:"url"`
}
