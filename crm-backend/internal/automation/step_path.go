package automation

import (
	"fmt"
	"strconv"
	"strings"
)

// StepPathSegment represents one segment of a pipe-delimited step path.
// Root-level steps have only Index. Branch steps have Branch ("yes"/"no") + Index.
type StepPathSegment struct {
	Branch string // "" for root, "yes" or "no" for branches
	Index  int
}

// FormatStepPath encodes segments into the pipe-delimited path format.
//
// Encoding convention (pipe-delimited):
//   - Root step at index 0:          "0"
//   - Root[1] → yes branch → idx 2: "1|yes|2"
//   - Root[0] → yes[0] → no[1]:     "0|yes|0|no|1"
//
// Why pipes (|):
//   - Dot (.) clashes with JSON paths (contact.email)
//   - Slash (/) conflicts with URLs (/api/workflows/...)
//   - Pipe is URL-safe, not used in JSON keys, and visually distinct
func FormatStepPath(segments []StepPathSegment) string {
	if len(segments) == 0 {
		return ""
	}
	var parts []string
	for _, seg := range segments {
		if seg.Branch != "" {
			parts = append(parts, seg.Branch, strconv.Itoa(seg.Index))
		} else {
			parts = append(parts, strconv.Itoa(seg.Index))
		}
	}
	return strings.Join(parts, "|")
}

// ParseStepPath decodes a pipe-delimited step path into segments.
// Returns an error for empty strings or malformed paths.
func ParseStepPath(path string) ([]StepPathSegment, error) {
	if path == "" {
		return nil, fmt.Errorf("step path must not be empty")
	}

	parts := strings.Split(path, "|")
	var segments []StepPathSegment

	i := 0
	for i < len(parts) {
		if parts[i] == "yes" || parts[i] == "no" {
			if i+1 >= len(parts) {
				return nil, fmt.Errorf("branch '%s' at position %d has no index", parts[i], i)
			}
			idx, err := strconv.Atoi(parts[i+1])
			if err != nil {
				return nil, fmt.Errorf("invalid index '%s' after branch '%s'", parts[i+1], parts[i])
			}
			segments = append(segments, StepPathSegment{Branch: parts[i], Index: idx})
			i += 2
		} else {
			idx, err := strconv.Atoi(parts[i])
			if err != nil {
				return nil, fmt.Errorf("invalid segment '%s' at position %d", parts[i], i)
			}
			segments = append(segments, StepPathSegment{Index: idx})
			i++
		}
	}

	if len(segments) == 0 {
		return nil, fmt.Errorf("step path produced no segments")
	}
	return segments, nil
}

// BuildStepPath creates a child path by appending a branch+index to a parent path.
// If parentPath is empty and branch is empty, returns just the index (root level).
func BuildStepPath(parentPath string, branch string, index int) string {
	var suffix string
	if branch != "" {
		suffix = branch + "|" + strconv.Itoa(index)
	} else {
		suffix = strconv.Itoa(index)
	}
	if parentPath == "" {
		return suffix
	}
	return parentPath + "|" + suffix
}
