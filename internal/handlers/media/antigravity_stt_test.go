package media

import (
	"bytes"
	json "encoding/json/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"nox9go/proxy/internal/db"
	"nox9go/proxy/internal/providers"
)

func TestHandleSTT_Antigravity(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/v1internal:generateContent") {
			t.Errorf("expected path /v1internal:generateContent, got %s", r.URL.Path)
		}
		if auth := r.Header.Get("Authorization"); auth != "Bearer ag-token-stt-123" {
			t.Errorf("expected Bearer ag-token-stt-123, got %q", auth)
		}

		var reqBody struct {
			Project string `json:"project"`
			Model   string `json:"model"`
			Request struct {
				Contents []struct {
					Role  string `json:"role"`
					Parts []struct {
						Text       string `json:"text"`
						InlineData struct {
							MimeType string `json:"mimeType"`
							Data     string `json:"data"`
						} `json:"inlineData"`
					} `json:"parts"`
				} `json:"contents"`
			} `json:"request"`
		}
		if err := json.UnmarshalRead(r.Body, &reqBody); err != nil {
			t.Errorf("decode request body: %v", err)
		}

		if reqBody.Project != "test-stt-proj" {
			t.Errorf("expected project test-stt-proj, got %s", reqBody.Project)
		}

		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{
			"response": {
				"candidates": [{
					"content": {
						"parts": [{
							"text": "Hello, this is a test audio transcription."
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
		"apiKey":    "ag-token-stt-123",
		"projectId": "test-stt-proj",
	})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-ag-stt', 'antigravity', 'oauth', 'Antigravity STT Test', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(connData)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)

	// Create multipart body
	bodyBuf := &bytes.Buffer{}
	mpWriter := multipart.NewWriter(bodyBuf)
	filePart, err := mpWriter.CreateFormFile("file", "test.mp3")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	filePart.Write([]byte("dummy mp3 audio content"))
	_ = mpWriter.WriteField("model", "antigravity/gemini-2.5-flash")
	_ = mpWriter.WriteField("response_format", "json")
	mpWriter.Close()

	req := httptest.NewRequest("POST", "/v1/audio/transcriptions", bodyBuf)
	req.Header.Set("Content-Type", mpWriter.FormDataContentType())
	rec := httptest.NewRecorder()

	handler.HandleAudioTranscriptions(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Text string `json:"text"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("parse response: %v", err)
	}

	if resp.Text != "Hello, this is a test audio transcription." {
		t.Errorf("expected transcribed text, got %q", resp.Text)
	}
}
