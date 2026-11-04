//go:build integration

package integration

import (
	"net/http"
	"testing"

	"nox9go/proxy/internal/auth"
)

// The catalogue sweep's dashboard route has to exist on the router the
// production boot wires, be reachable with the operator's credential, and
// answer the shape the view reads. A unit test that mounts a hand-built router
// cannot see any of those three.
func TestCatalogSweepRouteIsWired(t *testing.T) {
	env := newEnv(t)

	res := env.Get(t, "/api/models/sweep", WithHeader(auth.CLITokenHeader, auth.CLIToken()), env.AsSession())
	if res.Status != http.StatusOK {
		t.Fatalf("GET /api/models/sweep status = %d: %s", res.Status, truncate(res.Body))
	}

	var body struct {
		Config struct {
			Enabled     bool `json:"enabled"`
			IntervalMin int  `json:"intervalMin"`
		} `json:"config"`
		Providers map[string]any `json:"providers"`
	}
	res.Decode(t, &body)

	if !body.Config.Enabled || body.Config.IntervalMin != 720 {
		t.Errorf("config = %+v, want the default sweep enabled on a 720 minute interval", body.Config)
	}
	if body.Providers == nil {
		t.Errorf("providers = nil, want an object, empty before the first sweep")
	}
	if len(body.Providers) != 0 {
		t.Errorf("providers = %+v, want none before the first sweep", body.Providers)
	}

	// The route is administration: without a credential it must refuse rather
	// than run a sweep on behalf of an anonymous caller.
	anonymous := env.Get(t, "/api/models/sweep", WithoutAPIKey())
	if anonymous.Status != http.StatusUnauthorized {
		t.Errorf("unauthenticated GET /api/models/sweep status = %d, want 401", anonymous.Status)
	}
}
