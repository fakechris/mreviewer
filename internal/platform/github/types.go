package github

type PullRequestUser struct {
	Login string `json:"login"`
	ID    int64  `json:"id,omitempty"`
	Name  string `json:"name,omitempty"`
	Email string `json:"email,omitempty"`
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
	HeadCommit  PullRequestCommit `json:"head_commit,omitempty"`
	Files       []PullRequestFile `json:"files,omitempty"`
}

type PullRequestCommit struct {
	SHA       string          `json:"sha"`
	Title     string          `json:"title,omitempty"`
	Message   string          `json:"message,omitempty"`
	Author    PullRequestUser `json:"author"`
	Committer PullRequestUser `json:"committer"`
}

type IssueComment struct {
	ID   int64           `json:"id"`
	Body string          `json:"body"`
	User PullRequestUser `json:"user"`
}

type ReviewComment struct {
	ID        int64           `json:"id"`
	Body      string          `json:"body"`
	Path      string          `json:"path"`
	Line      int             `json:"line"`
	StartLine int             `json:"start_line"`
	Side      string          `json:"side"`
	StartSide string          `json:"start_side"`
	User      PullRequestUser `json:"user"`
}
