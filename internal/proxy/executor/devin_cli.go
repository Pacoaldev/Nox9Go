package executor

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"

	"encoding/json/jsontext"
	json "encoding/json/v2"

	"nox9go/proxy/internal/log"
	"nox9go/proxy/internal/proxy"
	"nox9go/proxy/internal/translator"
)

// Devin CLI bridge: Devin Cloud reached through the locally installed `devin`
// CLI in ACP mode instead of a hosted HTTP endpoint.
//
// The gateway launches `devin acp --cloud` as a subprocess and speaks ACP
// (Agent Client Protocol) over its stdio: JSON-RPC 2.0, UTF-8, one object per
// newline-delimited line (the ACP stdio transport forbids embedded newlines).
// The turn is initialize → session/new (cwd) → session/prompt, and the agent
// reports its answer as `session/update` notifications carrying
// `agent_message_chunk` text before the `session/prompt` response closes it.
//
// Authentication is entirely the CLI's own (`devin auth login`): the gateway
// never holds a Devin credential, which is why the registry marks this
// provider NoAuth and the executor ignores req.APIKey. The subprocess
// inherits the ordinary environment and receives nothing extra, and no agent
// or CLI output is echoed into a client-facing error: the credential check is
// judged by exit code alone, and the ACP agent's stderr stays in an
// 8 KiB-bounded capture that reaches the gateway log as a length note.

const (
	devinCLIName        = "devin"
	devinACPMaxLine     = 1 << 20  // a JSON-RPC frame larger than this is refused
	devinACPStderrLimit = 8 * 1024 // bounded stderr capture per turn
	devinShutdownGrace  = 5 * time.Second
	devinAuthCacheTTL   = 30 * time.Second  // 'auth status' spawns a process; do not pay it per turn
	devinModelsCacheTTL = 5 * time.Minute   // 'models' is the account's catalog; it changes rarely
	devinModeEnv        = "DEVIN_ACP_MODE"  // "cloud" relays to Devin Cloud; unset runs the local agent
	devinModelsCmd      = "models"
)

// devinCLIPath resolves the executable the bridge launches. A package-level
// indirection so the tests install a fake agent without touching PATH.
var devinCLIPath = devinCLIDefaultPath

// devinAuthCache remembers a successful `devin auth status` for a short window.
// A failure is never cached: a logged-out account must surface on the next turn.
var devinAuthCache struct {
	mu      sync.Mutex
	path    string
	okUntil time.Time
}

// devinModelsCache remembers the account's model ids so a requested model is
// only forwarded when the CLI can actually serve it.
var devinModelsCache struct {
	mu      sync.Mutex
	path    string
	ids     map[string]bool
	okUntil time.Time
}

func devinCLIDefaultPath() (string, error) {
	path, err := exec.LookPath(devinCLIName)
	if err != nil {
		return "", fmt.Errorf("lookup %s: %w", devinCLIName, err)
	}
	return path, nil
}

// devinCommand builds the subprocess command. An npm-style install exposes
// the CLI as a .cmd/.bat shim on Windows, which CreateProcess cannot launch
// directly; those go through cmd.exe explicitly.
func devinCommand(ctx context.Context, path string, args ...string) *exec.Cmd {
	if runtime.GOOS == "windows" {
		if ext := strings.ToLower(filepath.Ext(path)); ext == ".bat" || ext == ".cmd" {
			return exec.CommandContext(ctx, "cmd", append([]string{"/c", path}, args...)...)
		}
	}
	return exec.CommandContext(ctx, path, args...)
}

// devinCLIAuthenticated judges the credential only by the exit code of
// `devin auth status`. Its output names the signed-in account, so stdout and
// stderr are discarded unread and the failure message quotes the exit code
// and nothing else. A success is cached briefly because the check spawns a
// process; a failure is never cached, so a logout is visible immediately.
func devinCLIAuthenticated(ctx context.Context, path string) error {
	devinAuthCache.mu.Lock()
	cached := devinAuthCache.path == path && time.Now().Before(devinAuthCache.okUntil)
	devinAuthCache.mu.Unlock()
	if cached {
		return nil
	}

	cmd := devinCommand(ctx, path, "auth", "status")
	cmd.Stdout = io.Discard
	cmd.Stderr = io.Discard
	if err := cmd.Run(); err != nil {
		devinAuthCache.mu.Lock()
		devinAuthCache.path = ""
		devinAuthCache.okUntil = time.Time{}
		devinAuthCache.mu.Unlock()
		var exitErr *exec.ExitError
		if errors.As(err, &exitErr) {
			return proxy.UpstreamFailure(http.StatusBadGateway, fmt.Sprintf(
				"devin-cli: 'devin auth status' exited with status %d — run 'devin auth login' in a terminal, then retry",
				exitErr.ExitCode()))
		}
		return proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: could not run 'devin auth status' — verify the Devin CLI installation")
	}

	devinAuthCache.mu.Lock()
	devinAuthCache.path = path
	devinAuthCache.okUntil = time.Now().Add(devinAuthCacheTTL)
	devinAuthCache.mu.Unlock()
	return nil
}

