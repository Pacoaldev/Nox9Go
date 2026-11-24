package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestSubmitGentleDesktopAnswerOption(t *testing.T) {
	root := t.TempDir()
	sessionID := "01a20aa0-test"
	target := GentleDesktopAnswerTarget{
		AgentDir:   root,
		SessionID:  sessionID,
		ToolCallID: "call_test",
	}
	if err := submitGentleDesktopAnswerOption(target, 1); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(root, "desktop", "inbox", sessionID)
	entries, err := os.ReadDir(dir)
	if err != nil || len(entries) != 1 {
		t.Fatalf("inbox entries = %v, err = %v", entries, err)
	}
	raw, err := os.ReadFile(filepath.Join(dir, entries[0].Name()))
	if err != nil {
		t.Fatal(err)
	}
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	if doc["schema"] != desktopActionSchema {
		t.Errorf("schema = %v", doc["schema"])
	}
	args, _ := doc["args"].(map[string]any)
	if args["toolCallId"] != "call_test" || args["via"] != "session" {
		t.Fatalf("args = %v", args)
	}
	if opt, _ := args["option"].(float64); int(opt) != 1 {
		t.Errorf("option = %v", args["option"])
	}
}