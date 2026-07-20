package ai

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// The reported case: the reply ran out of budget inside a UUID link and the raw
// "[ABC OK](#203f05b4-30ac-4c14-b53f-" reached the user's screen.
func TestFinalizeTruncatedReply_StripsHangingRecordLink(t *testing.T) {
	got := finalizeTruncatedReply(
		"**Becue Company Contacts**\n\n| Contact | Company |\n| --- | --- |\n| [ABC OK](#203f05b4-30ac-4c14-b53f-")

	assert.NotContains(t, got, "203f05b4", "the partial uuid must not survive")
	assert.NotContains(t, got, "[ABC OK]", "the broken link must not survive")
	assert.Contains(t, got, "Becue Company Contacts", "content before the cut is kept")
	assert.Contains(t, got, truncationNotice)
}

func TestFinalizeTruncatedReply(t *testing.T) {
	tests := []struct {
		name        string
		in          string
		wantContent string // what must remain, "" if only the notice
		wantGone    string // must not appear, "" to skip
	}{
		{
			name:        "complete link is left alone",
			in:          "Here is one:\n\n[ABC OK](#203f05b4-30ac-4c14-b53f-9e2d1c4a7b60)",
			wantContent: "[ABC OK](#203f05b4-30ac-4c14-b53f-9e2d1c4a7b60)",
		},
		{
			name:        "cut mid-sentence keeps the prose",
			in:          "The pipeline is looking healthy and the top deal is",
			wantContent: "The pipeline is looking healthy and the top deal is",
		},
		{
			name:        "empty table row is peeled back",
			in:          "| Contact |\n| --- |\n| [X](#abc",
			wantContent: "| Contact |",
			wantGone:    "[X]",
		},
		{
			name:        "nothing but a broken link yields only the notice",
			in:          "[X](#abc",
			wantContent: "",
			wantGone:    "[X]",
		},
		{
			name:        "empty input yields only the notice",
			in:          "",
			wantContent: "",
		},
		{
			name:        "a bare bracket is not mistaken for a link",
			in:          "Results [see above]",
			wantContent: "Results [see above]",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := finalizeTruncatedReply(tt.in)
			assert.Contains(t, got, truncationNotice, "the user must always be told")
			if tt.wantContent != "" {
				assert.Contains(t, got, tt.wantContent)
			} else {
				assert.Equal(t, truncationNotice, got)
			}
			if tt.wantGone != "" {
				assert.NotContains(t, got, tt.wantGone)
			}
		})
	}
}

// Truncation was invisible because this field was never populated — the whole
// reason a half-written answer reached the user looking complete.
func TestParseCFResponse_ReportsFinishReason(t *testing.T) {
	t.Run("length is surfaced", func(t *testing.T) {
		content, _, _, reason := parseCFResponse([]byte(
			`{"choices":[{"finish_reason":"length","message":{"content":"cut off here"}}],"usage":{"prompt_tokens":5,"completion_tokens":9}}`))
		require.Equal(t, "cut off here", content)
		assert.Equal(t, finishReasonLength, reason)
	})

	t.Run("normal completion is not flagged", func(t *testing.T) {
		_, in, out, reason := parseCFResponse([]byte(
			`{"choices":[{"finish_reason":"stop","message":{"content":"all done"}}],"usage":{"prompt_tokens":5,"completion_tokens":9}}`))
		assert.Equal(t, "stop", reason)
		assert.NotEqual(t, finishReasonLength, reason)
		assert.Equal(t, 5, in)
		assert.Equal(t, 9, out)
	})

	t.Run("legacy workers ai format reports no reason", func(t *testing.T) {
		content, _, _, reason := parseCFResponse([]byte(
			`{"result":{"response":"legacy text","usage":{"prompt_tokens":1,"completion_tokens":2}}}`))
		require.Equal(t, "legacy text", content)
		assert.Empty(t, reason, "absent is not the same as truncated")
	})

	t.Run("garbage yields no reason", func(t *testing.T) {
		_, _, _, reason := parseCFResponse([]byte(`not json`))
		assert.Empty(t, reason)
	})
}

// The notice is user-facing prose; keep it from silently becoming a code-ish blob.
func TestTruncationNoticeIsReadable(t *testing.T) {
	assert.True(t, strings.HasPrefix(truncationNotice, "_"), "rendered as markdown emphasis")
	assert.Contains(t, truncationNotice, "cut short")
}
