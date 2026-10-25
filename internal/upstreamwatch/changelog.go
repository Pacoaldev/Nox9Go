package upstreamwatch

import (
	"context"
	json "encoding/json/v2"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"nox9go/proxy/internal/updater"
)

// maxChangelogBytes caps one changelog body. A fork release body runs past
// 100 KB, so the modal is handed a bounded document rather than the whole
// release: enough to read what changed, not enough to stall a small box.
const maxChangelogBytes = 256 << 10

// versionPattern is the only shape a version may have. It is interpolated into a
// URL below, so the guard is what keeps a caller from walking the path.
var versionPattern = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// Sentinel errors, so callers classify with errors.Is instead of matching on a
// message that a later edit would silently break (AGENTS.md §4.E).
var (
	// ErrUnknownSource is returned for a source id this build does not track.
	ErrUnknownSource = errors.New("unknown source")
	// ErrInvalidVersion is returned for a version that is not tag-shaped.
	ErrInvalidVersion = errors.New("invalid version")
	// ErrVersionNotPublished is returned when the source has no such version.
	ErrVersionNotPublished = errors.New("that version is not published")
)

// Changelog returns the notes for one published version of one source.
//
// What counts as "the changelog" differs per source, and that is a property of
// the repositories, not a preference: decolua/9router publishes no release, so
// its notes are the tagged commit's own message; luqman-v1/9router-go publishes
// releases, so its notes are the release body.
func (c *Checker) Changelog(ctx context.Context, sourceID, version string) (string, error) {
	version = strings.TrimSpace(version)
	if !versionPattern.MatchString(version) {
		return "", fmt.Errorf("%w: %q", ErrInvalidVersion, version)
	}

	key := sourceID + "@" + version
	if cached, ok := c.cachedChangelog(key); ok {
		return cached, nil
	}

	var src Source
	found := false
	for _, candidate := range Sources() {
		if candidate.ID == sourceID {
			src, found = candidate, true
			break
		}
	}
	if !found {
		return "", fmt.Errorf("%w: %q", ErrUnknownSource, sourceID)
	}

	notes, err := c.fetchChangelog(ctx, src, version)
	if err != nil {
		return "", err
	}
	c.rememberChangelog(key, notes)
	return notes, nil
}

func (c *Checker) fetchChangelog(ctx context.Context, src Source, version string) (string, error) {
	switch src.Kind {
	case kindTags:
		return c.commitMessage(ctx, src.Repo, version)
	case kindReleases:
		return c.releaseBody(ctx, src.Repo, version)
	default:
		return "", fmt.Errorf("unknown source kind %q", src.Kind)
	}
}

// commitMessage reads the message of the commit a tag points at. GitHub resolves
// a tag name in the commits path, so no separate tag-to-sha lookup is needed.
func (c *Checker) commitMessage(ctx context.Context, repo, ref string) (string, error) {
	body, err := c.getBounded(ctx, fmt.Sprintf("%s/repos/%s/commits/%s", APIBase, repo, url.PathEscape(ref)), maxChangelogBytes)
	if err != nil {
		return "", err
	}
	var commit struct {
		Commit struct {
			Message string `json:"message"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(body, &commit); err != nil {
		return "", fmt.Errorf("decode commit: %w", err)
	}
	message := strings.TrimSpace(commit.Commit.Message)
	if message == "" {
		return "", fmt.Errorf("commit %s carries no message", ref)
	}
	return message, nil
}

// releaseBody reads one release's body by its tag.
func (c *Checker) releaseBody(ctx context.Context, repo, tag string) (string, error) {
	body, err := c.getBounded(ctx, fmt.Sprintf("%s/repos/%s/releases/tags/%s", APIBase, repo, url.PathEscape(tag)), maxChangelogBytes)
	if err != nil {
		return "", err
	}
	var release struct {
		Body string `json:"body"`
	}
	if err := json.Unmarshal(body, &release); err != nil {
		return "", fmt.Errorf("decode release: %w", err)
	}
	notes := strings.TrimSpace(release.Body)
	if notes == "" {
		return "", fmt.Errorf("release %s carries no notes", tag)
	}
	return notes, nil
}

// getBounded is get with an explicit body cap, for the larger changelog reads.
// A missing version is reported as such: the operator asked for a specific one,
// and "not published" is a different answer from "GitHub is unhappy".
func (c *Checker) getBounded(ctx context.Context, target string, limit int64) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "nox9go/"+updater.CurrentVersion)

	resp, err := c.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	switch {
	case resp.StatusCode == http.StatusNotFound:
		return nil, ErrVersionNotPublished
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("github answered %d", resp.StatusCode)
	}
	return io.ReadAll(io.LimitReader(resp.Body, limit))
}

func (c *Checker) cachedChangelog(key string) (string, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	entry, ok := c.changelogs[key]
	if !ok || c.now().Sub(entry.at) >= changelogTTL {
		return "", false
	}
	return entry.markdown, true
}

// rememberChangelog stores one body, evicting the oldest entry once the map is
// full: the key carries a caller-supplied version, so the map must not grow with
// the number of distinct values asked for.
func (c *Checker) rememberChangelog(key, markdown string) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.changelogs) >= maxChangelogKeys {
		oldestKey, oldestAt := "", time.Time{}
		for k, entry := range c.changelogs {
			if oldestAt.IsZero() || entry.at.Before(oldestAt) {
				oldestKey, oldestAt = k, entry.at
			}
		}
		delete(c.changelogs, oldestKey)
	}
	c.changelogs[key] = changelogEntry{markdown: markdown, at: c.now()}
}
