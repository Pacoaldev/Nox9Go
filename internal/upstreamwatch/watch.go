// Package upstreamwatch reports whether the repositories this fork tracks have
// published a version newer than the one this build was ported from.
//
// Two sources, deliberately distinct: `decolua/9router` is the behavioural
// specification this fork follows, and `luqman-v1/9router-go` is a sibling that
// ports the same features. They version independently — 0.5.x and 1.9.x — so a
// shared baseline would be meaningless; each carries its own.
//
// The baseline is a build-time constant, not persisted state:
//
//	go build -ldflags "-X nox9go/proxy/internal/upstreamwatch.BaselineUpstreamVersion=0.5.109 \
//	                   -X nox9go/proxy/internal/upstreamwatch.BaselineForkVersion=1.9.14"
//
// Bumping it is a deliberate act at each port, so a fresh install reports
// "nothing new" rather than announcing every version that predates it.
package upstreamwatch

import (
	"context"
	json "encoding/json/v2"
	"fmt"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"nox9go/proxy/internal/updater"
)

const (
	// GitHub answers an unauthenticated client 60 requests an hour per IP. Two
	// sources refreshed on this cache spend about four, which leaves the budget
	// to the changelog reads the operator actually asks for.
	snapshotTTL = 30 * time.Minute
	// A changelog read is one call per press, so its result is worth keeping —
	// but bounded, because the key carries a version string and an unbounded map
	// keyed by user input is a leak with a schedule (AGENTS.md §4.G).
	changelogTTL     = 30 * time.Minute
	maxChangelogKeys = 8

	requestTimeout = 10 * time.Second
	maxBodyBytes   = 1 << 20
)

// APIBase is the GitHub API root. A var so a test can point it at a local server.
var APIBase = "https://api.github.com"

// Baselines are the versions this binary was ported from. Set at build time with
// -ldflags; empty means "unknown", which reports no update rather than guessing.
//
// These are package-level vars on purpose: a build that sets them with ldflags
// and a test that sets them in-process both see their own value, and a build
// that leaves them unset reports "nothing new" rather than every version ever
// published.
var (
	BaselineUpstreamVersion = ""
	BaselineForkVersion     = ""
)

type kind string

const (
	kindTags     kind = "tags"
	kindReleases kind = "releases"
)

// Source is one tracked repository.
type Source struct {
	ID   string
	Name string
	Repo string
	Kind kind
	// Baseline is the version this build was ported from for this source.
	Baseline string
}

// Sources returns the tracked repositories. The baselines are read on every call
// so a build that sets them with -ldflags and a test that sets them in-process
// both see their own value.
func Sources() []Source {
	return []Source{
		{
			ID:       "upstream",
			Name:     "9Router (official)",
			Repo:     "decolua/9router",
			Kind:     kindTags,
			Baseline: BaselineUpstreamVersion,
		},
		{
			ID:       "fork",
			Name:     "9router-go (fork)",
			Repo:     "luqman-v1/9router-go",
			Kind:     kindReleases,
			Baseline: BaselineForkVersion,
		},
	}
}

// SourceStatus is one source's answer for the dashboard.
type SourceStatus struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Repo        string `json:"repo"`
	Baseline    string `json:"baseline"`
	Latest      string `json:"latest"`
	HasNew      bool   `json:"hasNew"`
	PublishedAt string `json:"publishedAt,omitempty"`
	ReleaseURL  string `json:"releaseUrl,omitempty"`
	// Error is set when this source could not be read. The other source's answer
	// still stands: one unreachable repository must not hide the other.
	Error string `json:"error,omitempty"`
}

// Snapshot is the whole answer.
type Snapshot struct {
	Sources   []SourceStatus `json:"sources"`
	CheckedAt string         `json:"checkedAt"`
}

// HasNew reports whether any source has a version past this build's baseline.
func (s Snapshot) HasNew() bool {
	for _, src := range s.Sources {
		if src.HasNew {
			return true
		}
	}
	return false
}

// versionEntry is one published version of a source.
type versionEntry struct {
	version     string
	publishedAt string
	releaseURL  string
}

// Checker reads the sources and keeps their answers for snapshotTTL. It is safe
// for concurrent use: the dashboard polls it from every open tab.
type Checker struct {
	client *http.Client
	now    func() time.Time

	mu         sync.Mutex
	cached     Snapshot
	checkedAt  time.Time
	changelogs map[string]changelogEntry
}

type changelogEntry struct {
	markdown string
	at       time.Time
}

// NewChecker returns a Checker using client, or a private pooled client when
// client is nil.
func NewChecker(client *http.Client) *Checker {
	if client == nil {
		client = &http.Client{Timeout: requestTimeout}
	}
	return &Checker{
		client:     client,
		now:        time.Now,
		changelogs: map[string]changelogEntry{},
	}
}

