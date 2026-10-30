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
	return []FilePlan{{File: paths.Files[0], Edits: a.applyEdits(paths.Files[0], opts)}}, nil
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
		// The block itself goes only with its own fields: an explorer agent
		// the user wrote (and that does not point at us) is theirs.
		edits = append(edits, Edit{KeyPath: []string{"agent", "explorer"}, Delete: true})
	}
	return applyPlans([]FilePlan{{File: paths.Files[0], Edits: edits}}, nil)
}

// opencodeApplyEdits writes the provider block, the default model, and the
// explorer subagent.
//
// When the provider block already exists, scalar fields (npm, baseURL, apiKey)
// and the requested model are patched individually so any existing model
// catalogue and per-model metadata (modalities, overrides) are preserved.
func (a opencodeAdapter) applyEdits(file TargetFile, opts Options) []Edit {
	// The prefix is stripped here and re-added at each write, so a caller that
	// passes it already (a user copying a model id out of opencode) does not end
	// up with "<key>/<key>/…".
	model := strings.TrimPrefix(opts.Model, a.prefix())
	state := opencodeInspectProvider(file, a.key, model)

	var edits []Edit
	if !state.hasProvider {
		modelsJSON := "{}"
		if model != "" {
			modelsJSON = `{` + jsonString(model) + `: {"name": ` + jsonString(model) + `}}`
		}
		provider := `{"npm": "@ai-sdk/openai-compatible", "options": {"baseURL": ` +
			jsonString(withV1(opts.BaseURL)) + `, "apiKey": ` + jsonString(opts.APIKey) +
			`}, "models": ` + modelsJSON + `}`
		edits = append(edits, Edit{
			KeyPath:         []string{"provider", a.key},
			ValueJSON:       provider,
			CreateIfMissing: true,
		})
	} else {
		edits = append(edits,
			Edit{KeyPath: []string{"provider", a.key, "npm"}, Value: "@ai-sdk/openai-compatible", CreateIfMissing: true},
			Edit{KeyPath: []string{"provider", a.key, "options", "baseURL"}, Value: withV1(opts.BaseURL), CreateIfMissing: true},
		)
		if opts.APIKey != "" {
			edits = append(edits, Edit{
				KeyPath:         []string{"provider", a.key, "options", "apiKey"},
				Value:           opts.APIKey,
				CreateIfMissing: true,
			})
		}
		if model != "" && !state.hasTargetModel {
			modelEntry := `{"name": ` + jsonString(model) + `}`
			edits = append(edits, Edit{
				KeyPath:         []string{"provider", a.key, "models", model},
				ValueJSON:       modelEntry,
				CreateIfMissing: true,
			})
		}
	}

	if opts.Model != "" {
		edits = append(edits, Edit{
			KeyPath:         []string{"model"},
			Value:           a.prefix() + model,
			CreateIfMissing: true,
		})
		// The explorer subagent is created only when the file has none: an
		// agent the user wrote — its own description, its own model — is
		// theirs, and the dashboard picked a model, not a prompt.
		if !state.hasExplorer {
			edits = append(edits,
				Edit{KeyPath: []string{"agent", "explorer", "model"}, Value: a.prefix() + model, CreateIfMissing: true},
				Edit{KeyPath: []string{"agent", "explorer", "description"}, Value: "Explore the codebase", CreateIfMissing: true},
				Edit{KeyPath: []string{"agent", "explorer", "mode"}, Value: "subagent", CreateIfMissing: true},
			)
		}
	}
	return edits
}

type opencodeProviderState struct {
	hasProvider    bool
	hasTargetModel bool
	hasExplorer    bool
}

func opencodeInspectProvider(file TargetFile, providerKey, targetModel string) opencodeProviderState {
	g, err := resolveGuard(file.Dir, relFromDir(file.Dir, file.Path))
	if err != nil {
		return opencodeProviderState{}
	}
	b, err := readThroughGuard(g)
	if err != nil || len(b) == 0 {
		return opencodeProviderState{}
	}
	_, root, err := parseJSONC(b)
	if err != nil || root.kind != jLBrace {
		return opencodeProviderState{}
	}
	var out opencodeProviderState
	if agent := findJSONMember(root, "agent"); agent != nil && agent.val.kind == jLBrace {
		if findJSONMember(agent.val, "explorer") != nil {
			out.hasExplorer = true
		}
	}
	providerMember := findJSONMember(root, "provider")
	if providerMember == nil || providerMember.val.kind != jLBrace {
		return out
	}
	prov := findJSONMember(providerMember.val, providerKey)
	if prov == nil || prov.val.kind != jLBrace {
		return out
	}
	out.hasProvider = true
	if targetModel == "" {
		return out
	}
	models := findJSONMember(prov.val, "models")
	if models != nil && models.val.kind == jLBrace {
		if findJSONMember(models.val, targetModel) != nil {
			out.hasTargetModel = true
		}
	}
	return out
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
