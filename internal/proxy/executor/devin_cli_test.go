package executor

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// The tests in this file never reach the real Devin Cloud. The fake `devin`
// CLI is this test binary itself, re-entered through TestMain when the
// NOX9GO_DEVIN_FAKE environment variable is set: the executor's subprocess
// lookup is pointed at os.Executable(), so `devin acp --cloud` and
// `devin auth status` run the fake agent below instead of the real CLI.

const devinFakeEnv = "NOX9GO_DEVIN_FAKE"

func TestMain(m *testing.M) {
	if mode := os.Getenv(devinFakeEnv); mode != "" && len(os.Args) > 1 {
		switch os.Args[1] {
		case "auth":
			// The fake status output deliberately carries an account email:
			// the executor must judge the credential by exit code alone and
			// never echo this text into an error.
			if mode == "authfail" {
				fmt.Println("signed in as devin-user@example.com (session a1b2c3d4)")
				os.Exit(1)
			}
			os.Exit(0)
		case "acp":
			os.Exit(runDevinFakeACP(mode))
		case "models":
			// The account catalog the bridge consults before forwarding
			// --model. Mode "nomodel" serves a list without the requested id.
			// The header line mirrors `devin models`' family syntax, and the
			// ids are hyphenated exactly as the CLI spells them.
			fmt.Println("Available models (2 families)")
			if mode != "nomodel" {
				fmt.Println("  gpt-5.4-high                                 GPT-5.4 High")
			}
			fmt.Println("SWE-1.6 (swe-1.6)")
			fmt.Println("  swe-1-6                                      SWE-1.6")
			fmt.Println("  swe-2-flash-medium                           SWE-2 Flash Medium")
			os.Exit(0)
		}
	}
	os.Exit(m.Run())
}

// startFakeDevin points the executor's executable lookup at this test binary
// and arms the fake-agent mode for the subprocesses it spawns.
func startFakeDevin(t *testing.T, mode string) {
	t.Helper()
	t.Setenv(devinFakeEnv, mode)
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	prev := devinCLIPath
	devinCLIPath = func() (string, error) { return exe, nil }
	resetDevinCaches()
	t.Cleanup(func() {
		devinCLIPath = prev
		resetDevinCaches()
	})
}

// resetDevinCaches clears the cached auth and model lookups so one test never
// inherits another's verdict or catalog.
func resetDevinCaches() {
	devinAuthCache.mu.Lock()
	devinAuthCache.path = ""
	devinAuthCache.okUntil = time.Time{}
	devinAuthCache.mu.Unlock()

	devinModelsCache.mu.Lock()
	devinModelsCache.path = ""
	devinModelsCache.ids = nil
	devinModelsCache.okUntil = time.Time{}
	devinModelsCache.mu.Unlock()
}

