package clientconfig

import "strings"

// Pi: ~/.pi/agent/models.json, JSONC.
//
// Like OpenCode, the provider entry carries ProviderKey rather than the
// upstream "9router" literal. Unlike OpenCode, Pi stores its full model
// catalogue and per-model overrides inside the provider object itself
// (`providers.<key>.models` and `providers.<key>.modelOverrides`), which on a
// real install can hold hundreds of entries. Apply therefore patches the
// scalar leaf keys (`baseUrl`, `api`, `apiKey`) individually and only writes
// `models` when the provider has none yet or holds a single placeholder entry,
// so an existing catalogue and `modelOverrides` are preserved intact.

type piAdapter struct {
	baseAdapter
	key string
}

func newPiAdapter(key string) Adapter {
	return piAdapter{
		baseAdapter: baseAdapter{id: "pi", paths: piPaths},
		key:         key,
	}
}

func (a piAdapter) prefix() string { return a.key + "/" }

func (a piAdapter) Plan(opts Options) ([]FilePlan, error) {
	paths, err := a.Paths(opts)
	if err != nil {
		return nil, err
	}
	file := paths.Files[0]
	return []FilePlan{{File: file, Edits: a.applyEdits(file, opts)}}, nil
}

func (a piAdapter) Apply(opts Options) (Result, error) {
	plans, err := a.Plan(opts)
	if err != nil {
		return Result{}, err
	}
	return applyPlans(plans, nil)
}

func (a piAdapter) Reset(opts Options) (Result, error) {
	paths, err := a.Paths(opts)
	if err != nil {
		return Result{}, err
	}
	edits := []Edit{
		{KeyPath: []string{"providers", a.key}, Delete: true},
	}
	return applyPlans([]FilePlan{{File: paths.Files[0], Edits: edits}}, nil)
}

func (a piAdapter) applyEdits(file TargetFile, opts Options) []Edit {
	model := strings.TrimPrefix(strings.TrimSpace(opts.Model), a.prefix())
	edits := []Edit{
		{KeyPath: []string{"providers", a.key, "baseUrl"}, Value: withV1(opts.BaseURL), CreateIfMissing: true},
		{KeyPath: []string{"providers", a.key, "api"}, Value: "openai-completions", CreateIfMissing: true},
	}
	if opts.APIKey != "" {
		edits = append(edits, Edit{
			KeyPath:         []string{"providers", a.key, "apiKey"},
			Value:           opts.APIKey,
			CreateIfMissing: true,
		})
	}
	state := piInspectModels(file, a.key, model)
	if model != "" {
		if !state.hasModelsKey || (state.elemCount <= 1 && !state.hasTargetModel) {
			entry := `[{"id": ` + jsonString(model) + `, "name": ` + jsonString(model) +
				`, "reasoning": true, "contextWindow": 200000, "maxTokens": 64000}]`
			edits = append(edits, Edit{
				KeyPath:         []string{"providers", a.key, "models"},
				ValueJSON:       entry,
				CreateIfMissing: true,
			})
		}
	} else if !state.hasModelsKey {
		edits = append(edits, Edit{
			KeyPath:         []string{"providers", a.key, "models"},
			ValueJSON:       "[]",
			CreateIfMissing: true,
		})
	}
	return edits
}

type piModelsState struct {
	hasModelsKey   bool
	elemCount      int
	hasTargetModel bool
}

func piInspectModels(file TargetFile, providerKey, targetModel string) piModelsState {
	g, err := resolveGuard(file.Dir, relFromDir(file.Dir, file.Path))
	if err != nil {
		return piModelsState{}
	}
	b, err := readThroughGuard(g)
	if err != nil || len(b) == 0 {
		return piModelsState{}
	}
	p, root, err := parseJSONC(b)
	if err != nil || root.kind != jLBrace {
		return piModelsState{}
	}
	providers := findJSONMember(root, "providers")
	if providers == nil || providers.val.kind != jLBrace {
		return piModelsState{}
	}
	prov := findJSONMember(providers.val, providerKey)
	if prov == nil || prov.val.kind != jLBrace {
		return piModelsState{}
	}
	models := findJSONMember(prov.val, "models")
	if models == nil || models.val.kind != jLBracket {
		return piModelsState{}
	}
	out := piModelsState{
		hasModelsKey: true,
		elemCount:    len(models.val.elems),
	}
	if targetModel == "" {
		return out
	}
	for _, elem := range models.val.elems {
		if elem.kind != jLBrace {
			continue
		}
		idMember := findJSONMember(elem, "id")
		if idMember == nil || idMember.val.isContainer() {
			continue
		}
		t := p.toks[idMember.val.tok]
		if v, ok := jsonStringValue(b[t.off:t.end()]); ok && v == targetModel {
			out.hasTargetModel = true
			break
		}
	}
	return out
}

