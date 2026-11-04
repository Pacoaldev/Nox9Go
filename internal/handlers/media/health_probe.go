package media

import (
	"context"
	"net/http"
)

// HealthVerdict is what ProbeModelForHealth reports about one model.
type HealthVerdict struct {
	OK bool
	// Blocked means the account is parked: nothing was asked of the model.
	Blocked bool
	Error   string
}

// ProbeModelForHealth runs the same chat probe the dashboard's Test button
// runs for "<provider>/<model>" and reduces the answer to a verdict. It exists
// for the model-health loop, which has no HTTP request of its own to probe from.
func (h *MediaHandler) ProbeModelForHealth(ctx context.Context, model string) HealthVerdict {
	// probeModel only reads the request's context.
	r, err := http.NewRequestWithContext(ctx, http.MethodPost, "/api/models/test", nil)
	if err != nil {
		return HealthVerdict{Error: err.Error()}
	}
	res := h.probeModel(r, model, "")
	return HealthVerdict{OK: res.OK, Blocked: res.Blocked, Error: res.Error}
}