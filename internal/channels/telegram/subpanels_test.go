package telegram

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"unicode/utf8"

	tgbotmodels "github.com/go-telegram/bot/models"

	"nox9go/proxy/internal/db"
)

// inlineButtonView is one inline button as the operator receives it.
type inlineButtonView struct {
	Text string
	Data string
}

// inlineOf returns the inline keyboard carried by the message at index i, and
// reports whether that message carried one at all.
func inlineOf(t *testing.T, f *fakeTelegram, i int) ([]inlineButtonView, bool) {
	t.Helper()
	markups := f.replyMarkups()
	if i < 0 || i >= len(markups) || markups[i] == "" {
		return nil, false
	}
	var kb tgbotmodels.InlineKeyboardMarkup
	if err := json.Unmarshal([]byte(markups[i]), &kb); err != nil {
		// A reply keyboard is not an inline one, which is a legitimate answer
		// here rather than a failure.
		return nil, false
	}
	var out []inlineButtonView
	for _, row := range kb.InlineKeyboard {
		for _, button := range row {
			out = append(out, inlineButtonView{Text: button.Text, Data: button.CallbackData})
		}
	}
	return out, len(out) > 0
}

// lastInline returns the newest message that carried an inline keyboard,
// together with its buttons.
func lastInline(t *testing.T, f *fakeTelegram) (string, []inlineButtonView) {
	t.Helper()
	sent := f.messages()
	for i := len(sent) - 1; i >= 0; i-- {
		if buttons, ok := inlineOf(t, f, i); ok {
			return sent[i], buttons
		}
	}
	t.Fatalf("no message carried an inline keyboard; sent = %v", sent)
	return "", nil
}

// pressCallback delivers one inline button the way Telegram does, from an
// authorized operator on a message in their own chat.
func pressCallback(t *testing.T, sup *Supervisor, fake *fakeTelegram, userID int64, data string) {
	t.Helper()
	sup.handleCallbackQuery(context.Background(), fake.bot(t), &tgbotmodels.CallbackQuery{
		ID:   "cb-1",
		From: tgbotmodels.User{ID: userID},
		Message: tgbotmodels.MaybeInaccessibleMessage{
			Message: &tgbotmodels.Message{ID: 1, Chat: tgbotmodels.Chat{ID: 42}},
		},
		Data: data,
	})
}

// fakeModelsGateway serves the dashboard models route /model reads, so the
// subpanel is proven to read the same list instead of a second copy of it.
func fakeModelsGateway(t *testing.T, ids []string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasSuffix(r.URL.Path, "/api/channels/telegram/models") {
			t.Errorf("path = %q, want the dashboard models route", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"models": ids})
	}))
	t.Cleanup(srv.Close)
	return srv
}

// containsMessage reports whether any message the bot sent contains want. A
// press sends its answer and then the dock, so the assertion is about the
// answer existing, not about it being the last thing in the chat.
func containsMessage(sent []string, want string) bool {
	for _, message := range sent {
		if strings.Contains(message, want) {
			return true
		}
	}
	return false
}

// featureCheckout builds a checkout carrying two ODD documents, the same source
// /estado reads.
func featureCheckout(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	gitRun(t, dir, "init", "-q")
	gitRun(t, dir, "config", "user.email", "test@example.com")
	gitRun(t, dir, "config", "user.name", "test")
	writeAt(t, dir, "odd/tasks/alpha.md", "# Alpha\n")
	writeAt(t, dir, "odd/tasks/beta.md", "# Beta\n")
	gitRun(t, dir, "add", "-A")
	gitRun(t, dir, "commit", "-q", "-m", "docs: features")
	return dir
}