// runDevinFakeACP impersonates `devin acp --cloud`: an ACP agent speaking
// newline-delimited JSON-RPC 2.0 over stdio. Every request shape is checked,
// because the executor is what the bridge contract is tested against; a
// violation makes the fake print a plain-text reason to stdout and exit 3,
// which the executor must surface as an error rather than a completion.
func runDevinFakeACP(mode string) int {
	const sessionID = "sess_fake_1"
	// The bridge forwards --model only when the account catalog lists the id.
	// A fake agent that ignores argv would let a regression through silently.
	var gotModel string
	for i, arg := range os.Args {
		if arg == "--model" && i+1 < len(os.Args) {
			gotModel = os.Args[i+1]
		}
	}
	switch mode {
	case "ok":
		if gotModel != "gpt-5.4-high" {
			fmt.Println("fake-devin: expected --model gpt-5.4-high, got " + gotModel)
			return 3
		}
	case "okdot":
		// The dashboard spells it with a dot; the CLI spells it with a dash.
		// Normalization must bridge the two, and the requested spelling is
		// what gets forwarded.
		if gotModel != "swe-1.6" {
			fmt.Println("fake-devin: expected --model swe-1.6, got " + gotModel)
			return 3
		}
	case "nomodel":
		if gotModel != "" {
			fmt.Println("fake-devin: model is not in the catalog but was forwarded: " + gotModel)
			return 3
		}
	}
	reader := bufio.NewScanner(os.Stdin)
	reader.Buffer(make([]byte, 0, 64*1024), 1<<20)
	enc := json.NewEncoder(os.Stdout)

	for reader.Scan() {
		line := bytes.TrimSpace(reader.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      *int            `json:"id"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params"`
		}
		if err := json.Unmarshal(line, &msg); err != nil || msg.JSONRPC != "2.0" {
			fmt.Println("fake-devin: unparseable line")
			return 3
		}
		switch msg.Method {
		case "initialize":
			var params struct {
				ProtocolVersion int `json:"protocolVersion"`
			}
			if err := json.Unmarshal(msg.Params, &params); err != nil || params.ProtocolVersion != 1 {
				fmt.Println("fake-devin: initialize without protocolVersion 1")
				return 3
			}
			if mode == "badproto" {
				fmt.Println("this is not json at all")
				return 3
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result":  map[string]any{"protocolVersion": 1, "agentCapabilities": map[string]any{}},
			})
		case "session/new":
			var params struct {
				Cwd string `json:"cwd"`
			}
			if err := json.Unmarshal(msg.Params, &params); err != nil || !filepath.IsAbs(params.Cwd) {
				fmt.Println("fake-devin: session/new without an absolute cwd")
				return 3
			}
			// The gateway must not announce its own checkout as the agent's
			// workspace: the operator's project path is not cloud business.
			if wd, err := os.Getwd(); err == nil && params.Cwd == wd {
				fmt.Println("fake-devin: session/new used the gateway working directory")
				return 3
			}
			if filepath.Base(params.Cwd) != "nox9go-devin-workspace" {
				fmt.Println("fake-devin: session/new cwd is not the dedicated workspace: " + params.Cwd)
				return 3
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result":  map[string]any{"sessionId": sessionID},
			})
		case "session/prompt":
			var params struct {
				SessionID string `json:"sessionId"`
				Prompt    []struct {
					Type string `json:"type"`
					Text string `json:"text"`
				} `json:"prompt"`
			}
			err := json.Unmarshal(msg.Params, &params)
			if err != nil || params.SessionID != sessionID || len(params.Prompt) == 0 ||
				params.Prompt[0].Type != "text" || !strings.Contains(params.Prompt[0].Text, "NOX9GO") {
				fmt.Println("fake-devin: session/prompt missing session id, text block, or prompt marker")
				return 3
			}
			if mode == "perm" {
				// A string id, as a real agent may choose, and a permission
				// prompt the gateway must decline rather than leave hanging.
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"id":      "perm-1",
					"method":  "session/request_permission",
					"params": map[string]any{
						"sessionId": sessionID,
						"toolCall":  map[string]any{"toolCallId": "tc-1", "title": "Run a shell command"},
						"options":   []any{map[string]any{"optionId": "allow", "name": "Allow", "kind": "allow_once"}},
					},
				})
				if code := expectPermissionDenied(reader); code != 0 {
					return code
				}
			}
			if mode == "hang" {
				time.Sleep(15 * time.Second)
			}
			for _, chunk := range []string{"Hello ", "from Devin"} {
				_ = enc.Encode(map[string]any{
					"jsonrpc": "2.0",
					"method":  "session/update",
					"params": map[string]any{
						"sessionId": sessionID,
						"update": map[string]any{
							"sessionUpdate": "agent_message_chunk",
							"content":       map[string]any{"type": "text", "text": chunk},
						},
					},
				})
			}
			_ = enc.Encode(map[string]any{
				"jsonrpc": "2.0",
				"id":      msg.ID,
				"result": map[string]any{
					"stopReason": "end_turn",
					"usage":      map[string]any{"inputTokens": 11, "outputTokens": 7, "totalTokens": 18},
				},
			})
			return 0
		default:
			fmt.Println("fake-devin: unknown method " + msg.Method)
			return 3
		}
	}
	return 0
}

