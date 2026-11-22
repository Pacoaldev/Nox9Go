package dashboard

import (
	"encoding/json"
	"io"
	"net/http"
	"nox9go/proxy/internal/config"
	"nox9go/proxy/internal/devstatus"
	"strings"
	"time"

	"nox9go/proxy/internal/handlers/chat"
	"nox9go/proxy/internal/handlerutil"
)

// decodeTelegramBody reads a request body into dst. The size cap is the point:
// these are small settings payloads, and an unbounded read of a client-supplied
// body is a memory problem on the hardware this runs on.
func decodeTelegramBody(r *http.Request, dst any) error {
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil {
		return err
	}
	return json.Unmarshal(body, dst)
}

// telegramStatus is what the Canales tab renders. The token is only ever
// reported masked: a dashboard read must not be able to exfiltrate the secret it
// just stored.
type telegramStatus struct {
	Enabled        bool    `json:"enabled"`
	TokenPresent   bool    `json:"tokenPresent"`
	TokenMasked    string  `json:"tokenMasked"`
	AllowedUserIDs []int64 `json:"allowedUserIds"`
	Model          string  `json:"model"`
	State          string  `json:"state"`
	BotUsername    string  `json:"botUsername"`
	LastError      string  `json:"lastError"`
}

// TelegramLiveStatus is the source of the live channel state. Optional: a
// handler built outside a boot has none and falls back to the stored decision.
type TelegramLiveStatus interface {
	Status() (state, botUsername, lastError string)
}

// HandleGetTelegramChannel returns the channel's stored settings.
//
// It reports the state from storage rather than from a live supervisor so the
// tab still answers while the channel is stopped — which is exactly when the
// operator opens it to find out why.
func (h *DashboardHandler) HandleGetTelegramChannel(w http.ResponseWriter, r *http.Request) {
	cfg, err := h.Repo.GetTelegramChannelConfig()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	model, err := h.Repo.TelegramChannelModel()
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	status := telegramStatus{
		Enabled:        cfg.Enabled,
		TokenPresent:   cfg.TokenPresent,
		TokenMasked:    cfg.TokenMasked,
		AllowedUserIDs: cfg.AllowedUserIDs,
		Model:          model,
		State:          "disabled",
	}
	if !cfg.TokenPresent {
		status.State = "no_token"
	} else if len(cfg.AllowedUserIDs) == 0 {
		// The fail-closed gate reports its own reason: an operator looking at a
		// stopped bot needs to know which of the two conditions is missing.
		status.State = "no_allowlist"
	} else if cfg.Enabled {
		status.State = "running"
	}
	// The stored decision is the fallback; the live loop is the truth. A green
	// banner that says running while the poll loop is dead is worse than an
	// honest error, because it kills the first place an operator looks.
	if h.TelegramLiveStatus != nil {
		state, botUsername, lastError := h.TelegramLiveStatus.Status()
		if lastError != "" {
			status.State = "error"
			status.LastError = lastError
		} else if state != "" {
			status.State = state
			status.BotUsername = botUsername
		}
	}

	handlerutil.WriteJSON(w, http.StatusOK, status)
}

// HandleSetTelegramToken stores the bot token and reconciles the channel.
//
// The token is encrypted before it reaches storage, so a database copy does not
// carry a working bot credential.
func (h *DashboardHandler) HandleSetTelegramToken(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Token string `json:"token"`
	}
	if err := decodeTelegramBody(r, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	token := strings.TrimSpace(req.Token)
	if token == "" {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "token is required")
		return
	}
	// Telegram hands out "<bot_id>:<secret>". Rejecting anything else here means a
	// pasted chat id or a truncated paste fails at the form instead of as a
	// confusing 401 from the Telegram API minutes later.
	if !looksLikeBotToken(token) {
		handlerutil.WriteJSONError(w, http.StatusBadRequest,
			"token must look like <bot id>:<secret>")
		return
	}

	if err := h.Repo.SaveTelegramToken(token); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.reconcileTelegramChannel()
	h.HandleGetTelegramChannel(w, r)
}

