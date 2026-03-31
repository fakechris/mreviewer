package github

type PullRequestUser struct {
	Login string `json:"login"`
}

type PullRequest struct {
	ID          int64           `json:"id"`
	Number      int64           `json:"number"`
	Title       string          `json:"title"`
	Body        string          `json:"body"`
	State       string          `json:"state"`
	Draft       bool            `json:"draft"`
	HTMLURL     string          `json:"html_url"`
	BaseRefName string          `json:"base_ref_name"`
	BaseSHA     string          `json:"base_sha"`
	HeadRefName string          `json:"head_ref_name"`
	HeadSHA     string          `json:"head_sha"`
	User        PullRequestUser `json:"user"`
}

type PullRequestFile struct {
	Filename         string `json:"filename"`
	PreviousFilename string `json:"previous_filename,omitempty"`
	Status           string `json:"status"`
	Patch            string `json:"patch,omitempty"`
	Generated        bool   `json:"generated,omitempty"`
	Removed          bool   `json:"removed,omitempty"`
	Renamed          bool   `json:"renamed,omitempty"`
}

type PullRequestSnapshot struct {
	PullRequest PullRequest       `json:"pull_request"`
	Files       []PullRequestFile `json:"files,omitempty"`
}
