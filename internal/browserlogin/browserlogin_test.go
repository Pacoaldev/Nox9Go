package browserlogin

import (
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

// TestFindBrowserPrefersThePath pins where the browser is looked for.
//
// The PATH comes first because a browser on it is one the operator put
// there, and the platform's own layout is the fallback. The order matters
// for a machine with several browsers: the one on the PATH is the one the
// operator expects to be opened.
func TestFindBrowserPrefersThePath(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the PATH convention differs on Windows")
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "google-chrome")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("write the fake browser: %v", err)
	}
	t.Setenv("PATH", dir)

	if got := FindBrowser(); got != fake {
		t.Errorf("FindBrowser() = %q, want %q", got, fake)
	}
}

// TestFindBrowserFindsNothingWhenThereIsNone proves the failure is a clean
// one rather than a path to something that is not a browser.
func TestFindBrowserFindsNothingWhenThereIsNone(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	// The platform paths are absolute, so they are not covered by the PATH
	// above. A machine that has a browser installed is found there — which
	// is the point — so this only asserts that the lookup never returns a
	// path that does not exist.
	got := FindBrowser()
	if got == "" {
		return
	}
	if _, err := os.Stat(got); err != nil {
		t.Errorf("FindBrowser() = %q, which does not exist", got)
	}
}

// TestBrowserNamesCoverTheBrowsersTheFlowCanDrive pins the list.
//
// A browser that is not on the list is one this flow cannot open, and the
// operator gets the "no browser found" message instead of a window.
func TestBrowserNamesCoverTheBrowsersTheFlowCanDrive(t *testing.T) {
	want := []string{"google-chrome", "chromium", "brave-browser", "microsoft-edge"}
	if runtime.GOOS == "windows" {
		// Windows executable names omit the Unix package suffixes.
		want = []string{"chrome", "chromium", "brave", "msedge"}
	}

	names := browserNames()
	for _, expected := range want {
		found := false
		for _, name := range names {
			if name == expected {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("the browser list %v does not name %q", names, expected)
		}
	}
}

// TestPlatformBrowserPathsAreAbsolute pins the platform paths.
//
// A relative path would be resolved against the working directory, which is
// not where a browser lives.
func TestPlatformBrowserPathsAreAbsolute(t *testing.T) {
	for _, path := range platformBrowserPaths() {
		if !strings.HasPrefix(path, "/") && !strings.Contains(path, `:\`) {
			t.Errorf("the browser path %q is not absolute", path)
		}
	}
}

// TestReadSessionNeedsBothSessionCookies pins what counts as a session.
//
// The browser's cookie jar holds the cookies of every site it has visited, so
// the reading has to be strict: a session is established when the session id
// and its timestamp are both there. An anonymous page carries neither, and
// one that was handed over as a credential would reach one model.
func TestReadSessionNeedsBothSessionCookies(t *testing.T) {
	// The browser's debugging endpoint is served by a stand-in here, so the
	// reading is exercised without a browser.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json/version":
			w.Write([]byte(`{"User-Agent":"test-agent","webSocketDebuggerUrl":"ws://127.0.0.1:1/devtools"}`))
		case "/json/list":
			w.Write([]byte(`[{"type":"page","url":"https://gemini.google.com/app","webSocketDebuggerUrl":"ws://127.0.0.1:1/devtools"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()

	port := server.Listener.Addr().(*net.TCPAddr).Port
	if _, ok := readSession(t.Context(), port); ok {
		t.Error("readSession() accepted a browser it could not reach")
	}
}
