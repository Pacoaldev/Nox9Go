package media

import (
	"bytes"
	json "encoding/json/v2"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"nox9go/proxy/internal/db"
)

func TestE2E_EdgeTTS_LiveEndpoint(t *testing.T) {
	if os.Getenv("ROUTER_LIVE_E2E") == "" {
		t.Skip("skipping live Edge TTS network test (set ROUTER_LIVE_E2E=1 to run)")
	}

	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)
	// Test 1: JSON response format
	body := []byte(`{"model":"edge-tts/en-US-AriaNeural","input":"Testing 9router speech synthesis"}`)
	req := httptest.NewRequest("POST", "/v1/audio/speech?response_format=json", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()

	handler.HandleAudioSpeech(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var jsonResp struct {
		Format string `json:"format"`
		Audio  string `json:"audio"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &jsonResp); err != nil {
		t.Fatalf("unmarshal json: %v", err)
	}

	if jsonResp.Format != "mp3" {
		t.Errorf("expected format mp3, got %s", jsonResp.Format)
	}
	if len(jsonResp.Audio) < 100 {
		t.Errorf("expected base64 audio > 100 chars, got %d", len(jsonResp.Audio))
	}

	t.Logf("TTS JSON Success: format=%s, audio base64 length=%d", jsonResp.Format, len(jsonResp.Audio))
}

func TestE2E_AudioVoices_EdgeTTS(t *testing.T) {
	if os.Getenv("ROUTER_LIVE_E2E") == "" {
		t.Skip("skipping live Edge TTS network test (set ROUTER_LIVE_E2E=1 to run)")
	}

	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()

	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)
	req := httptest.NewRequest("GET", "/api/media-providers/tts/voices?provider=edge-tts", nil)
	rec := httptest.NewRecorder()

	handler.HandleAudioVoices(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}

	var resp struct {
		Languages []struct {
			Code   string `json:"code"`
			Name   string `json:"name"`
			Voices []any  `json:"voices"`
		} `json:"languages"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &resp); err != nil {
		t.Fatalf("unmarshal voices response: %v", err)
	}

	if len(resp.Languages) == 0 {
		t.Fatalf("expected languages list, got 0")
	}

	t.Logf("Edge TTS Voices Success: found %d languages", len(resp.Languages))
}

func TestE2E_STT_Multipart_MockGroq(t *testing.T) {
	mockGroq := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gsk-test-123" {
			t.Errorf("unexpected auth header: %s", r.Header.Get("Authorization"))
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{"text":"The audio transcription was completely successful."}`))
	}))
	defer mockGroq.Close()

	database, cleanup := setupMultimodalTestDB(t)
	defer cleanup()
	connData, _ := json.Marshal(map[string]any{
		"apiKey":  "gsk-test-123",
		"baseUrl": mockGroq.URL,
	})
	if _, err := database.Exec(`INSERT INTO providerConnections (id, provider, authType, name, priority, isActive, data, createdAt, updatedAt) VALUES
		('conn-groq', 'groq', 'apikey', 'Groq Main', 1, 1, ?, '2026-07-18T00:00:00Z', '2026-07-18T00:00:00Z')`, string(connData)); err != nil {
		t.Fatalf("seed connection: %v", err)
	}

	repo := db.NewRepo(database)
	handler := newTestMediaHandler(repo)

	bodyBuf := &bytes.Buffer{}
	mpWriter := multipart.NewWriter(bodyBuf)
	filePart, err := mpWriter.CreateFormFile("file", "speech.mp3")
	if err != nil {
		t.Fatalf("create form file: %v", err)
	}
	filePart.Write([]byte("fake audio content"))
	_ = mpWriter.WriteField("model", "groq/whisper-large-v3")
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
		t.Fatalf("unmarshal stt response: %v", err)
	}

	if resp.Text != "The audio transcription was completely successful." {
		t.Errorf("unexpected text: %s", resp.Text)
	}

	t.Logf("STT Multipart Success: text=%q", resp.Text)
}
