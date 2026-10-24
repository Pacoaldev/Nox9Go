package media

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/providers"
)

func TestHandleImage_Antigravity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1internal:generateContent") {
			t.Errorf("expected path /v1internal:generateContent, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer ag-token-img-123" {
			t.Errorf("expected Bearer ag-token-img-123, got %q", auth)
		}

		var reqBody struct {
			Project     string `json:"project"`
			Model       string `json:"model"`
			RequestType string `json:"requestType"`
			Request     struct {
				Contents []struct {
					Role  string `json:"role"`
					Parts []struct {
						Text string `json:"text"`
					} `json:"parts"`
				} `json:"contents"`
			} `json:"request"`
		}
		if err := json.UnmarshalRead(r.Body, &reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}

		if reqBody.Project != "test-image-proj" {
			t.Errorf("expected project test-image-proj, got %s", reqBody.Project)
		}
		if reqBody.RequestType != "image_gen" {
			t.Errorf("expected requestType image_gen, got %s", reqBody.RequestType)
		}
		if len(reqBody.Request.Contents) == 0 || len(reqBody.Request.Contents[0].Parts) == 0 {
			t.Fatalf("expected contents parts, got %v", reqBody.Request.Contents)
		}
		if reqBody.Request.Contents[0].Parts[0].Text != "A cute cat wearing a hat" {
			t.Errorf("expected prompt text, got %s", reqBody.Request.Contents[0].Parts[0].Text)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"response": {
				"candidates": [{
					"content": {
						"parts": [{
							"inlineData": {
								"mimeType": "image/png",
								"data": "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg=="
							}
						}]
					}
				}]
			}
		}`))
	}))
	defer upstream.Close()

	ag := providers.KnownProviders["antigravity"]
	origBase := ag.BaseURL
	ag.BaseURL = upstream.URL
	providers.KnownProviders["antigravity"] = ag
	defer func() {
		ag.BaseURL = origBase
		providers.KnownProviders["antigravity"] = ag
	}()

	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()

	connData, _ := json.Marshal(map[string]any{
		"apiKey":    "ag-token-img-123",
		"projectId": "test-image-proj",
	})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-ag-img', 'antigravity', 'oauth', 'Antigravity Image Test', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(connData)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)

	body := `{"model":"antigravity/gemini-3.1-flash-image","prompt":"A cute cat wearing a hat","n":1,"size":"auto"}`
	req := httptest.NewRequest("POST", "/v1/images/generations", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleImages(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Created int64 `json:"created"`
		Data    []struct {
			B64JSON       string `json:"b64_json"`
			RevisedPrompt string `json:"revised_prompt"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if len(resp.Data) != 1 {
		t.Fatalf("expected 1 image in data, got %d", len(resp.Data))
	}
	if !strings.HasPrefix(resp.Data[0].B64JSON, "iVBORw0KGgo") {
		t.Errorf("expected base64 PNG data, got %s", resp.Data[0].B64JSON)
	}
}