// expectPermissionDenied reads the client's answer to the fake agent's
// session/request_permission and requires the ACP-declined outcome. Anything
// else — silence, an approval, a mismatched id — fails the subprocess.
func expectPermissionDenied(reader *bufio.Scanner) int {
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if !reader.Scan() {
			fmt.Println("fake-devin: client closed stdin without answering the permission request")
			return 3
		}
		line := bytes.TrimSpace(reader.Bytes())
		if len(line) == 0 {
			continue
		}
		var reply struct {
			ID     any `json:"id"`
			Result struct {
				Outcome struct {
					Outcome string `json:"outcome"`
				} `json:"outcome"`
			} `json:"result"`
		}
		if err := json.Unmarshal(line, &reply); err != nil {
			fmt.Println("fake-devin: permission answer was not JSON")
			return 3
		}
		if reply.ID != "perm-1" {
			fmt.Println("fake-devin: permission answer did not echo the agent id")
			return 3
		}
		if reply.Result.Outcome.Outcome != "cancelled" {
			fmt.Println("fake-devin: gateway must decline, got outcome " + reply.Result.Outcome.Outcome)
			return 3
		}
		return 0
	}
	fmt.Println("fake-devin: no permission answer within the deadline")
	return 3
}

func TestForwardDevinCLI_MissingExecutableIsActionable(t *testing.T) {
	prev := devinCLIPath
	devinCLIPath = func() (string, error) {
		return "", errors.New("exec: \"devin\": executable file not found in %PATH%")
	}
	t.Cleanup(func() { devinCLIPath = prev })

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:  context.Background(),
		Body: []byte(`{"model":"gpt-5.4-high","messages":[{"role":"user","content":"hi NOX9GO"}]}`),
	})
	if err == nil {
		t.Fatal("expected an error for a missing executable")
	}
	msg := err.Error()
	if !strings.Contains(msg, "not found on PATH") {
		t.Errorf("error should name the PATH problem, got %q", msg)
	}
	if !strings.Contains(msg, "devin auth login") {
		t.Errorf("error should be actionable (devin auth login), got %q", msg)
	}
}

func TestForwardDevinCLI_AuthStatusFailsWithoutEchoingOutput(t *testing.T) {
	startFakeDevin(t, "authfail")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:  context.Background(),
		Body: []byte(`{"model":"gpt-5.4-high","messages":[{"role":"user","content":"hi NOX9GO"}]}`),
	})
	if err == nil {
		t.Fatal("expected an error when 'devin auth status' exits non-zero")
	}
	msg := err.Error()
	if !strings.Contains(msg, "devin auth login") {
		t.Errorf("error should advise 'devin auth login', got %q", msg)
	}
	if !strings.Contains(msg, "1") {
		t.Errorf("error should report the exit status, got %q", msg)
	}
	// The fake's stdout carries an account email. Exit-code-only parsing means
	// none of it may reach the client.
	if strings.Contains(msg, "example.com") || strings.Contains(msg, "@") {
		t.Errorf("error leaked the CLI's output: %q", msg)
	}
	if rec.Body.Len() != 0 {
		t.Errorf("no body should be written on the failure path, got %q", rec.Body.String())
	}
}

