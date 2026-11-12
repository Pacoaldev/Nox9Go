package proxy

// A turn that produced only reasoning is not an answer.
//
// Gemini aborts a generation with finishReason MALFORMED_FUNCTION_CALL (it
// attempted a native function call the API cannot serialize) and still answers
// HTTP 200 with reasoning frames and no content. Counting reasoning as content
// made the gateway settle on that model, serve a 200 that carried nothing, and
// skip the rest of the combo.

import (
	"strings"
	"testing"
)

func TestZeroContent_ReasoningOnlyIsNotAnAnswer(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "reasoning_content only",
			body: `{"choices":[{"message":{"content":"","reasoning_content":"let me think"},"finish_reason":"in_progress"}]}`,
		},
		{
			name: "reasoning only",
			body: `{"choices":[{"message":{"content":"","reasoning":"let me think"},"finish_reason":"in_progress"}]}`,
		},
		{
			name: "reasoning plus empty tool call list",
			body: `{"choices":[{"message":{"content":"","reasoning_content":"hmm","tool_calls":[]},"finish_reason":"in_progress"}]}`,
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if !IsZeroContentResponse([]byte(tt.body)) {
				t.Error("IsZeroContentResponse = false, want true: no text and no tool call")
			}
			if err := EmptyUpstreamError([]byte(tt.body)); err == nil {
				t.Error("EmptyUpstreamError = nil, want a failure so the combo falls through")
			}
		})
	}
}

func TestZeroContent_ReasoningWithAnswerStaysAnAnswer(t *testing.T) {
	bodies := []string{
		`{"choices":[{"message":{"content":"hello","reasoning_content":"step"},"finish_reason":"stop"}]}`,
		`{"choices":[{"message":{"content":"","reasoning_content":"step","tool_calls":[{"id":"c1","type":"function","function":{"name":"f"}}]},"finish_reason":"tool_calls"}]}`,
	}
	for _, body := range bodies {
		if IsZeroContentResponse([]byte(body)) {
			t.Errorf("IsZeroContentResponse = true for %s, want false: the turn carries an answer", body)
		}
		if err := EmptyUpstreamError([]byte(body)); err != nil {
			t.Errorf("EmptyUpstreamError = %v for %s, want nil", err, body)
		}
	}
}

// TestStreamPeek_ReasoningOnlyFrameDoesNotCommit pins that the pre-commit peek
// keeps reading past a reasoning-only frame instead of committing headers for a
// turn that never produced a client-usable answer.
func TestStreamPeek_ReasoningOnlyFrameDoesNotCommit(t *testing.T) {
	reasoningOnly := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"thinking\"}}]}\n\n"
	withContent := "data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"

	// Reasoning only: the peek must refuse before anything is written, so the
	// combo can move to the next model.
	if _, _, err := PeekFirstStreamEvent(nil, strings.NewReader(reasoningOnly+"data: [DONE]\n\n"), 0); err == nil {
		t.Error("peek = nil error for a reasoning-only stream, want a failure")
	}

	// Reasoning followed by content: the peek must accept, because the turn
	// does produce an answer.
	if _, _, err := PeekFirstStreamEvent(nil, strings.NewReader(reasoningOnly+withContent), 0); err != nil {
		t.Errorf("peek = %v for reasoning followed by content, want nil", err)
	}
}
