package tray

import (
	"testing"

	"nox9go/proxy/internal/service"
)

func TestResolveStop_CLIFallbackOnNoSocketOwner(t *testing.T) {
	path, refusal := resolveStop(service.Info{}, false, false, true)
	if path != StopVerified {
		t.Fatalf("path = %v, want StopVerified", path)
	}
	if refusal != RefuseNone {
		t.Fatalf("refusal = %q, want empty", refusal)
	}
}

func TestResolveStop_CLIFallbackOnWindowsTask(t *testing.T) {
	path, refusal := resolveStop(service.Info{Backend: service.BackendWindowsTask}, true, false, true)
	if path != StopVerified {
		t.Fatalf("path = %v, want StopVerified", path)
	}
	if refusal != RefuseNone {
		t.Fatalf("refusal = %q, want empty", refusal)
	}
}

func TestResolveStop_NoFallbackKeepsRefuse(t *testing.T) {
	path, refusal := resolveStop(service.Info{}, false, false, false)
	if path != StopRefused {
		t.Fatalf("path = %v, want StopRefused", path)
	}
	if refusal != RefuseNoSocketOwner {
		t.Fatalf("refusal = %q, want RefuseNoSocketOwner", refusal)
	}
}

func TestResolveStop_SupervisedStillPreferred(t *testing.T) {
	path, refusal := resolveStop(service.Info{Backend: service.BackendSystemdUser}, true, false, true)
	if path != StopSupervised {
		t.Fatalf("path = %v, want StopSupervised", path)
	}
	if refusal != RefuseNone {
		t.Fatalf("refusal = %q, want empty", refusal)
	}
}
