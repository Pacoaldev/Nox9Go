package proxy

import (
	json "encoding/json/v2"
	"testing"
)

func TestCleanKiroBody_ToolResultsPlaceholder(t *testing.T) {
	// Case 1: user turn with only tool results -> "Tool results provided."
	bodyWithTools := []byte(`{
		"userInputMessage": {
			"content": "",
			"userInputMessageContext": {
				"toolResults": [
					{"toolUseId": "call_1", "status": "success"}
				]
			}
		}
	}`)
	cleaned := cleanKiroBody(bodyWithTools)
	var parsed map[string]any
	if err := json.Unmarshal(cleaned, &parsed); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	uim := parsed["userInputMessage"].(map[string]any)
	if uim["content"] != KiroToolResultsPlaceholder {
		t.Errorf("expected %q, got %v", KiroToolResultsPlaceholder, uim["content"])
	}

	// Case 2: genuinely empty user turn -> "continue"
	emptyBody := []byte(`{
		"userInputMessage": {
			"content": "   "
		}
	}`)
	cleanedEmpty := cleanKiroBody(emptyBody)
	var parsedEmpty map[string]any
	_ = json.Unmarshal(cleanedEmpty, &parsedEmpty)
	uimEmpty := parsedEmpty["userInputMessage"].(map[string]any)
	if uimEmpty["content"] != KiroEmptyUserPlaceholder {
		t.Errorf("expected %q, got %v", KiroEmptyUserPlaceholder, uimEmpty["content"])
	}

	// Case 3: user turn with real content -> preserved
	realBody := []byte(`{
		"userInputMessage": {
			"content": "Hello Kiro!"
		}
	}`)
	cleanedReal := cleanKiroBody(realBody)
	var parsedReal map[string]any
	_ = json.Unmarshal(cleanedReal, &parsedReal)
	uimReal := parsedReal["userInputMessage"].(map[string]any)
	if uimReal["content"] != "Hello Kiro!" {
		t.Errorf("expected 'Hello Kiro!', got %v", uimReal["content"])
	}
}
