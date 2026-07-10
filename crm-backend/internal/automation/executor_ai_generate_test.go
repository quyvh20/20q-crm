package automation

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
)

// fakeGenerator captures the prompt + maxTokens and returns canned output/errors.
type fakeGenerator struct {
	lastPrompt string
	lastMax    int
	text       string
	retryable  bool
	err        error
}

func (f *fakeGenerator) GenerateText(_ context.Context, _, _ uuid.UUID, prompt string, maxTokens int) (string, bool, error) {
	f.lastPrompt, f.lastMax = prompt, maxTokens
	if f.err != nil {
		return "", f.retryable, f.err
	}
	return f.text, false, nil
}

func TestAIGenerate_InterpolatesPromptAndOutputsText(t *testing.T) {
	f := &fakeGenerator{text: "Hello Jane, thanks for your interest."}
	ex := NewAIGenerateExecutor(f)
	run := &WorkflowRun{ID: uuid.New(), OrgID: uuid.New()}
	action := ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{
		"prompt":     "Write a note to {{contact.first_name}}",
		"max_tokens": 300,
	}}
	evalCtx := EvalContext{Contact: map[string]any{"first_name": "Jane"}}

	out, err := ex.Execute(context.Background(), run, action, evalCtx)
	require.NoError(t, err)
	require.Equal(t, "Write a note to Jane", f.lastPrompt)
	require.Equal(t, 300, f.lastMax)
	require.Equal(t, "Hello Jane, thanks for your interest.", out.(map[string]any)["text"])
}

func TestAIGenerate_ClampsMaxTokens(t *testing.T) {
	f := &fakeGenerator{text: "ok"}
	ex := NewAIGenerateExecutor(f)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{"prompt": "hi", "max_tokens": 999999}},
		EvalContext{})
	require.NoError(t, err)
	require.Equal(t, aiGenerateMaxTokens, f.lastMax, "over-cap max_tokens is clamped")

	f2 := &fakeGenerator{text: "ok"}
	ex2 := NewAIGenerateExecutor(f2)
	_, err = ex2.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{"prompt": "hi"}}, EvalContext{})
	require.NoError(t, err)
	require.Equal(t, aiGenerateDefaultTokens, f2.lastMax, "absent max_tokens defaults")
}

func TestAIGenerate_MissingPrompt(t *testing.T) {
	ex := NewAIGenerateExecutor(&fakeGenerator{})
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{"prompt": "   "}}, EvalContext{})
	require.Error(t, err)
}

func TestAIGenerate_RetryableError(t *testing.T) {
	f := &fakeGenerator{err: errors.New("AI service unavailable"), retryable: true}
	ex := NewAIGenerateExecutor(f)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{"prompt": "hi"}}, EvalContext{})
	require.Error(t, err)
	require.True(t, isRetryable(err), "a transient AI outage should retry")
}

func TestAIGenerate_PermanentError(t *testing.T) {
	f := &fakeGenerator{err: errors.New("budget exceeded"), retryable: false}
	ex := NewAIGenerateExecutor(f)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{"prompt": "hi"}}, EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err), "budget-exhausted is permanent")
}

func TestAIGenerate_NoGeneratorConfigured(t *testing.T) {
	ex := NewAIGenerateExecutor(nil)
	_, err := ex.Execute(context.Background(), &WorkflowRun{ID: uuid.New()},
		ActionSpec{ID: "g1", Type: ActionAIGenerate, Params: map[string]any{"prompt": "hi"}}, EvalContext{})
	require.Error(t, err)
	require.False(t, isRetryable(err))
}