// S4: the 🧠 Modelos IA button draws the gateway's own model list, and a tap
// switches the model, redrawing the panel in place.
func TestModelsSubpanel_ListsAndSwitches(t *testing.T) {
	gw := fakeModelsGateway(t, []string{"ag/gemini-3.8-flash-low", "ag/gpt-6-luna"})

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, model: "ag/gemini-3.8-flash-low"}
	sup := NewSupervisor(repo, func(int64) bool { return true })
	sup.SetGateway(gw.URL, "cli")

	// The panel is opened first so the model change has a message to redraw.
	runCommand(t, sup, fake, "/panel")
	pressDock(t, sup, fake, dockChatModels)

	text, buttons := lastInline(t, fake)
	if !strings.Contains(text, "ag/gemini-3.8-flash-low") {
		t.Errorf("subpanel text = %q, want the current model named", text)
	}
	var wantData []string
	for _, id := range []string{"ag/gemini-3.8-flash-low", "ag/gpt-6-luna"} {
		wantData = append(wantData, cbModelPick+id)
	}
	gotData := make([]string, 0, len(buttons))
	for _, b := range buttons {
		gotData = append(gotData, b.Data)
	}
	if strings.Join(gotData, ",") != strings.Join(wantData, ",") {
		t.Fatalf("subpanel buttons = %v, want one per model %v", gotData, wantData)
	}

	pressCallback(t, sup, fake, 42, cbModelPick+"ag/gpt-6-luna")

	if repo.model != "ag/gpt-6-luna" {
		t.Fatalf("stored model = %q, want the tapped one", repo.model)
	}
	if sent := fake.messages(); !containsMessage(sent, "Modelo cambiado") {
		t.Errorf("sent = %q, want the model confirmation", sent)
	}
	edits := fake.editedMessages()
	if len(edits) == 0 || !strings.Contains(edits[len(edits)-1], "ag/gpt-6-luna") {
		t.Errorf("panel edits = %v, want the panel redrawn with the new model", edits)
	}
	// S3: the press leaves the dock on screen.
	assertRows(t, dockLabelsOf(t, lastReplyMarkup(t, fake)), dockRows(db.ModeChat))
}

// S4/S9: Telegram rejects callback data over 64 bytes, which would make the
// button dead. A long model id is truncated inside the payload and still maps
// back to exactly the model it names.
func TestModelCallbackPayload_TruncatesAndMapsBack(t *testing.T) {
	longID := "vendor/" + strings.Repeat("verylongmodelname", 8)

	payload := truncateBytes(longID, maxCallbackData-len(cbModelPick))
	data := cbModelPick + payload
	if len(data) > maxCallbackData {
		t.Fatalf("callback_data is %d bytes, limit %d", len(data), maxCallbackData)
	}
	if payload == longID {
		t.Fatalf("the long id was not truncated, so the test proves nothing")
	}

	got, ok := matchCallbackValue([]string{"other/model", longID}, payload)
	if !ok || got != longID {
		t.Errorf("matchCallbackValue() = (%q, %v), want the long id", got, ok)
	}
}

// S4/S9: map-back must never guess. An ambiguous prefix is refused, and an exact
// id wins over a longer id that starts with it.
func TestMatchCallbackValue(t *testing.T) {
	tests := []struct {
		name    string
		values  []string
		payload string
		want    string
		wantOK  bool
	}{
		{name: "exact id", values: []string{"ag/a", "ag/b"}, payload: "ag/b", want: "ag/b", wantOK: true},
		{name: "truncated id", values: []string{"vendor/model-a"}, payload: "vendor/mod", want: "vendor/model-a", wantOK: true},
		{name: "exact beats prefix", values: []string{"ag/model", "ag/model-long"}, payload: "ag/model", want: "ag/model", wantOK: true},
		{name: "ambiguous prefix", values: []string{"ag/a", "ag/b"}, payload: "ag/", wantOK: false},
		{name: "no match", values: []string{"ag/a"}, payload: "cx/", wantOK: false},
		{name: "empty payload", values: []string{"ag/a"}, payload: "", wantOK: false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := matchCallbackValue(tt.values, tt.payload)
			if ok != tt.wantOK {
				t.Fatalf("matchCallbackValue(%v, %q) ok = %v, want %v", tt.values, tt.payload, ok, tt.wantOK)
			}
			if ok && got != tt.want {
				t.Errorf("matchCallbackValue() = %q, want %q", got, tt.want)
			}
		})
	}
}

