package automation

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// ═══════════════════════════════════════════════════════════════════
// FormatStepPath
// ═══════════════════════════════════════════════════════════════════

func TestFormatStepPath_RootIndex0(t *testing.T) {
	path := FormatStepPath([]StepPathSegment{{Index: 0}})
	assert.Equal(t, "0", path)
}

func TestFormatStepPath_RootIndex3(t *testing.T) {
	path := FormatStepPath([]StepPathSegment{{Index: 3}})
	assert.Equal(t, "3", path)
}

func TestFormatStepPath_OneLevelYesBranch(t *testing.T) {
	path := FormatStepPath([]StepPathSegment{
		{Index: 1},
		{Branch: "yes", Index: 2},
	})
	assert.Equal(t, "1|yes|2", path)
}

func TestFormatStepPath_OneLevelNoBranch(t *testing.T) {
	path := FormatStepPath([]StepPathSegment{
		{Index: 0},
		{Branch: "no", Index: 0},
	})
	assert.Equal(t, "0|no|0", path)
}

func TestFormatStepPath_TwoLevelNested(t *testing.T) {
	path := FormatStepPath([]StepPathSegment{
		{Index: 0},
		{Branch: "yes", Index: 0},
		{Branch: "no", Index: 1},
	})
	assert.Equal(t, "0|yes|0|no|1", path)
}

func TestFormatStepPath_DeepNesting5Levels(t *testing.T) {
	path := FormatStepPath([]StepPathSegment{
		{Index: 0},
		{Branch: "yes", Index: 0},
		{Branch: "no", Index: 1},
		{Branch: "yes", Index: 0},
		{Branch: "no", Index: 2},
		{Branch: "yes", Index: 0},
	})
	assert.Equal(t, "0|yes|0|no|1|yes|0|no|2|yes|0", path)
}

func TestFormatStepPath_EmptySegments(t *testing.T) {
	path := FormatStepPath(nil)
	assert.Equal(t, "", path)
}

// ═══════════════════════════════════════════════════════════════════
// ParseStepPath
// ═══════════════════════════════════════════════════════════════════

func TestParseStepPath_Root0(t *testing.T) {
	segs, err := ParseStepPath("0")
	require.NoError(t, err)
	assert.Equal(t, []StepPathSegment{{Index: 0}}, segs)
}

func TestParseStepPath_Root3(t *testing.T) {
	segs, err := ParseStepPath("3")
	require.NoError(t, err)
	assert.Equal(t, []StepPathSegment{{Index: 3}}, segs)
}

func TestParseStepPath_OneLevelYes(t *testing.T) {
	segs, err := ParseStepPath("1|yes|2")
	require.NoError(t, err)
	assert.Equal(t, []StepPathSegment{
		{Index: 1},
		{Branch: "yes", Index: 2},
	}, segs)
}

func TestParseStepPath_TwoLevelNested(t *testing.T) {
	segs, err := ParseStepPath("0|yes|0|no|1")
	require.NoError(t, err)
	assert.Equal(t, []StepPathSegment{
		{Index: 0},
		{Branch: "yes", Index: 0},
		{Branch: "no", Index: 1},
	}, segs)
}

func TestParseStepPath_Empty(t *testing.T) {
	_, err := ParseStepPath("")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "must not be empty")
}

func TestParseStepPath_BranchWithoutIndex(t *testing.T) {
	_, err := ParseStepPath("0|yes")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "has no index")
}

func TestParseStepPath_InvalidSegment(t *testing.T) {
	_, err := ParseStepPath("0|foo|1")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid segment")
}

func TestParseStepPath_InvalidIndex(t *testing.T) {
	_, err := ParseStepPath("0|yes|abc")
	assert.Error(t, err)
	assert.Contains(t, err.Error(), "invalid index")
}

// ═══════════════════════════════════════════════════════════════════
// Round-trip
// ═══════════════════════════════════════════════════════════════════

func TestStepPath_RoundTrip(t *testing.T) {
	cases := []struct {
		name string
		path string
	}{
		{"root", "0"},
		{"one_level_yes", "1|yes|2"},
		{"one_level_no", "0|no|0"},
		{"two_level", "0|yes|0|no|1"},
		{"deep", "0|yes|0|no|1|yes|0|no|2|yes|0"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			segs, err := ParseStepPath(tc.path)
			require.NoError(t, err)
			result := FormatStepPath(segs)
			assert.Equal(t, tc.path, result)
		})
	}
}

// ═══════════════════════════════════════════════════════════════════
// BuildStepPath
// ═══════════════════════════════════════════════════════════════════

func TestBuildStepPath_RootLevel(t *testing.T) {
	path := BuildStepPath("", "", 0)
	assert.Equal(t, "0", path)
}

func TestBuildStepPath_YesBranch(t *testing.T) {
	path := BuildStepPath("0", "yes", 1)
	assert.Equal(t, "0|yes|1", path)
}

func TestBuildStepPath_NoBranch(t *testing.T) {
	path := BuildStepPath("0", "no", 0)
	assert.Equal(t, "0|no|0", path)
}

func TestBuildStepPath_NestedBuild(t *testing.T) {
	root := BuildStepPath("", "", 0)
	yes0 := BuildStepPath(root, "yes", 0)
	no1 := BuildStepPath(yes0, "no", 1)
	assert.Equal(t, "0", root)
	assert.Equal(t, "0|yes|0", yes0)
	assert.Equal(t, "0|yes|0|no|1", no1)
}

// ═══════════════════════════════════════════════════════════════════
// Convention: first step = "0", empty = error (pitfall #7)
// ═══════════════════════════════════════════════════════════════════

func TestStepPath_FirstStep_Convention(t *testing.T) {
	path := BuildStepPath("", "", 0)
	assert.Equal(t, "0", path)

	segs, err := ParseStepPath(path)
	require.NoError(t, err)
	assert.Len(t, segs, 1)
	assert.Equal(t, 0, segs[0].Index)
	assert.Equal(t, "", segs[0].Branch)
}

func TestStepPath_EmptyIsNeverValid(t *testing.T) {
	_, err := ParseStepPath("")
	assert.Error(t, err)
}

// ═══════════════════════════════════════════════════════════════════
// No delimiter collision
// ═══════════════════════════════════════════════════════════════════

func TestStepPath_NoDelimiterCollision(t *testing.T) {
	path := "2|yes|3|no|0|yes|1"
	segs, err := ParseStepPath(path)
	require.NoError(t, err)
	assert.Len(t, segs, 4)
	assert.Equal(t, path, FormatStepPath(segs))
}
