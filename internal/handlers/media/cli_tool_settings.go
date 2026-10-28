package media

import (
	"errors"
	"net/http"
	"strings"

	json "encoding/json/v2"

	"nox9go/proxy/internal/clientconfig"
	"nox9go/proxy/internal/handlerutil"
)

// HTTP surface onto the clientconfig adapters: one route per client, three
// verbs, the same shape the dashboard's apply/reset buttons need.
//
// The route carries the client id (`/api/cli-tools/{tool}-settings`) and the id
// is resolved through clientconfig.Resolve, so an unknown name is a 404 listing
// what is accepted rather than a silent no-op.
//
// These routes write into the operator's home directory, so they belong to the
// dashboard group next to the Hermes ones: a session, an API key, or the local
// CLI token, never an anonymous caller.

// cliToolSettingsRequest is the POST body every adapter accepts.
type cliToolSettingsRequest struct {
	BaseURL string `json:"baseUrl"`
	APIKey  string `json:"apiKey"`
	Model   string `json:"model"`
}

// CLIToolHandler serves the per-client settings routes.
type CLIToolHandler struct{}

// NewCLIToolHandler returns a CLIToolHandler.
func NewCLIToolHandler() *CLIToolHandler { return &CLIToolHandler{} }

// toolFromPath pulls the client id out of `/api/cli-tools/<tool>-settings`.
func toolFromPath(path string) string {
	const prefix = "/api/cli-tools/"
	i := strings.Index(path, prefix)
	if i < 0 {
		return ""
	}
	rest := path[i+len(prefix):]
	rest = strings.TrimSuffix(rest, "-settings")
	return strings.TrimSuffix(rest, "/")
}

// resolveTool turns the path segment into an adapter.
func (h *CLIToolHandler) resolveTool(w http.ResponseWriter, r *http.Request) (clientconfig.Adapter, bool) {
	name := toolFromPath(r.URL.Path)
	if name == "" {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "unknown tool")
		return nil, false
	}
	adapter, err := clientconfig.Resolve(name)
	if err != nil {
		// The error names every accepted spelling, which is exactly what a
		// caller with a typo needs.
		handlerutil.WriteJSONError(w, http.StatusNotFound, err.Error())
		return nil, false
	}
	return adapter, true
}

// HandleGet serves GET /api/cli-tools/{tool}-settings: whether the client is
// installed, whether it already points at this gateway, and which files an
// apply would touch.
//
// The files are reported, not read back: the adapters own the write direction,
// and a read-back would need a second parser per dialect that would disagree
// with Apply the first time they drifted.
func (h *CLIToolHandler) HandleGet(w http.ResponseWriter, r *http.Request) {
	adapter, ok := h.resolveTool(w, r)
	if !ok {
		return
	}
	paths, err := adapter.Paths(clientconfig.Options{})
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	files := make([]map[string]any, 0, len(paths.Files))
	for _, f := range paths.Files {
		files = append(files, map[string]any{
			"path":    f.Path,
			"format":  f.Format.String(),
			"purpose": f.Purpose,
		})
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"tool":      adapter.ID(),
		"files":     files,
		"hasNox9Go": ToolHasNox9Go(adapter.ID()),
	})
}

// HandleApply serves POST /api/cli-tools/{tool}-settings.
func (h *CLIToolHandler) HandleApply(w http.ResponseWriter, r *http.Request) {
	adapter, ok := h.resolveTool(w, r)
	if !ok {
		return
	}
	var req cliToolSettingsRequest
	if err := json.UnmarshalRead(r.Body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "Invalid JSON body")
		return
	}
	baseURL := strings.TrimSpace(req.BaseURL)
	if baseURL == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "baseUrl is required")
		return
	}
	opts := clientconfig.Options{
		BaseURL: baseURL,
		APIKey:  strings.TrimSpace(req.APIKey),
		Model:   strings.TrimSpace(req.Model),
	}
	result, err := adapter.Apply(opts)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tool":    adapter.ID(),
		"changed": result.Changed,
		"backups": result.Backups,
		"message": "Settings applied",
	})
}

// HandleReset serves DELETE /api/cli-tools/{tool}-settings.
func (h *CLIToolHandler) HandleReset(w http.ResponseWriter, r *http.Request) {
	adapter, ok := h.resolveTool(w, r)
	if !ok {
		return
	}
	result, err := adapter.Reset(clientconfig.Options{})
	if err != nil {
		if errors.Is(err, clientconfig.ErrUnsupportedStructure) {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
			return
		}
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"tool":    adapter.ID(),
		"changed": result.Changed,
		"backups": result.Backups,
		"message": "Settings reset",
	})
}

// ToolHasNox9Go reports whether the named tool is configured against this
// gateway. It is the single answer the dashboard badge and the settings route
// both use, so the two cannot disagree.
func ToolHasNox9Go(id string) bool {
	for _, t := range cliTools {
		if t.id == id && t.hasNox9Go != nil {
			return t.hasNox9Go()
		}
	}
	return false
}