// S5: the sessions subpanel lists what /sessions lists, and a tap retakes the
// session it names through the storage helper that keeps one session open.
func TestSessionsSubpanel_Retakes(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{
		token:   "111:secret",
		allowed: []int64{42},
		sessions: []db.TelegramSession{
			{ID: 7, ChatID: 42, Title: "chat", CreatedAt: "2026-11-22 10:00:00"},
			{ID: 5, ChatID: 42, Title: "chat", CreatedAt: "2026-11-21 10:00:00", ArchivedAt: "2026-11-21 11:00:00"},
		},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressDock(t, sup, fake, dockChatSessions)

	_, buttons := lastInline(t, fake)
	if len(buttons) != 2 {
		t.Fatalf("subpanel buttons = %v, want one per stored session", buttons)
	}
	if buttons[1].Data != cbSessionPick+"5" {
		t.Errorf("second button = %q, want the archived session 5", buttons[1].Data)
	}

	pressCallback(t, sup, fake, 42, cbSessionPick+"5")

	if repo.resumed != 5 {
		t.Fatalf("resumed session = %d, want 5", repo.resumed)
	}
	sent := fake.messages()
	if !containsMessage(sent, "retomada") {
		t.Errorf("sent = %q, want the retake confirmation", sent)
	}
}

// S5/S9: a callback carries only a number, so a crafted payload must not reach
// another chat's session. The ownership check runs against the chat's own list.
func TestSessionsSubpanel_RefusesForeignSession(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{
		token:    "111:secret",
		allowed:  []int64{42},
		sessions: []db.TelegramSession{{ID: 7, ChatID: 42, CreatedAt: "2026-11-22 10:00:00"}},
	}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressCallback(t, sup, fake, 42, cbSessionPick+"99")

	if repo.resumed != 0 {
		t.Errorf("resumed session = %d, want nothing resumed for a foreign id", repo.resumed)
	}
	sent := fake.messages()
	if len(sent) == 0 || !strings.Contains(strings.Join(sent, "\n"), "no es de este chat") {
		t.Errorf("sent = %q, want a refusal naming the ownership check", sent)
	}
}

// S6: the 🎯 Feature ODD button lists the documents under odd/tasks, and a tap
// renders that feature's devstatus through the existing command.
func TestFeatureSubpanel_ListAndRender(t *testing.T) {
	dir := featureCheckout(t)

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, repoRoot: dir}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressDock(t, sup, fake, dockAgentFeature)

	_, buttons := lastInline(t, fake)
	if len(buttons) != 2 || buttons[0].Text != "alpha" || buttons[1].Text != "beta" {
		t.Fatalf("feature buttons = %v, want alpha and beta", buttons)
	}
	if buttons[0].Data != cbFeaturePick+"alpha" {
		t.Errorf("feature payload = %q, want the prefixed name", buttons[0].Data)
	}

	pressCallback(t, sup, fake, 42, cbFeaturePick+"alpha")

	sent := fake.messages()
	if !strings.Contains(strings.Join(sent, "\n"), "<b>alpha</b>") {
		t.Errorf("sent = %q, want the alpha report rendered", sent)
	}
}

// S6/L0b: with a stored feature configured the button answers in one tap with
// that feature instead of asking which one.
func TestFeatureSubpanel_UsesStoredFeature(t *testing.T) {
	dir := featureCheckout(t)

	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, repoRoot: dir, feature: "beta"}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressDock(t, sup, fake, dockAgentFeature)

	sent := fake.messages()
	joined := strings.Join(sent, "\n")
	if !strings.Contains(joined, "<b>beta</b>") {
		t.Errorf("sent = %q, want the stored feature rendered", sent)
	}
	// A picker would be a second question the configuration already answered.
	for i := range sent {
		if _, ok := inlineOf(t, fake, i); ok {
			t.Errorf("message %d carries a picker: %q", i, sent[i])
		}
	}
}

