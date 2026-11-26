package chat

import (
	"database/sql"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
)

// S4/S5 — the X-Nox-Role header.
//
// Every provider the fixture touches is given its own connection whose baseUrl
// cannot be parsed. Request construction then fails before a socket opens, and
// the failure message embeds that connection's URL — so the marker in the
// response body names exactly which connection served the request, and no test
// here can pass by accident: the body model's own marker proves the header was
// ignored, and a different marker proves which combo the header selected.
const (
	bodyMarker  = "body-only"
	lightMarker = "light-only"
	workMarker  = "work-only"
	planMarker  = "plan-only"
)

func addBadConnection(t *testing.T, database *sql.DB, id, provider, marker string) {
	t.Helper()

	data, _ := json.Marshal(map[string]any{"apiKey": "sk-mock-key", "baseUrl": "http://" + marker + `"\b`})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		(?, ?, 'apikey', ?, 0, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
		id, provider, id, string(data)); err != nil {
		t.Fatalf("insert connection %s: %v", id, err)
	}
}

func roleHeaderFixture(t *testing.T) (*ChatHandler, string) {
	t.Helper()

	database, cleanup := setupChatTestDB(t)
	t.Cleanup(cleanup)

	// The seeded connections point at real hosts; drop them so only the bad-URL
	// ones remain and nothing can reach the network.
	if _, err := database.Exec(`DELETE FROM providerConnections`); err != nil {
		t.Fatalf("clear seeded connections: %v", err)
	}
	addBadConnection(t, database, "conn-body", "openai", bodyMarker)
	addBadConnection(t, database, "conn-light", "groq", lightMarker)
	addBadConnection(t, database, "conn-work", "deepseek", workMarker)
	addBadConnection(t, database, "conn-plan", "anthropic", planMarker)

	for _, c := range []struct{ id, name, member string }{
		{"cl", "Combo-Light", "groq/light-model"},
		{"cw", "Combo-Work", "deepseek/work-model"},
		{"cp", "Combo-Plan", "anthropic/plan-model"},
	} {
		if _, err := database.Exec(`INSERT INTO combos (id, name, kind, models, createdAt, updatedAt) VALUES
			(?, ?, 'fallback', ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`,
			c.id, c.name, `["`+c.member+`"]`); err != nil {
			t.Fatalf("insert combo %s: %v", c.name, err)
		}
	}

	return NewChatHandler(db.NewRepo(database)), `{"model":"openai/body-model","messages":[{"role":"user","content":"hi"}]}`
}

func postChat(t *testing.T, handler *ChatHandler, body, role string) *httptest.ResponseRecorder {
	t.Helper()

	req := httptest.NewRequest("POST", "/v1/chat/completions", strings.NewReader(body))
	if role != "" {
		req.Header.Set("X-Nox-Role", role)
	}
	rec := httptest.NewRecorder()
	handler.HandleChatCompletions(rec, req)
	return rec
}

// S4 — each valid role forces its own combo with priority over the body model.
func TestChatRoleHeader_ForcesProfileComboOverBodyModel(t *testing.T) {
	tests := []struct {
		role   string
		marker string
	}{
		{role: "light", marker: lightMarker},
		{role: "work", marker: workMarker},
		{role: "plan", marker: planMarker},
	}

	for _, tt := range tests {
		t.Run(tt.role, func(t *testing.T) {
			handler, body := roleHeaderFixture(t)

			rec := postChat(t, handler, body, tt.role)

			if rec.Code != http.StatusBadGateway {
				t.Fatalf("status = %d, want 502; body: %s", rec.Code, rec.Body.String())
			}
			if !strings.Contains(rec.Body.String(), tt.marker) {
				t.Errorf("body does not name the %s connection; got: %s", tt.marker, rec.Body.String())
			}
			if strings.Contains(rec.Body.String(), bodyMarker) {
				t.Errorf("body model served the request: the header was ignored; got: %s", rec.Body.String())
			}
		})
	}
}

// S5 — an unrecognized value is ignored, and the result is byte-identical to a
// request without the header.
func TestChatRoleHeader_UnknownValueIsIgnored(t *testing.T) {
	handler, body := roleHeaderFixture(t)

	without := postChat(t, handler, body, "")
	bogus := postChat(t, handler, body, "  LIGHTNING  ")

	if without.Code != http.StatusBadGateway {
		t.Fatalf("baseline status = %d, want 502; body: %s", without.Code, without.Body.String())
	}
	if !strings.Contains(without.Body.String(), bodyMarker) {
		t.Fatalf("baseline did not use the body model's connection; got: %s", without.Body.String())
	}
	if bogus.Code != without.Code {
		t.Errorf("status = %d, want %d (same as no header)", bogus.Code, without.Code)
	}
	if bogus.Body.String() != without.Body.String() {
		t.Errorf("body = %q, want %q (identical to no header)", bogus.Body.String(), without.Body.String())
	}
}
