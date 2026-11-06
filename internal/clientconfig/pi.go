package clientconfig

import (
	"strconv"
	"strings"

	"nox9go/proxy/internal/providers"
)

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
		if !state.hasModelsKey {
			entry := piModelEntryJSON(model)
			edits = append(edits, Edit{
				KeyPath:         []string{"providers", a.key, "models"},
				ValueJSON:       entry,
				CreateIfMissing: true,
			})
		} else if !state.hasTargetModel && state.newArrayJSON != "" {
			edits = append(edits, Edit{
				KeyPath:         []string{"providers", a.key, "models"},
				ValueJSON:       state.newArrayJSON,
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

// piModelEntryJSON renders the single-model `models` array Pi expects.
func piModelEntryJSON(model string) string {
	return "[" + piModelEntry(model) + "]"
}

// piModelEntry builds one Pi model entry.
//
// contextWindow/maxTokens come from the model capability catalog, not a fixed
// placeholder. The old entry claimed 200000/64000 for every model, which is what
// made a Pi client ask a provider for a max_tokens it rejects: Groq answered
// "`max_tokens` must be less than or equal to `16384`" for qwen3.8-27b precisely
// because the client sent the invented 64000. When the catalog has no figure the
// entry omits the field rather than guessing, so the client falls back to the
// model's own default instead of a number we made up.
func piModelEntry(model string) string {
	caps := providers.GetCapabilitiesDetailForModel("", model)
	var b strings.Builder
	b.WriteString(`{"id": `)
	b.WriteString(jsonString(model))
	b.WriteString(`, "name": `)
	b.WriteString(jsonString(model))
	if caps.Reasoning {
		b.WriteString(`, "reasoning": true`)
	}
	if caps.ContextWindow > 0 {
		b.WriteString(`, "contextWindow": `)
		b.WriteString(strconv.Itoa(caps.ContextWindow))
	}
	if caps.MaxOutput > 0 {
		b.WriteString(`, "maxTokens": `)
		b.WriteString(strconv.Itoa(caps.MaxOutput))
	}
	b.WriteString("}")
	return b.String()
}

type piModelsState struct {
	hasModelsKey   bool
	elemCount      int
	hasTargetModel bool
	newArrayJSON   string
}

func piAppendModelToArray(b []byte, toks []jsonTok, arrayNode *jsonNode, model string) string {
	newEntry := piModelEntry(model)
	if len(arrayNode.elems) == 0 {
		return "[" + newEntry + "]"
	}

	start := toks[arrayNode.tok].off
	closeOff := toks[arrayNode.closeTok].off
	beforeClose := string(b[start:closeOff])

	lastNL := strings.LastIndexByte(beforeClose, '\n')
	if lastNL < 0 {
		trimmed := strings.TrimRight(beforeClose, " \t\r\n")
		needsComma := !strings.HasSuffix(trimmed, ",") && !strings.HasSuffix(trimmed, "[")
		if needsComma {
			return trimmed + ", " + newEntry + "]"
		}
		return trimmed + newEntry + "]"
	}

	indent := beforeClose[lastNL+1:]
	trimmed := strings.TrimRight(beforeClose, " \t\r\n")
	needsComma := !strings.HasSuffix(trimmed, ",") && !strings.HasSuffix(trimmed, "[")
	elemIndent := indent + "  "
	if needsComma {
		return trimmed + ",\n" + elemIndent + newEntry + "\n" + indent + "]"
	}
	return trimmed + "\n" + elemIndent + newEntry + "\n" + indent + "]"
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
	if !out.hasTargetModel {
		out.newArrayJSON = piAppendModelToArray(b, p.toks, models.val, targetModel)
	}
	return out
}
