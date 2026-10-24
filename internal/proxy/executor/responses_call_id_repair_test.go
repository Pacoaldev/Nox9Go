package executor

import (
	json "encoding/json/v2"
	"testing"
)

func TestBuildResponsesBody_RepairMissingCallID(t *testing.T) {
	// Case 1: Responses input[] where function_call_output lost call_id (PR #4090)
	rawInput := []byte(`{
		"model": "muse-spark-1.3-contributor-free",
		"input": [
			{"type": "message", "role": "user", "content": [{"type": "input_text", "text": "run command"}]},
			{"type": "function_call", "call_id": "call_abc123", "name": "shell", "arguments": "{}"},
			{"type": "function_call_output", "output": "command success"}
		]
	}`)

	transformed, _, err := buildResponsesBody(rawInput)
	if err != nil {
		t.Fatalf("buildResponsesBody failed: %v", err)
	}

	var parsed map[string]any
	if err := json.Unmarshal(transformed, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	inputList := parsed["input"].([]any)
	outputItem := inputList[2].(map[string]any)
	if outputItem["call_id"] != "call_abc123" {
		t.Errorf("expected function_call_output to be repaired with 'call_abc123', got %v", outputItem["call_id"])
	}

	// Case 2: OpenAI messages format where tool role message lost tool_call_id
	rawMessages := []byte(`{
		"model": "muse-spark-1.3-contributor-free",
		"messages": [
			{"role": "user", "content": "run command"},
			{"role": "assistant", "tool_calls": [{"id": "call_xyz789", "type": "function", "function": {"name": "shell", "arguments": "{}"}}]},
			{"role": "tool", "content": "command success"}
		]
	}`)

	transformedMsgs, _, err := buildResponsesBody(rawMessages)
	if err != nil {
		t.Fatalf("buildResponsesBody messages failed: %v", err)
	}

	var parsedMsgs map[string]any
	_ = json.Unmarshal(transformedMsgs, &parsedMsgs)
	inMsgs := parsedMsgs["input"].([]any)
	// Last item should be function_call_output with call_id: call_xyz789
	lastItem := inMsgs[len(inMsgs)-1].(map[string]any)
	if lastItem["type"] != "function_call_output" {
		t.Fatalf("expected last item to be function_call_output, got %v", lastItem["type"])
	}
	if lastItem["call_id"] != "call_xyz789" {
		t.Errorf("expected call_id 'call_xyz789', got %v", lastItem["call_id"])
	}
}
