package chat

import (
	"net/http"

	"nox9go/proxy/internal/handlerutil"
	"nox9go/proxy/internal/providers"
)

// HandleCatalogSyncStatus returns current models.dev sync status (GET /api/models/catalog-sync).
func (h *ChatHandler) HandleCatalogSyncStatus(w http.ResponseWriter, r *http.Request) {
	state := providers.GetCatalogState()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"running":    state.Running,
		"lastSync":   state.LastSync,
		"lastError":  state.LastError,
		"etag":       state.ETag,
		"modelCount": state.ModelCount,
		"url":        providers.ModelsDevCatalogURL,
		"intervalMs": providers.CatalogSyncInterval.Milliseconds(),
	})
}

// HandleCatalogSyncTrigger triggers a manual catalog sync (POST /api/models/catalog-sync).
func (h *ChatHandler) HandleCatalogSyncTrigger(w http.ResponseWriter, r *http.Request) {
	err := providers.SyncModelCatalog(r.Context(), h.Client, "")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable, err.Error())
		return
	}
	state := providers.GetCatalogState()
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"success": true,
		"result": map[string]any{
			"syncedAt":   state.LastSync,
			"modelCount": state.ModelCount,
		},
	})
}
