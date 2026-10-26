// Package reviewbench scores a review engine against an offline corpus of
// pull requests with expert-labelled findings (AACR-Bench samples, Chinese
// samples and "correct but noisy" control cases). It answers four questions
// per route: how many reported issues are real, how many known issues were
// found, how much a human has to read, and what it cost.
package reviewbench

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

// Case kinds. Golden cases carry expert findings; control cases are correct
// changes where every finding is a false positive; chinese cases are golden
// cases whose findings are also scored for Chinese naturalness.
const (
	KindGolden  = "golden"
	KindControl = "control"
	KindChinese = "chinese"
)

// Comment is one labelled review comment from the corpus.
type Comment struct {
	Path     string `json:"path"`
	Side     string `json:"side,omitempty"` // left | right
	FromLine int    `json:"from_line"`
	ToLine   int    `json:"to_line"`
	Category string `json:"category,omitempty"`
	Context  string `json:"context,omitempty"`
	Note     string `json:"note"`
	Source   string `json:"source,omitempty"`
}

type CaseDiff struct {
	OldPath     string `json:"old_path,omitempty"`
	NewPath     string `json:"new_path"`
	Diff        string `json:"diff"`
	NewFile     bool   `json:"new_file,omitempty"`
	DeletedFile bool   `json:"deleted_file,omitempty"`
	RenamedFile bool   `json:"renamed_file,omitempty"`
}

type CasePR struct {
	URL          string `json:"url"`
	Owner        string `json:"owner,omitempty"`
	Repo         string `json:"repo,omitempty"`
	Number       int64  `json:"number"`
	BaseSHA      string `json:"base_sha,omitempty"`
	HeadSHA      string `json:"head_sha,omitempty"`
	Title        string `json:"title,omitempty"`
	Description  string `json:"description,omitempty"`
	Language     string `json:"language,omitempty"`
	Category     string `json:"category,omitempty"`
	ChangedLines int    `json:"changed_lines,omitempty"`
}

// Case is one case.json on disk.
type Case struct {
	ID        string     `json:"id"`
	Source    string     `json:"source,omitempty"`
	Kind      string     `json:"kind,omitempty"`
	AddedAt   string     `json:"added_at,omitempty"`
	PR        CasePR     `json:"pr"`
	Diffs     []CaseDiff `json:"diffs"`
	Golden    []Comment  `json:"golden,omitempty"`
	Negatives []Comment  `json:"negatives,omitempty"`

	Dir string `json:"-"`
}

// LoadCases walks root for case.json files. since ("YYYY-MM-DD") keeps only
// cases added on or after that date, so a re-run after extending the corpus
// can score the new cases alone.
func LoadCases(root, since string) ([]Case, error) {
	var cases []Case
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || d.Name() != "case.json" {
			return nil
		}
		c, err := LoadCase(path)
		if err != nil {
			return err
		}
		if since != "" && c.AddedAt != "" && c.AddedAt < since {
			return nil
		}
		cases = append(cases, c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(cases, func(i, j int) bool { return cases[i].ID < cases[j].ID })
	return cases, nil
}

func LoadCase(path string) (Case, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Case{}, err
	}
	var c Case
	if err := json.Unmarshal(data, &c); err != nil {
		return Case{}, fmt.Errorf("reviewbench: parse %s: %w", path, err)
	}
	if strings.TrimSpace(c.ID) == "" {
		return Case{}, fmt.Errorf("reviewbench: %s: id is required", path)
	}
	if c.Kind == "" {
		c.Kind = KindGolden
	}
	if c.Kind == KindControl && len(c.Golden) > 0 {
		return Case{}, fmt.Errorf("reviewbench: %s: a control case must not carry golden findings", path)
	}
	c.Dir = filepath.Dir(path)
	return c, nil
}

// Snapshot turns the case into the platform snapshot the review input
// builder expects, so the engine sees exactly what it would see for a live
// GitHub pull request.
func (c Case) Snapshot() core.PlatformSnapshot {
	repo := strings.TrimSpace(c.PR.Owner + "/" + c.PR.Repo)
	if repo == "/" {
		repo = ""
	}
	snapshot := core.PlatformSnapshot{
		Target: core.ReviewTarget{
			Platform:     core.PlatformGitHub,
			URL:          c.PR.URL,
			Repository:   repo,
			ChangeNumber: c.PR.Number,
		},
		Change: core.PlatformChange{
			Number:      c.PR.Number,
			Title:       c.PR.Title,
			Description: c.PR.Description,
			HeadSHA:     c.PR.HeadSHA,
			WebURL:      c.PR.URL,
		},
		Version: core.PlatformVersion{
			BaseSHA:  c.PR.BaseSHA,
			StartSHA: c.PR.BaseSHA,
			HeadSHA:  c.PR.HeadSHA,
		},
	}
	for _, d := range c.Diffs {
		snapshot.Diffs = append(snapshot.Diffs, core.PlatformDiff{
			OldPath:     d.OldPath,
			NewPath:     d.NewPath,
			Diff:        d.Diff,
			NewFile:     d.NewFile,
			DeletedFile: d.DeletedFile,
			RenamedFile: d.RenamedFile,
		})
	}
	return snapshot
}
