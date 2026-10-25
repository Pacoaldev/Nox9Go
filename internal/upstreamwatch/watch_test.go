package upstreamwatch

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeGitHub answers the paths the checker dials. A path outside the map is a
// 404, so a test that forgets a route fails loudly instead of decoding an empty
// body into a zero value.
func fakeGitHub(t *testing.T, routes map[string]string) {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		for prefix, body := range routes {
			if strings.HasPrefix(r.URL.Path, prefix) {
				w.Header().Set("Content-Type", "application/json")
				_, _ = w.Write([]byte(body))
				return
			}
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)
	previous := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = previous })
}

const (
	upstreamTagsPath = "/repos/decolua/9router/tags"
	forkTagsPath     = "/repos/luqman-v1/9router-go/tags"
	forkReleasePath  = "/repos/luqman-v1/9router-go/releases/latest"
)

func testChecker(t *testing.T) *Checker {
	t.Helper()
	checker := NewChecker(nil)
	fixed := time.Date(2026, 10, 25, 12, 0, 0, 0, time.UTC)
	checker.now = func() time.Time { return fixed }
	return checker
}

func TestSortedVersionsDesc(t *testing.T) {
	got := sortedVersionsDesc([]string{"0.5.1", "0.5.10", "0.5.2", "1.0.0-rc", "1.0.0"})
	// A prerelease outranks every lower core: 1.0.0-rc is above 0.5.10.
	want := []string{"1.0.0", "1.0.0-rc", "0.5.10", "0.5.2", "0.5.1"}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("position %d: got %q want %q; full %v", i, got[i], want[i], got)
		}
	}
}

func TestLatestOf(t *testing.T) {
	// The API hands these back in a non-version order, which is the whole reason
	// the maximum is computed here.
	if got := latestOf([]string{"v0.5.95", "v0.5.104", "v0.5.109"}); got != "v0.5.109" {
		t.Fatalf("got %q, want v0.5.109", got)
	}
	if got := latestOf(nil); got != "" {
		t.Fatalf("empty: got %q", got)
	}
}

func TestIsNewer(t *testing.T) {
	cases := []struct {
		name             string
		latest, baseline string
		want             bool
	}{
		{name: "newer tag", latest: "1.0.0", baseline: "0.9.0", want: true},
		{name: "equal", latest: "1.0.0", baseline: "1.0.0", want: false},
		{name: "older", latest: "0.9.0", baseline: "1.0.0", want: false},
		{name: "unknown baseline", latest: "1.0.0", baseline: "", want: false},
		{name: "no latest", latest: "", baseline: "1.0.0", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isNewer(c.latest, c.baseline); got != c.want {
				t.Errorf("isNewer(%q, %q) = %v, want %v", c.latest, c.baseline, got, c.want)
			}
		})
	}
}

func TestSnapshotHasNew(t *testing.T) {
	snap := Snapshot{Sources: []SourceStatus{{HasNew: false}, {HasNew: false}}}
	if snap.HasNew() {
		t.Error("no source is new")
	}
	snap.Sources[1].HasNew = true
	if !snap.HasNew() {
		t.Error("one source is new")
	}
}

func TestCheckUsesTagOrderIndependentlyOfPageOrder(t *testing.T) {
	fakeGitHub(t, map[string]string{
		upstreamTagsPath: `[{"name":"v0.5.95"},{"name":"v0.5.104"},{"name":"v0.5.109"}]`,
		forkReleasePath:  `{"tag_name":"v1.9.14","html_url":"https://example.test/v1.9.14"}`,
	})

	BaselineUpstreamVersion = "0.5.100"
	BaselineForkVersion = "1.9.0"
	t.Cleanup(func() {
		BaselineUpstreamVersion = ""
		BaselineForkVersion = ""
	})

	snap := testChecker(t).Check(context.Background())
	if len(snap.Sources) != 2 {
		t.Fatalf("expected 2 sources, got %d", len(snap.Sources))
	}
	if up := snap.Sources[0]; up.Latest != "v0.5.109" || !up.HasNew {
		t.Errorf("upstream = %+v, want latest v0.5.109 and HasNew", up)
	}
	if fork := snap.Sources[1]; fork.Latest != "v1.9.14" || !fork.HasNew {
		t.Errorf("fork = %+v, want latest v1.9.14 and HasNew", fork)
	}
}