// devinModelArgs forwards the requested model to the CLI only when the
// account's own catalog lists it. `devin models` is the authority on what the
// key may use; anything else (an old catalog row, a model gated by the plan)
// would make the CLI reject the turn, so the agent keeps its default instead.
func devinModelArgs(ctx context.Context, path, model string) []string {
	if model == "" || model == devinCLIName {
		return nil
	}
	if !devinModelKnown(ctx, path, model) {
		return nil
	}
	return []string{"--model", model}
}

func devinModelKnown(ctx context.Context, path, model string) bool {
	want := devinNormalizeModel(model)
	if want == "" {
		return false
	}
	devinModelsCache.mu.Lock()
	if devinModelsCache.path == path && time.Now().Before(devinModelsCache.okUntil) {
		ids := devinModelsCache.ids
		devinModelsCache.mu.Unlock()
		return ids[want]
	}
	devinModelsCache.mu.Unlock()

	cmd := devinCommand(ctx, path, devinModelsCmd)
	out, err := cmd.Output()
	if err != nil {
		// A failed lookup is cached briefly so a broken CLI is not spawned on
		// every turn; the turn itself still runs with the agent's default model.
		devinModelsCache.mu.Lock()
		devinModelsCache.path = path
		devinModelsCache.ids = map[string]bool{}
		devinModelsCache.okUntil = time.Now().Add(devinAuthCacheTTL)
		devinModelsCache.mu.Unlock()
		return false
	}
	ids := map[string]bool{}
	for _, token := range strings.Fields(string(out)) {
		if id := devinNormalizeModel(token); id != "" {
			ids[id] = true
		}
	}
	devinModelsCache.mu.Lock()
	devinModelsCache.path = path
	devinModelsCache.ids = ids
	devinModelsCache.okUntil = time.Now().Add(devinModelsCacheTTL)
	devinModelsCache.mu.Unlock()
	return ids[want]
}

// devinNormalizeModel makes ids comparable across the two spellings the CLI
// uses: the dashboard catalog writes dots (`swe-1.6`), the CLI writes dashes
// (`swe-1-6`), and `devin models` also prints family headers wrapped in
// parentheses. Lower-cased, punctuation folded to a dash, wrapper punctuation
// trimmed — that is enough for an exact id match, and it never guesses a
// different model.
func devinNormalizeModel(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	s = strings.Trim(s, "()[]\"'`,")
	s = strings.NewReplacer(".", "-", "_", "-", " ", "-").Replace(s)
	return strings.Trim(s, "-")
}

// devinACPArgs is the argv the bridge spawns. The local agent is the default:
// it answers from the same account without creating a Devin Cloud session, and
// a Cloud session needs a paid quota (`out_of_quota` on a free org).
func devinACPArgs(ctx context.Context, path, model string) []string {
	args := []string{"acp"}
	if strings.EqualFold(strings.TrimSpace(os.Getenv(devinModeEnv)), "cloud") {
		args = append(args, "--cloud")
	}
	return append(args, devinModelArgs(ctx, path, model)...)
}

// ProbeDevinCLI reports whether the bridge can serve a turn: the executable
// resolves on PATH and the CLI's own login is present. The dashboard's "Check"
// button calls it so a not-installed or logged-out CLI is surfaced before a
// request reaches the router, instead of being reported as a valid provider.
func ProbeDevinCLI(ctx context.Context) error {
	path, err := devinCLIPath()
	if err != nil {
		return proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: the 'devin' executable was not found on PATH — install the Devin CLI, then run 'devin auth login' once")
	}
	return devinCLIAuthenticated(ctx, path)
}

