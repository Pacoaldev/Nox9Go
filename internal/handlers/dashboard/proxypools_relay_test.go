package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

// Upstream parity: relay-type pools (vercel/cloudflare/deno) must be tested
// with a plain GET carrying x-relay-target/x-relay-path headers — never
// dialed as an HTTP proxy (which fails with "malformed HTTP status code").
func TestTestRelayPool(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()

	if _, err := repo.RawDB().Exec(`CREATE TABLE IF NOT EXISTS proxyPools (
		id TEXT PRIMARY KEY,
		isActive INTEGER DEFAULT 1,
		testStatus TEXT,
		data TEXT NOT NULL,
		createdAt TEXT NOT NULL,
		updatedAt TEXT NOT NULL
	);`); err != nil {
		t.Fatalf("failed to create proxyPools table: %v", err)
	}

	var gotTarget, gotPath string
	relay := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotTarget = r.Header.Get("x-relay-target")
		gotPath = r.Header.Get("x-relay-path")
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"ok":true}`))
	}))
	defer relay.Close()

	router := setupTestRouter(repo)

	bodyBytes, _ := json.Marshal(map[string]any{
		"name":     "Vercel Relay",
		"proxyUrl": relay.URL,
		"type":     "vercel",
	})
	req := httptest.NewRequest(http.MethodPost, "/api/proxy-pools", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("create pool expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var pool map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &pool); err != nil {
		t.Fatalf("failed to unmarshal pool: %v", err)
	}
	poolID, _ := pool["id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/proxy-pools/"+poolID+"/test", nil)
	testRec := httptest.NewRecorder()
	router.ServeHTTP(testRec, req)
	if testRec.Code != http.StatusOK {
		t.Fatalf("test relay expected 200, got %d: %s", testRec.Code, testRec.Body.String())
	}
	var result map[string]any
	if err := json.Unmarshal(testRec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if result["success"] != true {
		t.Fatalf("expected success=true, got %v", result)
	}
	if gotTarget != "https://httpbin.org" || gotPath != "/get" {
		t.Fatalf("relay got wrong headers: target=%q path=%q", gotTarget, gotPath)
	}

	// Failing relay (500) must report success=false with an error message.
	bad := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadGateway)
	}))
	defer bad.Close()

	bodyBytes, _ = json.Marshal(map[string]any{
		"name":     "Broken Relay",
		"proxyUrl": bad.URL,
		"type":     "cloudflare",
	})
	req = httptest.NewRequest(http.MethodPost, "/api/proxy-pools", bytes.NewReader(bodyBytes))
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	var badPool map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &badPool); err != nil {
		t.Fatalf("failed to unmarshal pool: %v", err)
	}
	badID, _ := badPool["id"].(string)

	req = httptest.NewRequest(http.MethodPost, "/api/proxy-pools/"+badID+"/test", nil)
	testRec = httptest.NewRecorder()
	router.ServeHTTP(testRec, req)
	var badResult map[string]any
	if err := json.Unmarshal(testRec.Body.Bytes(), &badResult); err != nil {
		t.Fatalf("failed to unmarshal result: %v", err)
	}
	if badResult["success"] != false {
		t.Fatalf("expected success=false, got %v", badResult)
	}
	if _, ok := badResult["error"].(string); !ok {
		t.Fatalf("expected error message, got %v", badResult)
	}
}
