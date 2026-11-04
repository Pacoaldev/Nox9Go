package detection

import (
	"testing"
)

func TestDetectLoops_Empty(t *testing.T) {
	findings := DetectLoops(nil, "")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for empty tool calls, got %d", len(findings))
	}
}

func TestDetectLoops_ConsecutiveTool(t *testing.T) {
	tools := []ToolCallItem{
		{Name: "bash", Arguments: `{"cmd":"ls"}`},
		{Name: "bash", Arguments: `{"cmd":"ls"}`},
		{Name: "bash", Arguments: `{"cmd":"ls"}`},
	}
	findings := DetectLoops(tools, "")
	if len(findings) == 0 {
		t.Fatal("expected findings for consecutive identical tool calls")
	}
	if findings[0].Rule != "consecutive_tool" || findings[0].Severity != SeverityCritical {
		t.Errorf("unexpected finding: %+v", findings[0])
	}
}

func TestDetectLoops_SameToolDifferentArgsNeverFlagsEvenAt15(t *testing.T) {
	commands := []string{"grep socketFile", "grep ResolveDataDir", "grep capabilityLen", "grep mcode-auth-lease-v1.sock", "grep capabilityFile", "grep isBase64URL", "grep claudeIdentityRegex", "git diff media.go", "git diff live_catalog.go", "grep fallback", "grep ResolveEndpoint", "grep socketFile runDir", "grep capabilityLen cap", "grep truncateRuneSafe", "git diff usage.go"}
	tools := make([]ToolCallItem, 0, len(commands))
	for _, cmd := range commands {
		tools = append(tools, ToolCallItem{Name: "terminal", Arguments: `{"command":"` + cmd + `"}`})
	}
	findings := DetectLoops(tools, "")
	if len(findings) != 0 {
		t.Fatalf("expected 0 findings for 15 terminal calls with different arguments, got %+v", findings)
	}
}

func TestDetectLoops_SameArgsStillFlagsAtAnyLength(t *testing.T) {
	for _, n := range []int{3, 5, 15} {
		tools := make([]ToolCallItem, 0, n)
		for range n {
			tools = append(tools, ToolCallItem{Name: "terminal", Arguments: `{"command":"pytest"}`})
		}
		findings := DetectLoops(tools, "")
		if len(findings) == 0 {
			t.Fatalf("expected stuck-loop finding for %d identical calls, got none", n)
		}
		if findings[0].Rule != "consecutive_tool" || findings[0].Severity != SeverityCritical {
			t.Errorf("expected critical consecutive_tool for %d identical calls, got %+v", n, findings[0])
		}
	}
}

func TestDetectLoops_CyclePattern(t *testing.T) {
	tools := []ToolCallItem{
		{Name: "read_file", Arguments: `{"p":"a"}`},
		{Name: "edit_file", Arguments: `{"p":"a"}`},
		{Name: "read_file", Arguments: `{"p":"b"}`},
		{Name: "edit_file", Arguments: `{"p":"b"}`},
		{Name: "read_file", Arguments: `{"p":"c"}`},
		{Name: "edit_file", Arguments: `{"p":"c"}`},
	}
	findings := DetectLoops(tools, "")
	if len(findings) == 0 {
		t.Fatal("expected findings for cycle pattern")
	}
	if findings[0].Rule != "cycle_pattern" {
		t.Errorf("unexpected rule: %s", findings[0].Rule)
	}
}

func TestDetectLoops_RepeatedError(t *testing.T) {
	tools := []ToolCallItem{
		{Name: "bash", Arguments: `{"cmd":"x"}`, Error: "syntaxerror: unexpected token"},
		{Name: "bash", Arguments: `{"cmd":"y"}`, Error: "syntaxerror: unexpected token"},
		{Name: "bash", Arguments: `{"cmd":"z"}`, Error: "syntaxerror: unexpected token"},
	}
	findings := DetectLoops(tools, "")
	if len(findings) == 0 {
		t.Fatal("expected findings for repeated error")
	}
	if findings[0].Rule != "repeated_error" {
		t.Errorf("unexpected rule: %s", findings[0].Rule)
	}
}