func TestForwardDevinCLI_Completion_NonStream(t *testing.T) {
	startFakeDevin(t, "ok")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:         context.Background(),
		Body:        []byte(`{"model":"gpt-5.4-high","messages":[{"role":"system","content":"Be brief."},{"role":"user","content":"Say hi to NOX9GO"}]}`),
		IsStream:    false,
		ResponseBuf: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("ForwardDevinCLI: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	var completion struct {
		Model   string `json:"model"`
		Choices []struct {
			Message struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"message"`
			FinishReason string `json:"finish_reason"`
		} `json:"choices"`
		Usage struct {
			PromptTokens     int `json:"prompt_tokens"`
			CompletionTokens int `json:"completion_tokens"`
			TotalTokens      int `json:"total_tokens"`
		} `json:"usage"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &completion); err != nil {
		t.Fatalf("parse completion: %v; body: %s", err, rec.Body.String())
	}
	if len(completion.Choices) != 1 {
		t.Fatalf("choices = %d, want 1", len(completion.Choices))
	}
	choice := completion.Choices[0]
	if choice.Message.Content != "Hello from Devin" {
		t.Errorf("content = %q, want %q", choice.Message.Content, "Hello from Devin")
	}
	if choice.Message.Role != "assistant" {
		t.Errorf("role = %q, want assistant", choice.Message.Role)
	}
	if choice.FinishReason != "stop" {
		t.Errorf("finish_reason = %q, want stop", choice.FinishReason)
	}
	if completion.Model != "gpt-5.4-high" {
		t.Errorf("model = %q, want the requested model", completion.Model)
	}
	// The agent's own accounting must win over the length estimate: a client
	// reading usage pays for what Devin billed.
	if completion.Usage.PromptTokens != 11 || completion.Usage.CompletionTokens != 7 || completion.Usage.TotalTokens != 18 {
		t.Errorf("usage = %+v, want the agent-reported 11/7/18", completion.Usage)
	}
}

// The dashboard catalog spells ids with dots (`swe-1.6`); the CLI spells them
// with dashes (`swe-1-6`). The bridge must recognise both and forward the id
// the client actually asked for.
func TestForwardDevinCLI_DotSpellingMatchesCLIId(t *testing.T) {
	startFakeDevin(t, "okdot")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:         context.Background(),
		Body:        []byte(`{"model":"swe-1.6","messages":[{"role":"user","content":"Say hi to NOX9GO"}]}`),
		ResponseBuf: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("ForwardDevinCLI: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "Hello from Devin") {
		t.Errorf("completion lost the agent text: %s", rec.Body.String())
	}
}

// A model the account catalog does not list must not reach the CLI as --model:
// the CLI rejects unknown ids, so the agent keeps its default and the request
// still succeeds.
func TestForwardDevinCLI_UnlistedModelOmitsModelFlag(t *testing.T) {
	startFakeDevin(t, "nomodel")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:         context.Background(),
		Body:        []byte(`{"model":"gpt-5.5-xhigh","messages":[{"role":"user","content":"Say hi to NOX9GO"}]}`),
		ResponseBuf: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("ForwardDevinCLI: %v", err)
	}
	if !strings.Contains(rec.Body.String(), "Hello from Devin") {
		t.Errorf("completion lost the agent text: %s", rec.Body.String())
	}
}

func TestForwardDevinCLI_Completion_Stream(t *testing.T) {
	startFakeDevin(t, "ok")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:         context.Background(),
		Body:        []byte(`{"model":"gpt-5.4-high","stream":true,"messages":[{"role":"user","content":"Say hi to NOX9GO"}]}`),
		IsStream:    true,
		ResponseBuf: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("ForwardDevinCLI: %v", err)
	}
	if ct := rec.Header().Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}

	type chunk struct {
		Object  string `json:"object"`
		Choices []struct {
			Delta struct {
				Role    string `json:"role"`
				Content string `json:"content"`
			} `json:"delta"`
			FinishReason *string `json:"finish_reason"`
		} `json:"choices"`
	}
	var contents []string
	var role, finish string
	done := false
	for _, raw := range sseFrames(t, rec.Body.String()) {
		payload := strings.TrimSpace(strings.TrimPrefix(raw, "data:"))
		if payload == "[DONE]" {
			done = true
			continue
		}
		var c chunk
		if err := json.Unmarshal([]byte(payload), &c); err != nil {
			t.Fatalf("parse chunk %q: %v", payload, err)
		}
		if len(c.Choices) != 1 || c.Object != "chat.completion.chunk" {
			t.Fatalf("malformed chunk %q", payload)
		}
		if c.Choices[0].Delta.Role != "" {
			role = c.Choices[0].Delta.Role
		}
		if c.Choices[0].Delta.Content != "" {
			contents = append(contents, c.Choices[0].Delta.Content)
		}
		if c.Choices[0].FinishReason != nil {
			finish = *c.Choices[0].FinishReason
		}
	}
	if role != "assistant" {
		t.Errorf("first delta should carry the assistant role, got %q", role)
	}
	if strings.Join(contents, "") != "Hello from Devin" {
		t.Errorf("streamed content = %q, want %q", strings.Join(contents, ""), "Hello from Devin")
	}
	if finish != "stop" {
		t.Errorf("finish_reason = %q, want stop", finish)
	}
	if !done {
		t.Error("stream must end with a [DONE] frame")
	}
}

