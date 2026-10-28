package clientconfig

import "strings"

// OpenCode: ~/.config/opencode/opencode.json, JSONC.
//
// The provider entry carries the gateway's own name (providerKey) rather than
// the upstream "9router" literal: a fork that renames the product must not
// leave a provider called after the product it forked from sitting in the
// user's file. Every model this writes is prefixed with that name, and Reset
// only touches keys that still carry the prefix — a model the user pointed
// somewhere else is theirs.
//
// Two spellings are on disk in the wild: opencode.json and opencode.jsonc.
// Both are read; the one the user already has wins, and opencode.json is only
// created when neither exists, because OpenCode prefers it.

type opencodeAdapter struct {
	baseAdapter
	// key is the provider id written under "provider" and used as the model
	// prefix ("<key>/<model>").
	key string
}

func newOpenCodeAdapter(key string) Adapter {
	return opencodeAdapter{
		baseAdapter: baseAdapter{id: "opencode", paths: opencodePaths},
		key:         key,
	}
}

func (a opencodeAdapter) prefix() string { return a.key + "/" }

func (a opencodeAdapter) Plan(opts Options) ([]FilePlan, error) {
	paths, err := a.Paths(opts)
	if err != nil {
		return nil, err
	}
	return []FilePlan{{File: paths.Files[0], Edits: a.applyEdits(opts)}}, nil
}

func (a opencodeAdapter) Apply(opts Options) (Result, error) {
	plans, err := a.Plan(opts)
	if err != nil {
		return Result{}, err
	}
	return applyPlans(plans, nil)
}

func (a opencodeAdapter) Reset(opts Options) (Result, error) {
	paths, err := a.Paths(opts)
	if err != nil {
		return Result{}, err
	}
	edits := []Edit{
		{KeyPath: []string{"provider", a.key}, Delete: true},
	}
	// `model` and `agent.explorer.model` are only ours while they point at us.
	owned := opencodeOwnedModelKeys(paths.Files[0], a.prefix())
	if owned.model {
		edits = append(edits, Edit{KeyPath: []string{"model"}, Delete: true})
	}
	if owned.explorer {
		edits = append(edits, Edit{KeyPath: []string{"agent", "explorer", "model"}, Delete: true})
		edits = append(edits, Edit{KeyPath: []string{"agent", "explorer", "description"}, Delete: true})
		edits = append(edits, Edit{KeyPath: []string{"agent", "explorer", "mode"}, Delete: true})
	}
	edits = append(edits, Edit{KeyPath: []string{"agent", "explorer"}, Delete: true})
	return applyPlans([]FilePlan{{File: paths.Files[0], Edits: edits}}, nil)
}

// opencodeApplyEdits writes the provider block, the default model, and the
// explorer subagent.
//
// The provider object is one edit with ValueJSON rather than several key edits:
// its shape is fixed (npm package, baseURL, apiKey, one models entry) and
// writing it whole keeps the file's other providers untouched by construction.
func (a opencodeAdapter) applyEdits(opts Options) []Edit {
	// The prefix is stripped here and re-added at each write, so a caller that
	// passes it already (a user copying a model id out of opencode) does not end
	// up with "<key>/<key>/…".
	model := strings.TrimPrefix(opts.Model, a.prefix())
	provider := `{"npm": "@ai-sdk/openai-compatible", "options": {"baseURL": ` +
		jsonString(withV1(opts.BaseURL)) + `, "apiKey": ` + jsonString(opts.APIKey) +
		`}, "models": {` + jsonString(model) + `: {"name": ` + jsonString(model) + `}}}`
	edits := []Edit{
		{KeyPath: []string{"provider", a.key}, ValueJSON: provider, CreateIfMissing: true},
	}
	if opts.Model != "" {
		edits = append(edits, Edit{
			KeyPath:         []string{"model"},
			Value:           a.prefix() + model,
			CreateIfMissing: true,
		})
		edits = append(edits,
			Edit{KeyPath: []string{"agent", "explorer", "model"}, Value: a.prefix() + model, CreateIfMissing: true},
			Edit{KeyPath: []string{"agent", "explorer", "description"}, Value: "Explore the codebase", CreateIfMissing: true},
			Edit{KeyPath: []string{"agent", "explorer", "mode"}, Value: "subagent", CreateIfMissing: true},
		)
	}
	return edits
}

// jsonString renders a Go string as a JSON string literal.
func jsonString(s string) string { return jsoncQuote(s) }

// opencodeOwnedKeys reports which of the two model keys still point at us.
// It is read-only and returns false on any error, so a file that cannot be
// parsed leaves the keys alone rather than deleting them blind.
type opencodeOwned struct{ model, explorer bool }

func opencodeOwnedModelKeys(file TargetFile, prefix string) opencodeOwned {
	g, err := resolveGuard(file.Dir, relFromDir(file.Dir, file.Path))
	if err != nil {
		return opencodeOwned{}
	}
	b, err := readThroughGuard(g)
	if err != nil || len(b) == 0 {
		return opencodeOwned{}
	}
	p, root, err := parseJSONC(b)
	if err != nil {
		return opencodeOwned{}
	}
	var out opencodeOwned
	if m := findJSONMember(root, "model"); m != nil && !m.val.isContainer() {
		t := p.toks[m.val.tok]
		if v, ok := jsonStringValue(b[t.off:t.end()]); ok {
			out.model = strings.HasPrefix(v, prefix)
		}
	}
	if agent := findJSONMember(root, "agent"); agent != nil && agent.val.kind == jLBrace {
		if explorer := findJSONMember(agent.val, "explorer"); explorer != nil && explorer.val.kind == jLBrace {
			if m := findJSONMember(explorer.val, "model"); m != nil && !m.val.isContainer() {
				t := p.toks[m.val.tok]
				if v, ok := jsonStringValue(b[t.off:t.end()]); ok {
					out.explorer = strings.HasPrefix(v, prefix)
				}
			}
		}
	}
	return out
}

// jsonStringValue unquotes a JSON string literal, reporting false when the
// bytes are not one.
func jsonStringValue(raw []byte) (string, bool) {
	if len(raw) < 2 || raw[0] != '"' {
		return "", false
	}
	v, err := decodeJSONString(raw)
	if err != nil {
		return "", false
	}
	return v, true
}

// withV1 appends the /v1 suffix every OpenAI-compatible client expects when the
// operator typed the bare origin.
func withV1(base string) string {
	b := strings.TrimRight(strings.TrimSpace(base), "/")
	if strings.HasSuffix(b, "/v1") {
		return b
	}
	return b + "/v1"
}
