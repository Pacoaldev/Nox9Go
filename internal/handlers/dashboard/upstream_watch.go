package dashboard

import (
	"errors"
	"net/http"
	"strings"

	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/upstreamwatch"
)

// Dashboard surface for the two repositories this fork tracks.
//
// The dashboard never calls GitHub itself (AGENTS.md §6.G.1): a third-party
// origin in the browser costs a DNS lookup, a TLS handshake and a round trip on
// a connection the operator did not choose, and it would also hand GitHub the
// operator's IP on every page load. These two routes are that indirection, and
// they ride the same dashboard auth as every other /api route.

// HandleGetUpstreamVersions handles GET /api/upstream-versions.
//
// It answers whether either tracked repository has published a version past the
// baseline this build was ported from. One snapshot serves every open tab: the
// checker keeps its answer for half an hour, which also keeps this well inside
// GitHub's 60-requests-an-hour ceiling for an unauthenticated client.
func (h *DashboardHandler) HandleGetUpstreamVersions(w http.ResponseWriter, r *http.Request) {
	snapshot := h.watch().Check(r.Context())
	handlerutil.WriteJSON(w, http.StatusOK, snapshot)
}

// HandleGetUpstreamChangelog handles GET /api/upstream-changelog?source=&version=.
//
// The notes are returned as markdown, because that is what the modal renders and
// what both sources publish. The checker bounds the body and refuses a version
// that is not tag-shaped, so this route cannot be used to walk a URL.
func (h *DashboardHandler) HandleGetUpstreamChangelog(w http.ResponseWriter, r *http.Request) {
	source := strings.TrimSpace(r.URL.Query().Get("source"))
	version := strings.TrimSpace(r.URL.Query().Get("version"))
	if source == "" || version == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing source or version")
		return
	}

	notes, err := h.watch().Changelog(r.Context(), source, version)
	if err != nil {
		// A refused version or an unknown source is the caller's mistake and says
		// so; a version that is simply not published is a 404; a spent rate limit
		// or a GitHub outage is a gateway failure. Classified by sentinel, never
		// by matching the message text.
		status := http.StatusBadGateway
		switch {
		case errors.Is(err, upstreamwatch.ErrInvalidVersion), errors.Is(err, upstreamwatch.ErrUnknownSource):
			status = http.StatusBadRequest
		case errors.Is(err, upstreamwatch.ErrVersionNotPublished):
			status = http.StatusNotFound
		}
		log.Warn("upstream-watch", "changelog read failed", "source", source, "version", version, "error", err)
		handlerutil.WriteJSONError(w, status, err.Error())
		return
	}

	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(escapeAngleBrackets(notes)))
}

// escapeAngleBrackets neuters raw HTML in notes this server did not write.
//
// The modal renders the answer with {@html}, and marked passes raw tags through
// by design. A release body is written by whoever publishes the release — a
// repository the operator does not control — so an unescaped `<script>` would
// run in the dashboard's own origin, which holds the session cookie and the CLI
// token. Escaping `<` leaves markdown formatting intact and renders a literal
// angle bracket as the character it is.
func escapeAngleBrackets(markdown string) string {
	if !strings.Contains(markdown, "<") {
		return markdown
	}
	return strings.ReplaceAll(markdown, "<", "&lt;")
}
