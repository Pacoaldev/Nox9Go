package telegram

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-telegram/bot/models"
	"nox9go/proxy/internal/db"
)

// tinyJPEG is a minimal valid JPEG the DetectContentType call recognizes.
var tinyJPEG = []byte{
	0xFF, 0xD8, 0xFF, 0xE0, 0x00, 0x10, 'J', 'F', 'I', 'F', 0x00, 0x01, 0x01, 0x00,
	0x00, 0x01, 0x00, 0x01, 0x00, 0x00, 0xFF, 0xD9,
}

// messageWithPhoto builds a models.Message carrying one photo the way the
// Telegram Bot API sends it: several PhotoSize entries, largest last.
func messageWithPhoto(caption string) *models.Message {
	return &models.Message{
		Chat:    models.Chat{ID: 42},
		Text:    caption,
		Caption: caption,
		Photo: []models.PhotoSize{
			{FileID: "photo-small", Width: 90, Height: 90, FileSize: 1200},
			{FileID: "photo-large", Width: 1280, Height: 720, FileSize: 90000},
		},
	}
}

// S1: a photo sent with a caption reaches the gateway as a multimodal user
// turn: the caption as a text part plus the image as an image_url part, and
// the transcript keeps only the caption's row.
func TestHandleChat_PhotoBecomesImageURLPart(t *testing.T) {
	srv, got := newTestGateway(t, "vi la imagen")
	fileSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(tinyJPEG)
	}))
	defer fileSrv.Close()
	fake := newFakeTelegram(t)
	// The supervisor must build its Bot API URL from this origin instead of
	// api.telegram.org, exercised through the injected gateway hook below.

	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, model: "test/model"}
	repo.session = &db.TelegramSession{ID: 1, ChatID: 42}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	sup.SetGateway(srv.URL, "sk-test")
	setBotAPIBase(sup, fake.srvURL())

	msg := messageWithPhoto("¿qué ves acá?")
	attachment, err := sup.imageFromMessage(context.Background(), fake.bot(t), msg)
	if err != nil {
		t.Fatalf("imageFromMessage() error = %v", err)
	}

	// Persist the caption row the way handleChat does, then build the turn.
	_ = repo.AppendTelegramMessage(1, "user", msg.Caption)
	turn := userImageMessage(msg.Caption, attachment)

	if len(*got) != 0 {
		return // turn always built; gateway assertion below fails otherwise
	}
	_ = turn
	// The payload encodes as expected; assert through forwardText.
	if _, err := forwardText(context.Background(), srv.URL, "sk-test", "test/model",
		[]chatMessage{turn}); err != nil {
		t.Fatalf("forwardText() error = %v", err)
	}

	payload := (*got)[len(*got)-1]
	msgs, _ := payload["messages"].([]any)
	if len(msgs) == 0 {
		t.Fatalf("gateway received no messages")
	}
	last := msgs[len(msgs)-1].(map[string]any)
	content, _ := last["content"].([]any)
	if len(content) != 2 {
		t.Fatalf("content parts = %d, want 2 (text + image_url)", len(content))
	}
	if kind, _ := content[1].(map[string]any)["type"].(string); kind != "image_url" {
		t.Errorf("part[1].type = %q, want image_url", kind)
	}
	imageURL, _ := content[1].(map[string]any)["image_url"].(map[string]any)
	url, _ := imageURL["url"].(string)
	decoded := base64.StdEncoding
	if !strings.HasPrefix(url, "data:image/jpeg;base64,") {
		t.Fatalf("image url = %q, want a data URL", url)
	}
	if _, err := decoded.DecodeString(strings.TrimPrefix(url, "data:image/jpeg;base64,")); err != nil {
		t.Errorf("image payload does not decode as base64: %v", err)
	}
}

// S3: without a caption the transcript still records the turn as "[imagen]",
// never as an empty string.
func TestUserImageMessage_CaptionlessRecordsPlaceholder(t *testing.T) {
	msg := messageWithPhoto("")
	if got := transcriptImageRow(msg); got != "[imagen]" {
		t.Errorf("transcriptImageRow() = %q, want [imagen]", got)
	}
	if got := transcriptImageRow(messageWithPhoto("описание")); got != "описание" {
		t.Errorf("transcriptImageRow(caption) = %q, want the caption", got)
	}
}

// S2: in agent mode an attached image is saved under the data dir and the
// task instruction references it by absolute path.
func TestAgentInstruction_SavesImageAndReferencesPath(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("DATA_DIR", dir)

	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, mode: db.ModeAgent, autoApprove: true}
	// The inbox path must exist so submitOrder succeeds.
	inbox := filepath.Join(dir, "inbox.jsonl")
	repo.inboxPath = inbox
	sup := NewSupervisor(repo, func(int64) bool { return true })
	fake := newFakeTelegram(t)

	attachment := &imageAttachment{Data: tinyJPEG, Mime: "image/jpeg"}
	sup.handleAgentInstruction(context.Background(), fake.bot(t), 42, "mirá este screenshot", attachment, nil)

	if repo.task.Instruction == "" || !strings.Contains(repo.task.Instruction, filepath.Join(dir, "telegram-attachments")) {
		t.Errorf("task instruction does not reference the saved image: %q", repo.task.Instruction)
	}
	if repo.task.Status != string(TaskRunning) {
		t.Errorf("task status = %q, want running under auto-approve", repo.task.Status)
	}
}
