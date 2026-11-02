package tokensaver

import (
	"testing"
)

func TestNewRTKFilters_Oxlint(t *testing.T) {
	cfg := DefaultRTKConfig()
	input := "  × eslint(no-console): Unexpected console statement.\n   ╭─[src/app.ts:5:3]\n 5 │   console.log(\"debug\");\n   ╰────\nFinished in 12ms on 100 files.\n"
	res := CompressTextDetailedWithCommand(input, "oxlint src/", cfg)
	if res.DetectedCategory != "build" {
		t.Errorf("expected category 'build', got %q", res.DetectedCategory)
	}
	if !contains(res.Text, "eslint(no-console)") {
		t.Errorf("expected output to contain lint error, got %q", res.Text)
	}
	if contains(res.Text, "Finished in 12ms") {
		t.Errorf("expected 'Finished in 12ms' to be stripped, got %q", res.Text)
	}
}

func TestNewRTKFilters_ShellCheck(t *testing.T) {
	cfg := DefaultRTKConfig()
	input := "In script.sh line 3:\nif [[ $1 == \"\" ]]\n     ^-- SC2236: Use -z instead of ! -n.\n"
	res := CompressTextDetailedWithCommand(input, "shellcheck script.sh", cfg)
	if res.DetectedCategory != "build" {
		t.Errorf("expected category 'build', got %q", res.DetectedCategory)
	}
	if !contains(res.Text, "SC2236") {
		t.Errorf("expected output to contain SC2236, got %q", res.Text)
	}
}

func TestNewRTKFilters_BrewInstall(t *testing.T) {
	cfg := DefaultRTKConfig()
	input := "Warning: rtk 0.27.1 is already installed and up-to-date.\nTo reinstall 0.27.1, run:\n  brew reinstall rtk\n"
	res := CompressTextDetailedWithCommand(input, "brew install rtk", cfg)
	if res.DetectedCategory != "package" {
		t.Errorf("expected category 'package', got %q", res.DetectedCategory)
	}
	if !contains(res.Text, "brew: ok (already installed)") {
		t.Errorf("expected short-circuit message, got %q", res.Text)
	}
}

func contains(s, substr string) bool {
	return len(s) > 0 && len(substr) > 0 && (s == substr || len(s) >= len(substr) && (s[:len(substr)] == substr || containsAnywhere(s, substr)))
}

func containsAnywhere(s, substr string) bool {
	for i := 0; i+len(substr) <= len(s); i++ {
		if s[i:i+len(substr)] == substr {
			return true
		}
	}
	return false
}

