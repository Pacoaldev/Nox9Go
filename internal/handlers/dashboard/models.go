package dashboard

import (
	json "encoding/json/v2"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strings"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/handlerutil"
)

// HandleGetCustomModels handles GET /api/models/custom.
// Returns custom models from kv scope customModels.
func (h *DashboardHandler) HandleGetCustomModels(w http.ResponseWriter, r *http.Request) {
	kv, err := h.Repo.GetKVScope("customModels")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	models := make([]any, 0, len(kv))
	for _, v := range kv {
		var item any
		if err := json.Unmarshal([]byte(v), &item); err == nil {
			models = append(models, item)
		} else {
			models = append(models, v)
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"models": models,
	})
}

// HandleSaveCustomModel handles POST /api/models/custom.
// Saves a custom model definition to kv scope customModels.
func (h *DashboardHandler) HandleSaveCustomModel(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var data map[string]any
	if err := json.Unmarshal(body, &data); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	// The array form is one request for a whole pasted list (D9). A client that
	// fired one call per id has nowhere to report the ids it could not save, and a
	// failure halfway leaves rows behind that the operator never sees.
	if _, batch := data["models"]; batch {
		h.saveCustomModelsBatch(w, r, data)
		return
	}

	var key string
	if k, ok := data["key"].(string); ok && k != "" {
		key = k
	} else if p, ok1 := data["providerAlias"].(string); ok1 && p != "" {
		mid, _ := data["id"].(string)
		mtype, _ := data["type"].(string)
		if mtype == "" {
			mtype = "llm"
		}
		key = fmt.Sprintf("%s|%s|%s", p, mid, mtype)
	} else if mid, ok2 := data["id"].(string); ok2 && mid != "" {
		key = mid
	}

	if key == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing custom model key or id")
		return
	}

	// The id is the segment a client sees as "<node-prefix>/<id>" and copies
	// into its config, so it is the part that must not collide with a combo
	// name or a model alias.
	modelID, _ := data["id"].(string)
	if modelID == "" {
		if parts := strings.SplitN(key, "|", 3); len(parts) >= 2 {
			modelID = parts[1]
		} else {
			modelID = key
		}
	}
	if reason := invalidModelIDReason(modelID); reason != "" {
		handlerutil.WriteJSON(w, http.StatusBadRequest, map[string]any{
			"error": reason,
			"code":  "INVALID_ID",
			"id":    modelID,
		})
		return
	}
	if h.guardNameCollision(w, nsCustomModel, modelID) {
		return
	}

	var valStr string
	if v, ok := data["value"]; ok {
		switch val := v.(type) {
		case string:
			valStr = val
		default:
			b, err := json.Marshal(val)
			if err != nil {
				handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to marshal model value")
				return
			}
			valStr = string(b)
		}
	} else {
		b, err := json.Marshal(data)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to marshal model data")
			return
		}
		valStr = string(b)
	}

	if err := h.Repo.SetKV("customModels", key, valStr); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A newly registered model is picked up by any rule whose pattern selects
	// it, which is the whole point of the feature: the operator never opens the
	// Combos page to add it.
	h.reconcileSmartCombosBestEffort(r.Context(), "model saved")

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    key,
	})
}

// HandleDeleteCustomModel handles DELETE /api/models/custom/{key}.
// Deletes a custom model from kv scope customModels.
func (h *DashboardHandler) HandleDeleteCustomModel(w http.ResponseWriter, r *http.Request) {
	key := getURLParam(r, "key")
	if unescaped, err := url.PathUnescape(key); err == nil && unescaped != "" {
		key = unescaped
	}
	if key == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing model key")
		return
	}

	if err := h.Repo.DeleteKV("customModels", key); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// The model is gone, so every combo a rule put it into must drop it. This
	// runs after the delete is answered: the operator's write already
	// succeeded, and a membership update must never turn it into a retry.
	h.reconcileSmartCombosBestEffort(r.Context(), "model deleted")

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"key":    key,
	})
}

