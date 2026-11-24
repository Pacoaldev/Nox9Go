package telegram

import (
	"bytes"
	"context"
	"encoding/base64"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"nox9go/proxy/internal/config"

	tgbot "github.com/go-telegram/bot"
	"github.com/go-telegram/bot/models"
)

// maxTelegramImage is the channel's own cap on an attachment. Telegram photos
// are recompressed server-side and stay well below it; an image document above
// the cap is refused honestly instead of being forwarded blind.
const maxTelegramImage = 5 << 20

// imageAttachment is one photo the operator sent with a message.
type imageAttachment struct {
	Data []byte
	Mime string
}

// telegramAPIBase is the public Bot API origin. GetFile returns a relative
// file path; the bytes live at <base>/file/bot<token>/<path>. It is a var so
// tests aim the supervisor at a fake Bot API origin.
var telegramAPIBase = "https://api.telegram.org"

// setBotAPIBase overrides the Bot API origin. It exists for tests that stand
// up a fake Telegram server.
func setBotAPIBase(_ *Supervisor, base string) {
	telegramAPIBase = strings.TrimRight(base, "/")
}

// imageFromMessage extracts the photo attached to a message, if any. It picks
// the largest PhotoSize in the array (Telegram sends the same photo in
// several sizes) and accepts image documents below the size cap. A message
// with no image returns nil with no error.
func (s *Supervisor) imageFromMessage(ctx context.Context, bot *tgbot.Bot, msg *models.Message) (*imageAttachment, error) {
	fileID, mime, size := pickImageFile(msg)
	if fileID == "" {
		return nil, nil
	}
	if size > maxTelegramImage {
		return nil, fmt.Errorf("la imagen pesa %d MB y el límite es %d MB", size>>20, maxTelegramImage>>20)
	}

	file, err := bot.GetFile(ctx, &tgbot.GetFileParams{FileID: fileID})
	if err != nil {
		return nil, fmt.Errorf("telegram: get file: %w", err)
	}
	if file.FilePath == "" {
		return nil, fmt.Errorf("telegram: file %s has no path", fileID)
	}

	token, err := s.repo.TelegramToken()
	if err != nil {
		return nil, err
	}

	url := telegramAPIBase + "/file/bot" + token + "/" + file.FilePath
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, fmt.Errorf("telegram: build download: %w", err)
	}
	resp, err := (&http.Client{
		Timeout:   2 * time.Minute,
		Transport: telegramAPITransport(),
	}).Do(req)
	if err != nil {
		return nil, fmt.Errorf("telegram: download image: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("telegram: download image: status %d", resp.StatusCode)
	}

	data, err := io.ReadAll(io.LimitReader(resp.Body, maxTelegramImage+1))
	if err != nil {
		return nil, fmt.Errorf("telegram: read image: %w", err)
	}
	if len(data) > maxTelegramImage {
		return nil, fmt.Errorf("la imagen supera el límite de %d MB", maxTelegramImage>>20)
	}
	if mime == "" {
		mime = http.DetectContentType(data)
	}
	return &imageAttachment{Data: data, Mime: mime}, nil
}

// pickImageFile resolves which file id carries the image. Photos win over
// documents; the largest PhotoSize is chosen because the smaller entries are
// the same picture recompressed.
func pickImageFile(msg *models.Message) (fileID, mime string, size int64) {
	if len(msg.Photo) > 0 {
		photo := msg.Photo[0]
		for _, p := range msg.Photo {
			if p.Width*p.Height > photo.Width*photo.Height || p.FileSize > photo.FileSize {
				photo = p
			}
		}
		return photo.FileID, "image/jpeg", int64(photo.FileSize)
	}
	if doc := msg.Document; doc != nil {
		if isImageMime(doc.MimeType) {
			return doc.FileID, doc.MimeType, doc.FileSize
		}
	}
	return "", "", 0
}

// isImageDocument reports whether the message carries an image sent as a
// document (an "uncompressed" photo), the same input pickImageFile accepts.
func isImageDocument(msg *models.Message) bool {
	return msg.Document != nil && isImageMime(msg.Document.MimeType)
}

func isImageMime(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "image/"
}

// transcriptImageRow is what the transcript stores for a photo message: the
// caption when it has one, an explicit placeholder when it does not. An empty
// row would look like an empty user turn the next time history replays it.
func transcriptImageRow(msg *models.Message) string {
	if caption := strings.TrimSpace(msg.Caption); caption != "" {
		return caption
	}
	return "[imagen]"
}

// dataURL encodes the attachment the way an OpenAI image_url part expects it.
func (a *imageAttachment) dataURL() string {
	return "data:" + a.Mime + ";base64," + base64.StdEncoding.EncodeToString(a.Data)
}

// saveAgentAttachment writes an agent-mode image under the gateway data dir
// and returns the absolute path, so the development session can open it. The
// directory is created on demand; an existing file with the same name is
// replaced, never renamed.
func (s *Supervisor) saveAgentAttachment(a *imageAttachment) (string, error) {
	base := config.ResolveDataDir()
	dir := filepath.Join(base, "telegram-attachments")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", fmt.Errorf("telegram: create attachments dir: %w", err)
	}
	ext := extensionForMime(a.Mime)
	path := filepath.Join(dir, "img-"+time.Now().Format("20060102-150405")+ext)
	if err := os.WriteFile(path, a.Data, 0o644); err != nil {
		return "", fmt.Errorf("telegram: save attachment: %w", err)
	}
	return path, nil
}

// extensionForMime maps the mime types Telegram serves to a file extension.
func (s *Supervisor) sendAgentImageConfirmation(ctx context.Context, bot *tgbot.Bot, chatID int64, path string, a *imageAttachment) {
	caption := "Imagen guardada.\n" + path
	file := &models.InputFileUpload{Filename: filepath.Base(path), Data: bytes.NewReader(a.Data)}
	if _, err := bot.SendPhoto(ctx, &tgbot.SendPhotoParams{
		ChatID:  chatID,
		Photo:   file,
		Caption: caption,
	}); err != nil {
		s.setError(fmt.Errorf("telegram: send photo ack: %w", err))
		s.send(ctx, bot, chatID, caption)
	}
}

func extensionForMime(mime string) string {
	switch mime {
	case "image/png":
		return ".png"
	case "image/webp":
		return ".webp"
	default:
		return ".jpg"
	}
}