// ForwardDevinCLI serves one Chat Completions turn through the ACP agent.
func ForwardDevinCLI(w http.ResponseWriter, req *Request) error {
	prompt, model, err := devinParsePrompt(req.Body)
	if err != nil {
		return err
	}
	ctx := req.Ctx
	if ctx == nil {
		ctx = context.Background()
	}
	path, err := devinCLIPath()
	if err != nil {
		// The lookup is judged by outcome, not by its error text: whatever
		// PATH lookup failed means the same advice to the operator.
		return proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: the 'devin' executable was not found on PATH — install the Devin CLI, then run 'devin auth login' once")
	}
	if err := devinCLIAuthenticated(ctx, path); err != nil {
		return err
	}
	if req.IsStream {
		return devinRunStream(w, req, ctx, path, prompt, model)
	}
	return devinRunCompletion(w, req, ctx, path, prompt, model)
}

// devinParsePrompt reads the OpenAI body and folds the conversation into the
// single text prompt the ACP content block carries.
func devinParsePrompt(body []byte) (string, string, error) {
	var envelope struct {
		Model    string            `json:"model"`
		Messages []deepSeekMessage `json:"messages"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", "", proxy.UpstreamFailure(http.StatusBadRequest,
			"devin-cli: parse request: "+err.Error())
	}
	prompt := deepSeekMessagesToPrompt(envelope.Messages, 0, false)
	if strings.TrimSpace(prompt) == "" {
		return "", "", proxy.UpstreamFailure(http.StatusBadRequest,
			"devin-cli: request carried no text content")
	}
	model := strings.TrimSpace(envelope.Model)
	if model == "" {
		model = devinCLIName
	}
	return prompt, model, nil
}

func devinResponseID() string {
	return "chatcmpl-devin-cli-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

// devinWorkspace returns the absolute directory session/new announces as the
// agent's cwd. It is a dedicated, empty directory rather than the gateway's
// own working directory: the operator's project path is not something a cloud
// session needs to learn, and the agent must never be pointed at the gateway
// checkout by accident.
func devinWorkspace() string {
	dir := filepath.Join(os.TempDir(), "nox9go-devin-workspace")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return os.TempDir()
	}
	return dir
}

// devinRunCompletion writes the whole agent answer as a chat.completion.
func devinRunCompletion(w http.ResponseWriter, req *Request, ctx context.Context, path, prompt, model string) error {
	full, usage, err := devinTurn(ctx, path, prompt, model, nil)
	if err != nil {
		return err
	}
	promptTokens, completionTokens, totalTokens := usage.prompt(), usage.completion(), usage.total()
	if totalTokens == 0 {
		promptTokens = len(prompt) / 4
		completionTokens = len(full) / 4
		totalTokens = promptTokens + completionTokens
	}
	completion := map[string]any{
		"id":      devinResponseID(),
		"object":  "chat.completion",
		"created": time.Now().Unix(),
		"model":   model,
		"choices": []map[string]any{{
			"index":         0,
			"message":       map[string]any{"role": "assistant", "content": full},
			"finish_reason": "stop",
		}},
		"usage": map[string]any{
			"prompt_tokens":     promptTokens,
			"completion_tokens": completionTokens,
			"total_tokens":      totalTokens,
		},
	}
	encoded, err := json.Marshal(completion)
	if err != nil {
		return fmt.Errorf("devin-cli: marshal completion: %w", err)
	}
	return jsonResponse(req.Ctx, w, bytes.NewReader(encoded), req.TranslateResp, req.ResponseBuf)
}

// devinRunStream relays the agent's message chunks as OpenAI SSE. The SSE
// headers stay unwritten until the first chunk arrives, so a turn that fails
// before it said anything still returns a real status the router can act on.
func devinRunStream(w http.ResponseWriter, req *Request, ctx context.Context, path, prompt, model string) error {
	hw := proxy.NewHeartbeatWriter(req.Ctx, w, 0)
	defer hw.Close()
	em := &devinStreamEmitter{
		req: req, hw: hw, model: model,
		id: devinResponseID(), created: time.Now().Unix(),
	}
	_, usage, err := devinTurn(ctx, path, prompt, model, em.emitContent)
	if err != nil {
		if !em.opened {
			return err
		}
		status, message := proxy.ClassifyStreamAbort(err)
		if _, werr := hw.Write(proxy.BuildStreamErrorBytes(status, message, proxy.SSEFormatOpenAI)); werr == nil && em.flusher != nil {
			em.flusher.Flush()
		}
		return err
	}
	if err := em.finish(); err != nil {
		return err
	}
	completionTokens := usage.completion()
	if completionTokens == 0 {
		completionTokens = em.sentText / 4
	}
	translator.SetUsage(req.Ctx, &translator.OpenAIUsage{
		PromptTokens:     usage.prompt(),
		CompletionTokens: completionTokens,
	})
	return nil
}

// devinStreamEmitter writes the streaming answer in the shared chunk shape.
type devinStreamEmitter struct {
	req      *Request
	hw       *proxy.HeartbeatWriter
	flusher  http.Flusher
	id       string
	model    string
	created  int64
	sentText int
	opened   bool
	roleSent bool
}

func (e *devinStreamEmitter) open() {
	if e.opened {
		return
	}
	e.opened = true
	e.flusher = proxy.WriteSSEHeaders(e.hw)
}

func (e *devinStreamEmitter) markTTFT() {
	if e.req.TTFT == nil || *e.req.TTFT != 0 {
		return
	}
	start := e.req.StartTime
	if start.IsZero() {
		start = time.Now()
	}
	*e.req.TTFT = time.Since(start).Milliseconds()
}

func (e *devinStreamEmitter) emitContent(text string) error {
	if text == "" {
		return nil
	}
	if e.req.ResponseBuf != nil {
		if _, err := e.req.ResponseBuf.Write([]byte(text)); err != nil {
			return err
		}
	}
	e.sentText += len(text)
	delta := map[string]any{"content": text}
	if !e.roleSent {
		e.roleSent = true
		delta["role"] = "assistant"
	}
	e.markTTFT()
	chunk := map[string]any{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []map[string]any{{"index": 0, "delta": delta, "finish_reason": nil}},
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("devin-cli: marshal chunk: %w", err)
	}
	e.open()
	if _, err := e.hw.Write(fmt.Appendf(nil, "data: %s\n\n", encoded)); err != nil {
		return err
	}
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return nil
}

// finish closes the stream with the terminal chunk and [DONE].
func (e *devinStreamEmitter) finish() error {
	chunk := map[string]any{
		"id":      e.id,
		"object":  "chat.completion.chunk",
		"created": e.created,
		"model":   e.model,
		"choices": []map[string]any{{"index": 0, "delta": map[string]any{}, "finish_reason": "stop"}},
	}
	encoded, err := json.Marshal(chunk)
	if err != nil {
		return fmt.Errorf("devin-cli: marshal finish chunk: %w", err)
	}
	e.open()
	if _, err := e.hw.Write(fmt.Appendf(nil, "data: %s\n\n", encoded)); err != nil {
		return err
	}
	if _, err := e.hw.Write([]byte("data: [DONE]\n\n")); err != nil {
		return err
	}
	if e.flusher != nil {
		e.flusher.Flush()
	}
	return nil
}

// devinUsage is the token accounting the agent reports for a turn. A zero
// total means the agent did not report any, and the caller estimates.
type devinUsage struct {
	In    int `json:"inputTokens"`
	Out   int `json:"outputTokens"`
	Total int `json:"totalTokens"`
}

func (u devinUsage) total() int {
	if u.Total > 0 {
		return u.Total
	}
	return u.In + u.Out
}

func (u devinUsage) prompt() int     { return u.In }
func (u devinUsage) completion() int { return u.Out }

// devinTurn runs one full ACP conversation: spawn, initialize, session/new,
// session/prompt. Agent text chunks go to onChunk as they arrive; the return
// value is the whole answer. The turn accumulates the chunks itself, so a
// non-streaming caller that passes no sink still receives the text.
func devinTurn(ctx context.Context, path, prompt, model string, onChunk func(string) error) (string, devinUsage, error) {
	conn, err := startDevinACP(ctx, path, model)
	if err != nil {
		return "", devinUsage{}, err
	}
	defer conn.close()

	var collected strings.Builder
	sink := func(text string) error {
		collected.WriteString(text)
		if onChunk != nil {
			return onChunk(text)
		}
		return nil
	}

	if _, err := conn.request(ctx, "initialize", map[string]any{
		"protocolVersion":    1,
		"clientCapabilities": map[string]any{"fs": map[string]any{"readTextFile": false, "writeTextFile": false}, "terminal": false},
	}, nil); err != nil {
		return "", devinUsage{}, err
	}
	result, err := conn.request(ctx, "session/new", map[string]any{
		"cwd":        devinWorkspace(),
		"mcpServers": []any{},
	}, nil)
	if err != nil {
		return "", devinUsage{}, err
	}
	var created struct {
		SessionID string `json:"sessionId"`
	}
	if err := json.Unmarshal(result, &created); err != nil || created.SessionID == "" {
		return "", devinUsage{}, proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: the ACP agent answered session/new without a sessionId")
	}
	finished, err := conn.request(ctx, "session/prompt", map[string]any{
		"sessionId": created.SessionID,
		"prompt":    []map[string]any{{"type": "text", "text": prompt}},
	}, sink)
	if err != nil {
		return "", devinUsage{}, err
	}
	var turn struct {
		Usage devinUsage `json:"usage"`
	}
	_ = json.Unmarshal(finished, &turn)
	return strings.TrimSpace(collected.String()), turn.Usage, nil
}

// devinACPMessage is one JSON-RPC frame in either direction. A missing ID marks
// a notification. ID stays raw because ACP allows a string or a number and the
// agent's requests must be answered with the exact id it chose. Params and
// Result stay raw so a frame this side does not read costs no allocation.
type devinACPMessage struct {
	JSONRPC string         `json:"jsonrpc"`
	ID      jsontext.Value `json:"id"`
	Method  string         `json:"method"`
	Params  jsontext.Value `json:"params"`
	Result  jsontext.Value `json:"result"`
	Error   *devinACPError `json:"error"`
}

type devinACPError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// devinACPConn is one live `devin acp` subprocess.
type devinACPConn struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	scan   *bufio.Scanner
	stderr *devinBoundedBuffer
	nextID int
}

func startDevinACP(ctx context.Context, path, model string) (*devinACPConn, error) {
	cmd := devinCommand(ctx, path, devinACPArgs(ctx, path, model)...)
	stderr := &devinBoundedBuffer{limit: devinACPStderrLimit}
	cmd.Stderr = stderr
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("devin-cli: stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("devin-cli: stdout pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: could not start 'devin acp --cloud' — verify the Devin CLI installation")
	}
	scan := bufio.NewScanner(stdout)
	scan.Buffer(make([]byte, 0, 64*1024), devinACPMaxLine)
	return &devinACPConn{cmd: cmd, stdin: stdin, scan: scan, stderr: stderr}, nil
}

// close ends the subprocess. The agent exits when its stdin closes; a process
// that lingers past the grace window is killed so no turn leaks one.
func (c *devinACPConn) close() {
	_ = c.stdin.Close()
	done := make(chan struct{})
	go func() {
		_ = c.cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(devinShutdownGrace):
		_ = c.cmd.Process.Kill()
		<-done
	}
	if n := c.stderr.Len(); n > 0 {
		log.Error("executor", "devin-cli agent wrote stderr (bounded capture)",
			"bytes", n, "tail", c.stderr.Tail(500))
	}
}

// request sends one JSON-RPC request and reads frames until its response
// arrives. Notifications pass through onChunk; the agent is expected to
// answer in order, so the first response carrying our id is ours.
func (c *devinACPConn) request(ctx context.Context, method string, params any, onChunk func(string) error) (jsontext.Value, error) {
	c.nextID++
	id := c.nextID
	frame, err := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      id,
		"method":  method,
		"params":  params,
	})
	if err != nil {
		return nil, fmt.Errorf("devin-cli: marshal %s request: %w", method, err)
	}
	if _, err := c.stdin.Write(append(frame, '\n')); err != nil {
		return nil, fmt.Errorf("devin-cli: write %s request: %w", method, err)
	}

	for c.scan.Scan() {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("devin-cli: turn aborted: %w", ctx.Err())
		}
		line := bytes.TrimSpace(c.scan.Bytes())
		if len(line) == 0 {
			continue
		}
		var msg devinACPMessage
		if err := json.Unmarshal(line, &msg); err != nil {
			return nil, proxy.UpstreamFailure(http.StatusBadGateway,
				"devin-cli: the ACP agent sent an unparseable message")
		}
		if msg.ID != nil && msg.Method != "" {
			// The agent is asking this side for something (a permission
			// decision, a filesystem read). Answering nothing would hang the
			// turn until its timeout, so every such request is refused here:
			// the gateway never grants a tool permission on the operator's
			// behalf, and it declared no fs/terminal capability anyway.
			if err := c.answerAgentRequest(msg); err != nil {
				return nil, err
			}
			continue
		}
		if msg.ID != nil && string(bytes.TrimSpace(msg.ID)) == strconv.Itoa(id) {
			if msg.Error != nil {
				return nil, devinAgentError(msg.Error)
			}
			return msg.Result, nil
		}
		if msg.ID == nil && msg.Method == "session/update" && onChunk != nil {
			if text := devinAgentChunkText(msg.Params); text != "" {
				if err := onChunk(text); err != nil {
					return nil, err
				}
			}
		}
	}
	if err := c.scan.Err(); err != nil {
		if ctx.Err() != nil {
			return nil, fmt.Errorf("devin-cli: turn aborted: %w", ctx.Err())
		}
		return nil, proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: failed to read the ACP agent's output")
	}
	if ctx.Err() != nil {
		return nil, fmt.Errorf("devin-cli: turn aborted: %w", ctx.Err())
	}
	return nil, proxy.UpstreamFailure(http.StatusBadGateway,
		"devin-cli: the ACP agent closed its output before completing the turn")
}

// answerAgentRequest refuses an inbound request from the agent. A permission
// prompt is answered `cancelled` — the ACP-defined way to decline — because the
// gateway must never approve a tool on the operator's behalf; anything else is
// reported as an unsupported method. The agent's id is echoed byte for byte.
func (c *devinACPConn) answerAgentRequest(msg devinACPMessage) error {
	var reply []byte
	if msg.Method == "session/request_permission" {
		reply = fmt.Appendf(nil,
			"{\"jsonrpc\":\"2.0\",\"id\":%s,\"result\":{\"outcome\":{\"outcome\":\"cancelled\"}}}\n",
			bytes.TrimSpace(msg.ID))
	} else {
		reply = fmt.Appendf(nil,
			"{\"jsonrpc\":\"2.0\",\"id\":%s,\"error\":{\"code\":-32601,\"message\":\"method not supported by this client\"}}\n",
			bytes.TrimSpace(msg.ID))
	}
	if _, err := c.stdin.Write(reply); err != nil {
		return fmt.Errorf("devin-cli: answer %s: %w", msg.Method, err)
	}
	return nil
}

// devinAgentChunkText extracts agent_message_chunk text from a session/update.
func devinAgentChunkText(params jsontext.Value) string {
	var update struct {
		Update struct {
			SessionUpdate string `json:"sessionUpdate"`
			Content       struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		} `json:"update"`
	}
	if err := json.Unmarshal(params, &update); err != nil {
		return ""
	}
	if update.Update.SessionUpdate != "agent_message_chunk" || update.Update.Content.Type != "text" {
		return ""
	}
	return update.Update.Content.Text
}

// devinAgentError maps a JSON-RPC error response to an actionable failure.
// The protocol error message describes the turn, not the account, so it is
// safe to relay; the auth case is re-worded because its fix is the same login
// flow the exit-code check advises.
func devinAgentError(e *devinACPError) error {
	if e.Message == "" {
		return proxy.UpstreamFailure(http.StatusBadGateway, "devin-cli: the ACP agent refused the turn")
	}
	if strings.Contains(strings.ToLower(e.Message), "auth") {
		return proxy.UpstreamFailure(http.StatusBadGateway,
			"devin-cli: agent reported authentication required — run 'devin auth login' in a terminal, then retry")
	}
	return proxy.UpstreamFailure(http.StatusBadGateway, "devin-cli: "+e.Message)
}

// devinBoundedBuffer caps what the bridge keeps of the agent's stderr. The
// pipe must never block on a slow reader, so everything past the limit is
// swallowed and only the kept prefix is ever reported.
type devinBoundedBuffer struct {
	mu    sync.Mutex
	limit int
	buf   bytes.Buffer
}

func (b *devinBoundedBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.buf.Len() < b.limit {
		if room := b.limit - b.buf.Len(); len(p) > room {
			p = p[:room]
		}
		b.buf.Write(p)
	}
	return len(p), nil
}

func (b *devinBoundedBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Len()
}

// Tail returns up to the last n bytes of the capture, which is where an agent
// tool reports its final failure.
func (b *devinBoundedBuffer) Tail(n int) string {
	b.mu.Lock()
	defer b.mu.Unlock()
	s := b.buf.String()
	if len(s) > n {
		return s[len(s)-n:]
	}
	return s
}
