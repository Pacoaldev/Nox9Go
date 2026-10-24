package dashboard

import (
	"bytes"
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"testing"
)

func postConnection(t *testing.T, router http.Handler, body string) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest(http.MethodPost, "/api/connections", bytes.NewReader([]byte(body)))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	router.ServeHTTP(rec, req)
	return rec
}

func TestHandleCreateConnection_StoresModalFields(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	body := `{
		"provider": "clinepass",
		"authType": "apikey",
		"name": "ClinePass Production",
		"apiKey": "sk-cline-secret",
		"priority": 7,
		"testStatus": "active",
		"proxyPoolId": "pool-42",
		"providerSpecificData": {"accountId": "acc-1"}
	}`
	rec := postConnection(t, router, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &created); err != nil {
		t.Fatalf("decode create response: %v", err)
	}
	id, _ := created["id"].(string)
	if id == "" {
		t.Fatal("expected created connection id")
	}

	conn, err := repo.GetProviderConnectionByID(id)
	if err != nil || conn == nil {
		t.Fatalf("failed to reload connection: %v", err)
	}
	if conn.Name == nil || *conn.Name != "ClinePass Production" {
		t.Errorf("unexpected name %v", conn.Name)
	}
	if conn.Priority == nil || *conn.Priority != 7 {
		t.Errorf("expected priority 7, got %v", conn.Priority)
	}

	var data map[string]any
	if err := json.Unmarshal([]byte(conn.Data), &data); err != nil {
		t.Fatalf("decode connection data: %v", err)
	}
	if data["apiKey"] != "sk-cline-secret" {
		t.Errorf("expected apiKey stored, got %v", data["apiKey"])
	}
	if data["testStatus"] != "active" {
		t.Errorf("expected testStatus stored, got %v", data["testStatus"])
	}
	if data["proxyPoolId"] != "pool-42" {
		t.Errorf("expected proxyPoolId stored, got %v", data["proxyPoolId"])
	}
	psd, ok := data["providerSpecificData"].(map[string]any)
	if !ok || psd["accountId"] != "acc-1" {
		t.Errorf("expected providerSpecificData stored, got %v", data["providerSpecificData"])
	}
}

func TestHandleCreateConnection_DefaultsPriorityToNextFreeSlot(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	first := postConnection(t, router, `{"provider":"deepseek","name":"First","apiKey":"k1"}`)
	if first.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", first.Code, first.Body.String())
	}
	var firstBody map[string]any
	_ = json.Unmarshal(first.Body.Bytes(), &firstBody)
	firstConn, _ := repo.GetProviderConnectionByID(firstBody["id"].(string))
	if firstConn.Priority == nil || *firstConn.Priority != 1 {
		t.Fatalf("expected first connection priority 1, got %v", firstConn.Priority)
	}

	second := postConnection(t, router, `{"provider":"deepseek","name":"Second","apiKey":"k2"}`)
	if second.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", second.Code, second.Body.String())
	}
	var secondBody map[string]any
	_ = json.Unmarshal(second.Body.Bytes(), &secondBody)
	secondConn, _ := repo.GetProviderConnectionByID(secondBody["id"].(string))
	if secondConn.Priority == nil || *secondConn.Priority != 2 {
		t.Errorf("expected fallback priority max+1 = 2, got %v", secondConn.Priority)
	}
}

func TestHandleCreateConnection_KeepsLegacyDataString(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	body := `{"provider":"cline","authType":"oauth","name":"OAuth Conn","data":"{\"refreshToken\":\"rt-1\",\"accessToken\":\"at-1\"}"}`
	rec := postConnection(t, router, body)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d (%s)", rec.Code, rec.Body.String())
	}
	var created map[string]any
	_ = json.Unmarshal(rec.Body.Bytes(), &created)
	conn, _ := repo.GetProviderConnectionByID(created["id"].(string))

	var data map[string]any
	if err := json.Unmarshal([]byte(conn.Data), &data); err != nil {
		t.Fatalf("decode connection data: %v", err)
	}
	if data["refreshToken"] != "rt-1" || data["accessToken"] != "at-1" {
		t.Errorf("legacy data payload not preserved: %v", data)
	}
}

func TestHandleCreateConnection_RequiresProvider(t *testing.T) {
	repo, cleanup := setupTestDB(t)
	defer cleanup()
	router := setupTestRouter(repo)

	rec := postConnection(t, router, `{"name":"No provider","apiKey":"k"}`)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("expected 400, got %d", rec.Code)
	}
}