func TestCheckEqualBaselineReportsNothing(t *testing.T) {
	fakeGitHub(t, map[string]string{
		upstreamTagsPath: `[{"name":"v0.5.109"}]`,
		forkReleasePath:  `{"tag_name":"v1.9.14"}`,
	})

	BaselineUpstreamVersion = "v0.5.109"
	BaselineForkVersion = "v1.9.14"
	t.Cleanup(func() {
		BaselineUpstreamVersion = ""
		BaselineForkVersion = ""
	})

	if snap := testChecker(t).Check(context.Background()); snap.HasNew() {
		t.Errorf("equal baselines must report nothing, got %+v", snap.Sources)
	}
}

func TestCheckUnknownBaselineReportsNothing(t *testing.T) {
	fakeGitHub(t, map[string]string{
		upstreamTagsPath: `[{"name":"v1.9.14"}]`,
		forkReleasePath:  `{"tag_name":"v1.9.14"}`,
	})

	snap := testChecker(t).Check(context.Background())
	if snap.HasNew() {
		t.Errorf("an unset baseline must report nothing, got %+v", snap.Sources)
	}
	for _, src := range snap.Sources {
		if src.Baseline != "" {
			t.Errorf("%s baseline = %q, want empty", src.ID, src.Baseline)
		}
	}
}

// The prerelease filter is GitHub's on this endpoint, so the guard here only has
// to refuse an answer that contradicts what the endpoint documents: a prerelease
// must never reach the operator as the version to read about.
func TestCheckRefusesAPrereleaseAnswer(t *testing.T) {
	fakeGitHub(t, map[string]string{
		upstreamTagsPath: `[{"name":"v0.5.109"}]`,
		forkReleasePath:  `{"tag_name":"v1.9.15-exp.9","prerelease":true,"published_at":"2026-10-25T00:00:00Z"}`,
	})

	BaselineForkVersion = "1.9.0"
	t.Cleanup(func() { BaselineForkVersion = "" })

	fork := testChecker(t).Check(context.Background()).Sources[1]
	if fork.Latest != "" || fork.HasNew {
		t.Errorf("a prerelease must not be reported as the latest version: %+v", fork)
	}
	if fork.Error == "" {
		t.Error("the refusal must be visible as an error on the source")
	}
}

func TestCheckReadsReleaseFields(t *testing.T) {
	fakeGitHub(t, map[string]string{
		upstreamTagsPath: `[{"name":"v0.5.109"}]`,
		forkReleasePath:  `{"tag_name":"v1.9.14","html_url":"https://example.test/v1.9.14","published_at":"2026-10-20T00:00:00Z"}`,
	})

	BaselineForkVersion = "1.9.0"
	t.Cleanup(func() { BaselineForkVersion = "" })

	fork := testChecker(t).Check(context.Background()).Sources[1]
	if fork.Latest != "v1.9.14" {
		t.Errorf("latest = %q, want v1.9.14", fork.Latest)
	}
	if fork.ReleaseURL != "https://example.test/v1.9.14" {
		t.Errorf("releaseUrl = %q", fork.ReleaseURL)
	}
	if fork.PublishedAt != "2026-10-20T00:00:00Z" {
		t.Errorf("publishedAt = %q", fork.PublishedAt)
	}
}

// One unreachable repository must not hide the other source's answer.
func TestCheckReportsPerSourceFailure(t *testing.T) {
	fakeGitHub(t, map[string]string{
		upstreamTagsPath: `[{"name":"v0.5.109"}]`,
	})

	BaselineUpstreamVersion = "0.5.100"
	t.Cleanup(func() { BaselineUpstreamVersion = "" })

	snap := testChecker(t).Check(context.Background())
	if !snap.HasNew() {
		t.Error("the readable source must still report its new version")
	}
	if snap.Sources[1].Error == "" {
		t.Error("the unreadable source must carry an error")
	}
}

func TestCheckServesCacheWithinTTL(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`[{"name":"v0.5.109"}]`))
	}))
	t.Cleanup(srv.Close)
	previous := APIBase
	APIBase = srv.URL
	t.Cleanup(func() { APIBase = previous })

	checker := testChecker(t)
	checker.Check(context.Background())
	afterFirst := calls
	checker.Check(context.Background())
	if calls != afterFirst {
		t.Errorf("a second check inside the TTL dialed GitHub again (%d -> %d)", afterFirst, calls)
	}

	// Moving the clock past the TTL is what should re-read.
	checker.now = func() time.Time { return time.Now().Add(snapshotTTL + time.Minute) }
	checker.Check(context.Background())
	if calls == afterFirst {
		t.Error("a check past the TTL must re-read the sources")
	}
}