// S6/L0b: with nothing to pick the button names the command that does the job
// instead of opening an empty keyboard.
func TestFeatureSubpanel_NoFeatures(t *testing.T) {
	fake := newFakeTelegram(t)
	repo := &fakeRepo{token: "111:secret", allowed: []int64{42}, repoRoot: t.TempDir()}
	sup := NewSupervisor(repo, func(int64) bool { return true })

	pressDock(t, sup, fake, dockAgentFeature)

	sent := fake.messages()
	if len(sent) < 2 || sent[0] != featureGuidance {
		t.Fatalf("sent = %q, want the feature guidance first", sent)
	}
}

// S9: every new callback passes the same allowlist gate as text. A stranger's
// press changes nothing and draws no response.
func TestNewCallbacks_RefuseNonAllowed(t *testing.T) {
	data := []string{
		cbModelsPanel,
		cbSessionsPanel,
		cbFeaturesPanel,
		cbModelPick + "ag/model",
		cbSessionPick + "7",
		cbFeaturePick + "alpha",
	}

	for _, value := range data {
		t.Run(value, func(t *testing.T) {
			gw := fakeModelsGateway(t, []string{"ag/model"})
			fake := newFakeTelegram(t)
			repo := &fakeRepo{
				token:    "111:secret",
				allowed:  []int64{42},
				repoRoot: featureCheckout(t),
				sessions: []db.TelegramSession{{ID: 7, ChatID: 42}},
			}
			sup := NewSupervisor(repo, func(id int64) bool { return id == 42 })
			sup.SetGateway(gw.URL, "cli")

			pressCallback(t, sup, fake, 999, value)

			if sent := fake.messages(); len(sent) != 0 {
				t.Errorf("answered a stranger's callback %q: %v", value, sent)
			}
			if repo.model != "" || repo.resumed != 0 || repo.mode == db.ModeAgent {
				t.Errorf("state changed by an unauthorized callback %q", value)
			}
		})
	}
}

// S9: every value this package can put in callback_data fits Telegram's hard
// limit, including the ones assembled from a live id or name.
func TestNewCallbackDataFitsLimit(t *testing.T) {
	values := []string{
		cbModelsPanel, cbSessionsPanel, cbFeaturesPanel, cbModelPick, cbSessionPick, cbFeaturePick,
	}
	// The assembled ones, at their worst case: the longest id and name the
	// payload budget allows.
	values = append(values,
		cbModelPick+truncateBytes(strings.Repeat("m", 200), maxCallbackData-len(cbModelPick)),
		cbFeaturePick+truncateBytes(strings.Repeat("f", 200), maxCallbackData-len(cbFeaturePick)),
		cbSessionPick+"9223372036854775807",
	)

	for _, value := range values {
		if len(value) > maxCallbackData {
			t.Errorf("callback_data %q is %d bytes, limit %d", value, len(value), maxCallbackData)
		}
		if !strings.HasPrefix(value, cbPrefix) {
			t.Errorf("callback_data %q is outside the panel namespace", value)
		}
	}
}

// S4/S9: truncation never splits a UTF-8 sequence, which would make the data
// unusable rather than merely short.
func TestTruncateBytes_KeepsValidUTF8(t *testing.T) {
	tests := []struct {
		name  string
		value string
		max   int
		want  string
	}{
		{name: "shorter than the budget", value: "ag/model", max: 20, want: "ag/model"},
		{name: "ascii cut", value: "abcdefgh", max: 4, want: "abcd"},
		{name: "cut inside a rune", value: "año", max: 2, want: "a"},
		{name: "whole runes only", value: "año", max: 3, want: "añ"},
		{name: "zero budget", value: "ñ", max: 0, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := truncateBytes(tt.value, tt.max)
			if got != tt.want {
				t.Errorf("truncateBytes(%q, %d) = %q, want %q", tt.value, tt.max, got, tt.want)
			}
			if !utf8.ValidString(got) && got != "" {
				t.Errorf("truncateBytes(%q, %d) = %q, want valid UTF-8", tt.value, tt.max, got)
			}
			if len(got) > tt.max {
				t.Errorf("truncateBytes(%q, %d) = %q is over the budget", tt.value, tt.max, got)
			}
		})
	}
}