// Check returns the current state of every source, from cache while it is fresh.
// A source that cannot be read carries its error in SourceStatus.Error; the call
// itself only fails when nothing could be reported at all.
func (c *Checker) Check(ctx context.Context) Snapshot {
	c.mu.Lock()
	if len(c.cached.Sources) > 0 && c.now().Sub(c.checkedAt) < snapshotTTL {
		cached := c.cached
		c.mu.Unlock()
		return cached
	}
	c.mu.Unlock()

	snapshot := Snapshot{CheckedAt: c.now().UTC().Format(time.RFC3339)}
	for _, src := range Sources() {
		snapshot.Sources = append(snapshot.Sources, c.checkSource(ctx, src))
	}

	c.mu.Lock()
	c.cached = snapshot
	c.checkedAt = c.now()
	c.mu.Unlock()
	return snapshot
}

func (c *Checker) checkSource(ctx context.Context, src Source) SourceStatus {
	status := SourceStatus{
		ID:       src.ID,
		Name:     src.Name,
		Repo:     src.Repo,
		Baseline: src.Baseline,
	}
	entry, err := c.latest(ctx, src)
	if err != nil {
		status.Error = err.Error()
		return status
	}
	status.Latest = entry.version
	status.PublishedAt = entry.publishedAt
	status.ReleaseURL = entry.releaseURL
	status.HasNew = isNewer(entry.version, src.Baseline)
	return status
}

// latest reads the newest published version of a source.
func (c *Checker) latest(ctx context.Context, src Source) (versionEntry, error) {
	switch src.Kind {
	case kindTags:
		return c.latestTag(ctx, src.Repo)
	case kindReleases:
		return c.latestRelease(ctx, src.Repo)
	default:
		return versionEntry{}, fmt.Errorf("unknown source kind %q", src.Kind)
	}
}

// latestTag reads the tag list. The API does not order tags by version — a page
// came back 0.5.109, 0.5.104, 0.5.95 — so the maximum is taken here by semver,
// never from page order.
func (c *Checker) latestTag(ctx context.Context, repo string) (versionEntry, error) {
	body, err := c.get(ctx, fmt.Sprintf("%s/repos/%s/tags?per_page=100", APIBase, repo))
	if err != nil {
		return versionEntry{}, err
	}
	var tags []struct {
		Name string `json:"name"`
	}
	if err := json.Unmarshal(body, &tags); err != nil {
		return versionEntry{}, fmt.Errorf("decode tags: %w", err)
	}
	names := make([]string, 0, len(tags))
	for _, tag := range tags {
		if name := strings.TrimSpace(tag.Name); name != "" {
			names = append(names, name)
		}
	}
	best := latestOf(names)
	if best == "" {
		return versionEntry{}, fmt.Errorf("no tags published")
	}
	return versionEntry{
		version:    best,
		releaseURL: fmt.Sprintf("https://github.com/%s/tree/%s", repo, best),
	}, nil
}

// latestRelease reads the newest released version.
//
// It asks for `/releases/latest` rather than paging the release list, for two
// reasons that both showed up in practice: GitHub already excludes prereleases
// and drafts from that endpoint, which is exactly the selection rule; and the
// list endpoint is unusable here, because this fork's release bodies run past
// 100 KB each, so thirty of them overflow any sane read cap and the JSON arrives
// truncated.
func (c *Checker) latestRelease(ctx context.Context, repo string) (versionEntry, error) {
	body, err := c.get(ctx, fmt.Sprintf("%s/repos/%s/releases/latest", APIBase, repo))
	if err != nil {
		return versionEntry{}, err
	}
	var release struct {
		TagName     string `json:"tag_name"`
		Prerelease  bool   `json:"prerelease"`
		Draft       bool   `json:"draft"`
		PublishedAt string `json:"published_at"`
		HTMLURL     string `json:"html_url"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return versionEntry{}, fmt.Errorf("decode release: %w", err)
	}
	tag := strings.TrimSpace(release.TagName)
	// Defensive: the endpoint documents that it skips both, so seeing one here
	// means the answer is not what this code was written against.
	if tag == "" || release.Prerelease || release.Draft {
		return versionEntry{}, fmt.Errorf("no released version published")
	}
	return versionEntry{
		version:     tag,
		publishedAt: release.PublishedAt,
		releaseURL:  release.HTMLURL,
	}, nil
}

// get performs one bounded GET with the gateway's own User-Agent. GitHub answers
// a request without one a 403, which would surface as an opaque transport error.
func (c *Checker) get(ctx context.Context, target string) ([]byte, error) {
	return c.getBounded(ctx, target, maxBodyBytes)
}

// latestOf returns the highest version in versions by semver precedence.
func latestOf(versions []string) string {
	best := ""
	for _, v := range versions {
		if best == "" || updater.CompareVersions(v, best) > 0 {
			best = v
		}
	}
	return best
}

// isNewer reports whether latest is past baseline. An unknown baseline — a build
// that set no -ldflags — reports nothing rather than every version ever tagged.
func isNewer(latest, baseline string) bool {
	if latest == "" || baseline == "" {
		return false
	}
	return updater.CompareVersions(latest, baseline) > 0
}

// sortedVersionsDesc is used by the tests and by the changelog guard, which needs
// to know a requested version is one of the source's own.
func sortedVersionsDesc(versions []string) []string {
	out := append([]string(nil), versions...)
	sort.SliceStable(out, func(i, j int) bool {
		return updater.CompareVersions(out[i], out[j]) > 0
	})
	return out
}