// TestForwardDevinCLI_LiveLocalAgent is the opt-in proof that the bridge works
// against the real CLI: it spawns the installed `devin acp`, runs one turn on
// the account's free model and requires the agent's text. It is skipped unless
// NOX9GO_LIVE_TESTS=1, so the standard suite never needs a Devin account.
func TestForwardDevinCLI_LiveLocalAgent(t *testing.T) {
	if os.Getenv("NOX9GO_LIVE_TESTS") != "1" {
		t.Skip("set NOX9GO_LIVE_TESTS=1 to run against the installed Devin CLI")
	}
	devinCLIPath = devinCLIDefaultPath
	resetDevinCaches()
	t.Cleanup(resetDevinCaches)
	t.Setenv(devinModeEnv, "")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:         context.Background(),
		Body:        []byte(`{"model":"swe-2-flash-medium","messages":[{"role":"user","content":"Reply with exactly one word: PONG"}]}`),
		ResponseBuf: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("live turn: %v", err)
	}
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(strings.ToUpper(rec.Body.String()), "PONG") {
		t.Errorf("agent did not answer PONG: %s", rec.Body.String())
	}
}

func TestForwardDevinCLI_PermissionRequestIsDeclinedNotHung(t *testing.T) {
	startFakeDevin(t, "perm")

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:         context.Background(),
		Body:        []byte(`{"model":"gpt-5.4-high","messages":[{"role":"user","content":"Say hi to NOX9GO"}]}`),
		IsStream:    false,
		ResponseBuf: &bytes.Buffer{},
	})
	if err != nil {
		t.Fatalf("ForwardDevinCLI: %v", err)
	}
	// The fake exits 3 when the gateway fails to decline the permission
	// request; reaching a completion proves the answer was the declined one.
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if !strings.Contains(rec.Body.String(), "Hello from Devin") {
		t.Errorf("completion lost the agent text: %s", rec.Body.String())
	}
}

func TestForwardDevinCLI_ContextCancelTerminatesProcess(t *testing.T) {
	startFakeDevin(t, "hang")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	time.AfterFunc(300*time.Millisecond, cancel)
	start := time.Now()

	rec := httptest.NewRecorder()
	err := ForwardDevinCLI(rec, &Request{
		Ctx:      ctx,
		Body:     []byte(`{"model":"gpt-5.4-high","messages":[{"role":"user","content":"hi NOX9GO"}]}`),
		IsStream: false,
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected an error after cancellation")
	}
	if elapsed > 10*time.Second {
		t.Fatalf("cancellation took %v; the subprocess was not terminated", elapsed)
	}
	if !errors.Is(err, context.Canceled) {
		t.Errorf("error should wrap the cancellation, got %v", err)
	}
}

// sseFrames extracts the `data:` lines of an SSE body.
func sseFrames(t *testing.T, body string) []string {
	t.Helper()
	var frames []string
	for _, line := range strings.Split(body, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "data:") {
			frames = append(frames, line)
		}
	}
	if len(frames) == 0 {
		t.Fatalf("body carried no SSE frames: %q", body)
	}
	return frames
}
