package clientconfig

import (
	"bytes"
	"encoding/json"
	"strings"
)

// PiCatalogModel is the superset schema written into providers.<key>.models in
// ~/.pi/agent/models.json and ~/.gentle-shell/agent/models.json.
//
// It carries both the Pi ModelDefinitionSchema fields (id, name, reasoning,
// input, contextWindow, maxTokens) and the /v1/models fields (object, owned_by,
// capabilities, context_length, max_completion_tokens) so standalone Pi and
// Gentle Shell read the exact capabilities and token windows without querying
// the gateway over HTTP.
type PiCatalogModel struct {
	ID                  string   `json:"id"`
	Name                string   `json:"name"`
	Object              string   `json:"object,omitempty"`
	OwnedBy             string   `json:"owned_by,omitempty"`
	Reasoning           bool     `json:"reasoning,omitempty"`
	Input               []string `json:"input,omitempty"`
	ContextWindow       int      `json:"contextWindow,omitempty"`
	MaxTokens           int      `json:"maxTokens,omitempty"`
	ContextLength       int      `json:"context_length,omitempty"`
	MaxCompletionTokens int      `json:"max_completion_tokens,omitempty"`
	Capabilities        any      `json:"capabilities,omitempty"`
}

// SyncPiCatalog updates the models array of every NOX9GO provider block in
// ~/.pi/agent/models.json and ~/.gentle-shell/agent/models.json that already
// exists on disk.
//
// Files that do not exist or do not declare a NOX9GO provider entry are
// skipped, so background model mutations never create unrequested client files.
// In-place JSONC splicing preserves comments, modelOverrides, baseUrl, apiKey,
// and every other provider in the file. It writes atomically without creating
// per-mutation .bak files.
func SyncPiCatalog(home string, models []PiCatalogModel) (bool, error) {
	files, err := PiCandidateFiles(home)
	if err != nil {
		return false, err
	}
	modelsJSON, err := renderPiCatalogModelsJSON(models)
	if err != nil {
		return false, err
	}
	anyChanged := false
	for _, file := range files {
		changed, err := syncOnePiCatalogFile(file, modelsJSON)
		if err != nil {
			return anyChanged, err
		}
		if changed {
			anyChanged = true
		}
	}
	return anyChanged, nil
}

func syncOnePiCatalogFile(file TargetFile, modelsJSON string) (bool, error) {
	if !fileExists(file.Path) {
		return false, nil
	}
	g, err := resolveGuard(file.Dir, relFromDir(file.Dir, file.Path))
	if err != nil {
		return false, nil
	}
	old, err := readThroughGuard(g)
	if err != nil || len(old) == 0 {
		return false, nil
	}
	keys := findPiNox9GoProviderKeys(old)
	if len(keys) == 0 {
		return false, nil
	}
	cur := old
	for _, key := range keys {
		e := Edit{
			KeyPath:         []string{"providers", key, "models"},
			ValueJSON:       modelsJSON,
			CreateIfMissing: true,
		}
		next, patchErr := Patch(cur, file.Format, []Edit{e})
		if patchErr != nil {
			continue
		}
		cur = next
	}
	if bytes.Equal(cur, old) {
		return false, nil
	}
	if err := WriteFileAtomic(g, cur, hooks{}.withDefaults()); err != nil {
		return false, err
	}
	return true, nil
}

// findPiNox9GoProviderKeys returns the provider keys inside `providers` that
// belong to this gateway: any key matching ProviderKey case-insensitively
// ("nox9go", "NOX9GO") or whose baseUrl points at port 20130.
func findPiNox9GoProviderKeys(src []byte) []string {
	p, root, err := parseJSONC(src)
	if err != nil || root.kind != jLBrace {
		return nil
	}
	providersMember := findJSONMember(root, "providers")
	if providersMember == nil || providersMember.val.kind != jLBrace {
		return nil
	}
	var keys []string
	for _, m := range providersMember.val.members {
		if m.val.kind != jLBrace {
			continue
		}
		if strings.EqualFold(m.key, ProviderKey) || strings.EqualFold(m.key, "9router") {
			keys = append(keys, m.key)
			continue
		}
		for _, urlKey := range []string{"baseUrl", "baseURL"} {
			uMember := findJSONMember(m.val, urlKey)
			if uMember == nil || uMember.val.isContainer() {
				continue
			}
			t := p.toks[uMember.val.tok]
			if v, ok := jsonStringValue(src[t.off:t.end()]); ok && strings.Contains(v, ":20130") {
				keys = append(keys, m.key)
				break
			}
		}
	}
	return keys
}

func renderPiCatalogModelsJSON(models []PiCatalogModel) (string, error) {
	if len(models) == 0 {
		return "[]", nil
	}
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("      ", "  ")
	if err := enc.Encode(models); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
}