// HandleDeleteProviderModels handles DELETE /api/providers/{id}/models.
//
// Removes every model one provider node carries, so an operator who imported a
// whole /models list can undo it in one request instead of deleting the rows one
// at a time. A node's models live in two places — the imported ones under kv
// customModels, and any legacy alias pointing at one of them — and both are
// removed, which is exactly what deleting the rows individually did. Models
// owned by another node and the catalog models that ship with the build are
// untouched: the catalog is not stored per node and cannot be deleted here.
func (h *DashboardHandler) HandleDeleteProviderModels(w http.ResponseWriter, r *http.Request) {
	provider := getURLParam(r, "id")
	if unescaped, err := url.PathUnescape(provider); err == nil && unescaped != "" {
		provider = unescaped
	}
	if provider == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing provider")
		return
	}

	kv, err := h.Repo.GetKVScope("customModels")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	removedModels := make([]string, 0, len(kv))
	for key, value := range kv {
		if !customModelBelongsToProvider(key, value, provider) {
			continue
		}
		if err := h.Repo.DeleteKV("customModels", key); err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		removedModels = append(removedModels, key)
	}

	// Aliases are resolved from the same kv table but a different scope, so they
	// are swept separately. A read failure here is not fatal: the custom models
	// are already gone, and reporting a partial sweep is better than leaving the
	// caller to guess which half ran.
	removedAliases := []string{}
	if aliases, err := h.Repo.GetModelAliases(); err == nil {
		prefix := provider + "/"
		for alias, full := range aliases {
			if !strings.HasPrefix(full, prefix) {
				continue
			}
			if err := h.Repo.DeleteKV("modelAliases", string(alias)); err != nil {
				handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
				return
			}
			removedAliases = append(removedAliases, string(alias))
		}
	}

	sort.Strings(removedModels)
	sort.Strings(removedAliases)
	// A whole node's models just stopped existing, so every combo a rule put
	// them into must drop them. Same best-effort rule as the single delete: the
	// operator's write is already answered.
	h.reconcileSmartCombosBestEffort(r.Context(), "provider models deleted")
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"provider":       provider,
		"removedModels":  removedModels,
		"removedAliases": removedAliases,
		"count":          len(removedModels) + len(removedAliases),
	})
}

// customModelBelongsToProvider decides whether one kv customModels row belongs to
// a provider node.
//
// The key is not always a discriminator: the import path writes
// "<providerAlias>|<id>|<type>" while the single-model form can write a bare id,
// so the key's first segment is checked first and the stored providerAlias field
// is the fallback. A row that matches neither is left alone rather than guessed
// at — removing another provider's models is not recoverable from here.
func customModelBelongsToProvider(key, value, provider string) bool {
	if alias, _, ok := strings.Cut(key, "|"); ok && alias == provider {
		return true
	}
	var cm db.CustomModel
	if err := json.Unmarshal([]byte(value), &cm); err != nil {
		return false
	}
	return cm.ProviderAlias == provider
}

// HandleGetDisabledModels handles GET /api/models/disabled.
// Returns disabled models per provider from kv scope disabledModels.
func (h *DashboardHandler) HandleGetDisabledModels(w http.ResponseWriter, r *http.Request) {
	kv, err := h.Repo.GetKVScope("disabledModels")
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	result := make(map[string]any, len(kv))
	for k, v := range kv {
		var arr any
		if err := json.Unmarshal([]byte(v), &arr); err == nil {
			result[k] = arr
		} else {
			result[k] = v
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, result)
}

// HandleSaveDisabledModels handles PUT /api/models/disabled/{provider}.
// Saves an array of disabled model IDs for a provider in kv scope disabledModels.
func (h *DashboardHandler) HandleSaveDisabledModels(w http.ResponseWriter, r *http.Request) {
	provider := getURLParam(r, "provider")
	if unescaped, err := url.PathUnescape(provider); err == nil && unescaped != "" {
		provider = unescaped
	}
	if provider == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "missing provider")
		return
	}

	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var raw any
	if err := json.Unmarshal(body, &raw); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}

	var modelsArr any
	switch v := raw.(type) {
	case []any:
		modelsArr = v
	case map[string]any:
		if m, ok := v["models"]; ok {
			modelsArr = m
		} else if dm, ok := v["disabledModels"]; ok {
			modelsArr = dm
		} else {
			modelsArr = []any{}
		}
	default:
		modelsArr = []any{}
	}

	b, err := json.Marshal(modelsArr)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, "failed to marshal models")
		return
	}

	if err := h.Repo.SetKV("disabledModels", provider, string(b)); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	// A model the operator switched off leaves every combo it was in, and one
	// they switch back on is picked up again by the rule that owns it.
	h.reconcileSmartCombosBestEffort(r.Context(), "models disabled")

	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
		"status":         "ok",
		"provider":       provider,
		"disabledModels": modelsArr,
	})
}

// HandleGetModelAliases handles GET /api/models/alias.
func (h *DashboardHandler) HandleGetModelAliases(w http.ResponseWriter, r *http.Request) {
	aliases, err := h.Repo.GetModelAliases()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	res := make(map[string]string, len(aliases))
	for k, v := range aliases {
		res[string(k)] = v
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"aliases": res})
}

// HandleSetModelAlias handles PUT /api/models/alias.
func (h *DashboardHandler) HandleSetModelAlias(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(r.Body)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		Model string `json:"model"`
		Alias string `json:"alias"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if req.Model == "" || req.Alias == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "model and alias required")
		return
	}

	if h.guardNameCollision(w, nsModelAlias, req.Alias) {
		return
	}

	valBytes, _ := json.Marshal(req.Model)
	if err := h.Repo.SetKV("modelAliases", req.Alias, string(valBytes)); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true, "model": req.Model, "alias": req.Alias})
}

// HandleDeleteModelAlias handles DELETE /api/models/alias.
func (h *DashboardHandler) HandleDeleteModelAlias(w http.ResponseWriter, r *http.Request) {
	alias := r.URL.Query().Get("alias")
	if alias == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "alias required")
		return
	}
	if err := h.Repo.DeleteKV("modelAliases", alias); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"success": true})
}
