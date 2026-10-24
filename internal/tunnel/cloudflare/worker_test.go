package cloudflare

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// withTempDataDir points the data dir at a temp folder so the persisted
// worker state does not touch the developer's real installation.
func withTempDataDir(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)
	return dir
}

func TestGenerateShortID(t *testing.T) {
	const allowed = "abcdefghijklmnpqrstuvwxyz23456789"

	seen := map[string]bool{}
	for i := 0; i < 50; i++ {
		id := generateShortID()
		if len(id) != shortIDLength {
			t.Fatalf("generateShortID() length = %d, want %d", len(id), shortIDLength)
		}
		for _, c := range id {
			if !strings.ContainsRune(allowed, c) {
				t.Fatalf("generateShortID() = %q contains %q outside the allowed alphabet", id, c)
			}
		}
		seen[id] = true
	}
	// Random ids must not collide across 50 draws — a collision would hand two
	// tunnels the same public hostname.
	if len(seen) < 45 {
		t.Errorf("generateShortID() produced %d unique ids out of 50", len(seen))
	}
}

func TestPublicURLFor(t *testing.T) {
	orig := WorkerURL
	WorkerURL = "https://abc-tunnel.us"
	t.Cleanup(func() { WorkerURL = orig })

	if got := PublicURLFor("abc123"); got != "https://rabc123.abc-tunnel.us" {
		t.Errorf("PublicURLFor() = %q, want %q", got, "https://rabc123.abc-tunnel.us")
	}
	if got := PublicURLFor(""); got != "" {
		t.Errorf("PublicURLFor(\"\") = %q, want empty", got)
	}
}

func TestRegisterShortURL(t *testing.T) {
	withTempDataDir(t)

	var gotPath, gotBody string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath = r.URL.Path
		buf := make([]byte, r.ContentLength)
		_, _ = r.Body.Read(buf)
		gotBody = string(buf)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"success"}`))
	}))
	defer srv.Close()

	orig := WorkerURL
	WorkerURL = srv.URL
	t.Cleanup(func() { WorkerURL = orig })

	public, err := RegisterShortURL(context.Background(), "abc123", "https://demo.trycloudflare.com")
	if err != nil {
		t.Fatalf("RegisterShortURL() error = %v", err)
	}
	if gotPath != "/api/tunnel/register" {
		t.Errorf("register path = %q, want /api/tunnel/register", gotPath)
	}
	var payload map[string]string
	if err := json.Unmarshal([]byte(gotBody), &payload); err != nil {
		t.Fatalf("register body is not JSON: %v", err)
	}
	if payload["shortId"] != "abc123" {
		t.Errorf("shortId = %q, want abc123", payload["shortId"])
	}
	if payload["tunnelUrl"] != "https://demo.trycloudflare.com" {
		t.Errorf("tunnelUrl = %q, want the trycloudflare URL", payload["tunnelUrl"])
	}
	// The public URL must be derived from the worker host, not the tunnel URL:
	// the short id is inserted as a subdomain of the worker.
	workerHost := strings.TrimPrefix(srv.URL, "http://")
	if !strings.HasPrefix(public, "http://rabc123.") {
		t.Errorf("public URL %q does not start with the short-id subdomain", public)
	}
	if !strings.HasSuffix(public, workerHost) {
		t.Errorf("public URL %q does not end with the worker host %q", public, workerHost)
	}
}

func TestRegisterShortURL_WorkerDown(t *testing.T) {
	withTempDataDir(t)

	orig := WorkerURL
	WorkerURL = "http://127.0.0.1:1" // nothing listens here
	t.Cleanup(func() { WorkerURL = orig })

	if _, err := RegisterShortURL(context.Background(), "abc123", "https://demo.trycloudflare.com"); err == nil {
		t.Fatal("RegisterShortURL() expected an error when the worker is unreachable")
	}
}

func TestStateRoundTrip(t *testing.T) {
	dir := withTempDataDir(t)

	if _, ok := loadState(); ok {
		t.Fatal("loadState() reported a state on a fresh data dir")
	}

	if err := saveState(tunnelState{ShortID: "abc123", TunnelURL: "https://demo.trycloudflare.com"}); err != nil {
		t.Fatalf("saveState() error = %v", err)
	}
	st, ok := loadState()
	if !ok {
		t.Fatal("loadState() reported no state after a save")
	}
	if st.ShortID != "abc123" {
		t.Errorf("ShortID = %q, want abc123", st.ShortID)
	}

	path := filepath.Join(dir, "tunnel", "state.json")
	if _, err := os.Stat(path); err != nil {
		t.Errorf("state file not written to %s: %v", path, err)
	}

	clearState()
	if _, ok := loadState(); ok {
		t.Error("loadState() still reported a state after clearState()")
	}
}

func TestLoadState_CorruptFile(t *testing.T) {
	dir := withTempDataDir(t)

	tunnelDir := filepath.Join(dir, "tunnel")
	if err := os.MkdirAll(tunnelDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(tunnelDir, "state.json"), []byte("{not json"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	if _, ok := loadState(); ok {
		t.Error("loadState() accepted a corrupt state file")
	}
}
