package gitlab

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mreviewer/mreviewer/internal/reviewcomment"
	core "github.com/mreviewer/mreviewer/internal/reviewcore"
)

type WriteRequests struct {
	Discussions []reviewcomment.CreateDiscussionRequest
	Notes       []reviewcomment.CreateNoteRequest
}

type Writer struct{}

func NewWriter() *Writer { return &Writer{} }

func (w *Writer) BuildRequests(bundle core.ReviewBundle) (WriteRequests, error) {
	if bundle.Target.ProjectID <= 0 {
		return WriteRequests{}, fmt.Errorf("gitlab writer: target project_id is required")
	}
	if bundle.Target.ChangeNumber <= 0 {
		return WriteRequests{}, fmt.Errorf("gitlab writer: target change_number is required")
	}
	reqs := WriteRequests{}
	for _, candidate := range bundle.PublishCandidates {
		body := candidateRequestBody(candidate)
		if body == "" {
			continue
		}
		switch candidate.Kind {
		case "summary":
			reqs.Notes = append(reqs.Notes, reviewcomment.CreateNoteRequest{
				ProjectID:       bundle.Target.ProjectID,
				MergeRequestIID: bundle.Target.ChangeNumber,
				Body:            body,
			})
		case "finding":
			if candidate.PublishAsSummary {
				reqs.Notes = append(reqs.Notes, reviewcomment.CreateNoteRequest{
					ProjectID:       bundle.Target.ProjectID,
					MergeRequestIID: bundle.Target.ChangeNumber,
					Body:            body,
				})
				continue
			}
			position, err := buildPosition(candidate.Location)
			if err != nil {
				return WriteRequests{}, err
			}
			reqs.Discussions = append(reqs.Discussions, reviewcomment.CreateDiscussionRequest{
				ProjectID:       bundle.Target.ProjectID,
				MergeRequestIID: bundle.Target.ChangeNumber,
				Body:            body,
				Position:        position,
			})
		}
	}
	return reqs, nil
}

func candidateRequestBody(candidate core.PublishCandidate) string {
	body := strings.TrimSpace(candidate.Body)
	if body != "" {
		return body
	}
	title := strings.TrimSpace(candidate.Title)
	if title == "" {
		return ""
	}
	if candidate.Kind == "summary" {
		return title
	}
	return "### " + title
}

type gitlabPositionMetadata struct {
	BaseSHA   string                   `json:"base_sha"`
	StartSHA  string                   `json:"start_sha"`
	HeadSHA   string                   `json:"head_sha"`
	OldPath   string                   `json:"old_path"`
	NewPath   string                   `json:"new_path"`
	OldLine   *int32                   `json:"old_line,omitempty"`
	NewLine   *int32                   `json:"new_line,omitempty"`
	LineRange *reviewcomment.LineRange `json:"line_range,omitempty"`
}

func buildPosition(location core.CanonicalLocation) (reviewcomment.Position, error) {
	position := reviewcomment.Position{
		PositionType: "text",
		OldPath:      strings.TrimSpace(location.Path),
		NewPath:      strings.TrimSpace(location.Path),
	}

	if location.StartLine > 0 || location.EndLine > 0 {
		switch location.Side {
		case core.DiffSideOld:
			position.OldLine = int32Ptr(location.StartLine)
		case core.DiffSideNew:
			position.NewLine = int32Ptr(location.StartLine)
		default:
			position.NewLine = int32Ptr(location.StartLine)
		}
	} else {
		position.PositionType = "file"
	}

	if len(location.PlatformMetadata) == 0 {
		return position, nil
	}

	var metadata gitlabPositionMetadata
	if err := json.Unmarshal(location.PlatformMetadata, &metadata); err != nil {
		return reviewcomment.Position{}, fmt.Errorf("gitlab writer: parse platform metadata: %w", err)
	}

	if strings.TrimSpace(metadata.BaseSHA) != "" {
		position.BaseSHA = strings.TrimSpace(metadata.BaseSHA)
	}
	if strings.TrimSpace(metadata.StartSHA) != "" {
		position.StartSHA = strings.TrimSpace(metadata.StartSHA)
	}
	if strings.TrimSpace(metadata.HeadSHA) != "" {
		position.HeadSHA = strings.TrimSpace(metadata.HeadSHA)
	}
	if strings.TrimSpace(metadata.OldPath) != "" {
		position.OldPath = strings.TrimSpace(metadata.OldPath)
	}
	if strings.TrimSpace(metadata.NewPath) != "" {
		position.NewPath = strings.TrimSpace(metadata.NewPath)
	}
	if metadata.OldLine != nil {
		position.OldLine = metadata.OldLine
	}
	if metadata.NewLine != nil {
		position.NewLine = metadata.NewLine
	}
	if metadata.LineRange != nil {
		position.LineRange = metadata.LineRange
		position.OldLine = metadata.LineRange.End.OldLine
		position.NewLine = metadata.LineRange.End.NewLine
		position.PositionType = "text"
	}

	return position, nil
}

func int32Ptr(v int) *int32 {
	if v <= 0 {
		return nil
	}
	value := int32(v)
	return &value
}