// HandleSetTelegramAllowlist replaces the allowlist and reconciles the channel.
//
// An empty list is accepted and means "authorize nobody", which keeps the poll
// loop stopped. Refusing an empty list would leave no way to revoke everyone.
func (h *DashboardHandler) HandleSetTelegramAllowlist(w http.ResponseWriter, r *http.Request) {
	var req struct {
		UserIDs []int64 `json:"userIds"`
	}
	if err := decodeTelegramBody(r, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}
	for _, id := range req.UserIDs {
		if id <= 0 {
			handlerutil.WriteJSONError(w, http.StatusBadRequest, "user ids must be positive")
			return
		}
	}

	if err := h.Repo.SetTelegramAllowedUserIDs(req.UserIDs); err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	h.reconcileTelegramChannel()
	h.HandleGetTelegramChannel(w, r)
}

// reconcileTelegramChannel asks the owner of the poll loop to re-evaluate its
// state after a settings change.
//
// It is a callback rather than a direct call so this package does not import the
// channel package: the dashboard owns the settings form, the channel owns the
// loop, and neither needs the other's types. A nil hook (tests, tooling) makes
// the save take effect on the next start, which is the same behaviour as before.
func (h *DashboardHandler) reconcileTelegramChannel() {
	if h.ReconcileTelegramChannel != nil {
		h.ReconcileTelegramChannel()
	}
}

// looksLikeBotToken checks the shape Telegram uses: a numeric bot id, a colon,
// and a non-empty secret.
func looksLikeBotToken(token string) bool {
	id, secret, ok := strings.Cut(token, ":")
	if !ok || id == "" || secret == "" {
		return false
	}
	for _, c := range id {
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

// HandleTelegramModels lists the model ids this gateway can dispatch, for the
// bot's /model command (S6).
//
// The /v1/models route needs a client API key, which the bot does not have and
// must not need: it is this process talking to itself over the loopback. The
// id providers used to offer a list straight out of the catalog, which goes
// stale the moment a connection ages out — the same stale-catalog trap that
// AGENTS.md §0.3 documents for github and clinepass. Building the same list
// the /v1/models route builds, minus the per-key access filter (the caller is
// the operator's own process), keeps it truthful.
func (h *DashboardHandler) HandleTelegramModels(w http.ResponseWriter, r *http.Request) {
	if h.ChatHandler == nil {
		handlerutil.WriteJSONError(w, http.StatusServiceUnavailable,
			"models are unavailable while the engine is not wired")
		return
	}
	result := h.ChatHandler.BuildModelsListResult(r.Context(), chat.ModeListConnected)
	ids := make([]string, 0, len(result.Models))
	for _, m := range result.Models {
		if m.ID != "" {
			ids = append(ids, m.ID)
		}
	}
	handlerutil.WriteJSON(w, http.StatusOK, map[string]any{"models": ids})
}

// HandleDevStatus reports where the development stands, for the dashboard's
// development panel (S5).
//
// It is deliberately the same report the bot's /estado prints, built by the same
// package, so the two cannot drift: an operator who checks the dashboard and an
// operator who checks the phone are reading one answer with two renderings.
func (h *DashboardHandler) HandleDevStatus(w http.ResponseWriter, r *http.Request) {
	root := config.DevRepoRoot()
	if root == "" {
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"configured": false,
			"message":    "Set DEV_REPO_ROOT in the gateway's environment or env file to enable the development report.",
		})
		return
	}

	feature := strings.TrimSpace(r.URL.Query().Get("feature"))
	if feature == "" {
		feature = config.DevFeature()
	}

	snap := devstatus.Repo{Root: root}.Take(r.Context(), feature)

	payload := map[string]any{
		"configured":  true,
		"repoKnown":   snap.RepoKnown,
		"branch":      snap.Branch,
		"head":        snap.Head,
		"headSubject": snap.HeadSubject,
		"changes":     len(snap.Changes),
		"rendered":    snap.Render(),
		"features":    devstatus.Repo{Root: root}.ListFeatures(),
	}
	if !snap.HeadWhen.IsZero() {
		payload["headAt"] = snap.HeadWhen.UTC().Format(time.RFC3339)
	}
	if snap.Feature != nil {
		payload["feature"] = map[string]any{
			"name":  snap.Feature.Name,
			"title": snap.Feature.Title,
			"done":  snap.Feature.Done,
			"total": snap.Feature.Total,
		}
	}
	handlerutil.WriteJSON(w, http.StatusOK, payload)
}
