package reviewcore

import (
	"strconv"
	"strings"

	ctxpkg "github.com/mreviewer/mreviewer/internal/context"
)

// locationVerdict is the result of checking a finding's location against the
// hunks that were actually part of the review request.
type locationVerdict int

const (
	// locationUnknown means the index cannot judge the location: the file is
	// not in the request, its hunks were collapsed, or no line was given.
	locationUnknown locationVerdict = iota
	// locationInDiff means the anchor line is a changed line, a deletion
	// anchor, or lies inside a truncated hunk whose exact lines are unknown.
	locationInDiff
	// locationOutOfDiff means the file was reviewed with full hunks and the
	// anchor line is not one the change touched.
	locationOutOfDiff
)

// lineSpan is an inclusive [Start, End] range on one side of the diff.
type lineSpan struct{ Start, End int }

type fileLineIndex struct {
	oldLines map[int]bool
	newLines map[int]bool
	// anchors are new-side lines next to a pure deletion, so a finding about
	// removed code can still be anchored on the new file (OpenQodex's
	// deletion-point rule).
	anchors map[int]bool
	// spans are the hunk ranges per side; partial spans belong to truncated
	// hunks whose individual lines are not known.
	oldSpans, newSpans     []lineSpan
	partialOld, partialNew []lineSpan
}

// changedLineIndex maps a path to the lines the change touched, built from the
// hunks the assembler put in front of the reviewer.
type changedLineIndex struct {
	files map[string]*fileLineIndex
}

func buildChangedLineIndex(request ctxpkg.ReviewRequest) changedLineIndex {
	idx := changedLineIndex{files: map[string]*fileLineIndex{}}
	for _, change := range request.Changes {
		if len(change.Hunks) == 0 {
			// Collapsed, generated or too-large files carry no hunks: the model
			// may still have seen them through other context, so stay unknown.
			continue
		}
		file := &fileLineIndex{
			oldLines: map[int]bool{},
			newLines: map[int]bool{},
			anchors:  map[int]bool{},
		}
		for _, hunk := range change.Hunks {
			indexHunk(file, hunk)
		}
		for _, path := range []string{change.Path, change.OldPath} {
			path = strings.TrimSpace(path)
			if path != "" {
				idx.files[path] = file
			}
		}
	}
	return idx
}

func indexHunk(file *fileLineIndex, hunk ctxpkg.Hunk) {
	oldSpan := lineSpan{Start: hunk.OldStart, End: hunk.OldStart + maxInt(hunk.OldLines, 1) - 1}
	newSpan := lineSpan{Start: hunk.NewStart, End: hunk.NewStart + maxInt(hunk.NewLines, 1) - 1}
	if hunk.Truncated {
		file.partialOld = append(file.partialOld, oldSpan)
		file.partialNew = append(file.partialNew, newSpan)
		return
	}
	file.oldSpans = append(file.oldSpans, oldSpan)
	file.newSpans = append(file.newSpans, newSpan)

	oldN, newN := hunk.OldStart, hunk.NewStart
	pendingDeletion := false
	lines := strings.Split(hunk.Patch, "\n")
	for i, line := range lines {
		if i == 0 && strings.HasPrefix(line, "@@") {
			// Header may re-state the starts; trust it when present.
			if o, n, ok := parseHunkHeader(line); ok {
				oldN, newN = o, n
			}
			continue
		}
		if line == "" || strings.HasPrefix(line, `\`) {
			continue
		}
		switch line[0] {
		case '-':
			file.oldLines[oldN] = true
			oldN++
			pendingDeletion = true
		case '+':
			file.newLines[newN] = true
			newN++
			if pendingDeletion {
				// A replaced block: the added lines are the anchor.
				pendingDeletion = false
			}
		default: // context
			if pendingDeletion {
				if newN-1 >= hunk.NewStart {
					file.anchors[newN-1] = true
				}
				file.anchors[newN] = true
				pendingDeletion = false
			}
			oldN++
			newN++
		}
	}
	if pendingDeletion {
		// Deletion at the very end of the hunk: anchor on the last new line.
		if newN-1 >= hunk.NewStart {
			file.anchors[newN-1] = true
		}
		file.anchors[newN] = true
	}
}

// parseHunkHeader reads "@@ -a[,b] +c[,d] @@" and returns a and c.
func parseHunkHeader(header string) (oldStart, newStart int, ok bool) {
	fields := strings.Fields(header)
	if len(fields) < 3 || !strings.HasPrefix(fields[1], "-") || !strings.HasPrefix(fields[2], "+") {
		return 0, 0, false
	}
	parse := func(s string) (int, bool) {
		s = strings.TrimLeft(s, "-+")
		if i := strings.IndexByte(s, ','); i >= 0 {
			s = s[:i]
		}
		n, err := strconv.Atoi(s)
		return n, err == nil
	}
	o, ok1 := parse(fields[1])
	n, ok2 := parse(fields[2])
	if !ok1 || !ok2 {
		return 0, 0, false
	}
	return o, n, true
}

// verify reports whether loc points at a line the change touched.
func (idx changedLineIndex) verify(loc CanonicalLocation) locationVerdict {
	path := strings.TrimSpace(loc.Path)
	if path == "" || loc.StartLine <= 0 {
		return locationUnknown
	}
	file, ok := idx.files[path]
	if !ok {
		return locationUnknown
	}
	end := maxInt(loc.StartLine, loc.EndLine)
	switch loc.Side {
	case DiffSideOld:
		if file.oldLines[loc.StartLine] && withinSpans(file.oldSpans, loc.StartLine, end) {
			return locationInDiff
		}
		if withinSpans(file.partialOld, loc.StartLine, end) {
			return locationInDiff
		}
		return locationOutOfDiff
	default:
		if (file.newLines[loc.StartLine] || file.anchors[loc.StartLine]) && withinSpans(file.newSpans, loc.StartLine, end) {
			return locationInDiff
		}
		if withinSpans(file.partialNew, loc.StartLine, end) {
			return locationInDiff
		}
		return locationOutOfDiff
	}
}

// withinSpans is true when [start, end] fits inside one span.
func withinSpans(spans []lineSpan, start, end int) bool {
	for _, span := range spans {
		if start >= span.Start && end <= span.End {
			return true
		}
	}
	return false
}