func TestChangelogRejectsBadVersion(t *testing.T) {
	c := testChecker(t)
	for _, bad := range []string{"../bad", "", "v1.0.0/../x", "with space"} {
		if _, err := c.Changelog(context.Background(), "upstream", bad); err == nil {
			t.Errorf("version %q must be refused", bad)
		}
	}
}

func TestChangelogUnknownSource(t *testing.T) {
	c := testChecker(t)
	_, err := c.Changelog(context.Background(), "nope", "v1.0.0")
	if err == nil || !strings.Contains(err.Error(), "unknown source") {
		t.Fatalf("expected an unknown-source error, got %v", err)
	}
}

func TestChangelogReadsTaggedCommitForUpstream(t *testing.T) {
	fakeGitHub(t, map[string]string{
		"/repos/decolua/9router/commits/v0.5.109": `{"commit":{"message":"feat: add parity for embeddings"}}`,
	})

	notes, err := testChecker(t).Changelog(context.Background(), "upstream", "v0.5.109")
	if err != nil {
		t.Fatalf("changelog: %v", err)
	}
	if notes != "feat: add parity for embeddings" {
		t.Errorf("notes = %q", notes)
	}
}

func TestChangelogReadsReleaseBodyForFork(t *testing.T) {
	fakeGitHub(t, map[string]string{
		"/repos/luqman-v1/9router-go/releases/tags/v1.9.14": `{"body":"### Quota\n- lists every connection"}`,
	})

	notes, err := testChecker(t).Changelog(context.Background(), "fork", "v1.9.14")
	if err != nil {
		t.Fatalf("changelog: %v", err)
	}
	if !strings.Contains(notes, "lists every connection") {
		t.Errorf("notes = %q", notes)
	}
}

func TestChangelogCacheIsBounded(t *testing.T) {
	fakeGitHub(t, map[string]string{
		"/repos/decolua/9router/commits/": `{"commit":{"message":"m"}}`,
	})

	checker := testChecker(t)
	for _, version := range []string{"v1", "v2", "v3", "v4", "v5", "v6", "v7", "v8", "v9", "v10"} {
		if _, err := checker.Changelog(context.Background(), "upstream", version); err != nil {
			t.Fatalf("changelog %s: %v", version, err)
		}
	}
	if len(checker.changelogs) > maxChangelogKeys {
		t.Errorf("cache holds %d entries, want at most %d", len(checker.changelogs), maxChangelogKeys)
	}
}

func TestChangelogMissingVersionIsReportedAsSuch(t *testing.T) {
	fakeGitHub(t, map[string]string{})

	_, err := testChecker(t).Changelog(context.Background(), "upstream", "v9.9.9")
	if err == nil || !strings.Contains(err.Error(), "not published") {
		t.Fatalf("expected a not-published error, got %v", err)
	}
}

func TestSourcesCarryTheirOwnBaseline(t *testing.T) {
	BaselineUpstreamVersion = "0.5.109"
	BaselineForkVersion = "1.9.14"
	t.Cleanup(func() {
		BaselineUpstreamVersion = ""
		BaselineForkVersion = ""
	})

	sources := Sources()
	if sources[0].Baseline != "0.5.109" || sources[1].Baseline != "1.9.14" {
		t.Fatalf("baselines not carried: %+v", sources)
	}
	if sources[0].Repo == sources[1].Repo {
		t.Error("the two sources must be distinct repositories")
	}
}

// The tag list is a different shape from a release, and a source read as the
// wrong kind must fail loudly rather than report an empty version.
func TestForkTagsListIsNotMistakenForARelease(t *testing.T) {
	fakeGitHub(t, map[string]string{
		forkReleasePath: `[{"name":"v1.9.14"}]`,
	})

	checker := testChecker(t)
	fork := checker.Check(context.Background()).Sources[1]
	if fork.Latest != "" {
		t.Errorf("a body with no tag_name must yield no version, got %q", fork.Latest)
	}
	if fork.Error == "" {
		t.Error("expected an error for a release list with no usable entries")
	}
}
