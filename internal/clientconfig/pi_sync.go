package clientconfig

import (
	"bytes"
	"encoding/json"
	"path/filepath"
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
// exists on disk, then prunes the enabledModels patterns that catalogue no
// longer serves.
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
		changed, keys, err := syncOnePiCatalogFile(file, modelsJSON)
		if err != nil {
			return anyChanged, err
		}
		if changed {
			anyChanged = true
		}
		pruned, pruneErr := prunePiEnabledModels(file, keys, models)
		if pruneErr != nil {
			return anyChanged, pruneErr
		}
		if pruned {
			anyChanged = true
		}
	}
	return anyChanged, nil
}

func syncOnePiCatalogFile(file TargetFile, modelsJSON string) (bool, []string, error) {
	if !fileExists(file.Path) {
		return false, nil, nil
	}
	g, err := resolveGuard(file.Dir, relFromDir(file.Dir, file.Path))
	if err != nil {
		return false, nil, nil
	}
	old, err := readThroughGuard(g)
	if err != nil || len(old) == 0 {
		return false, nil, nil
	}
	keys := findPiNox9GoProviderKeys(old)
	if len(keys) == 0 {
		return false, nil, nil
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
		return false, keys, nil
	}
	if err := WriteFileAtomic(g, cur, hooks{}.withDefaults()); err != nil {
		return false, keys, err
	}
	return true, keys, nil
}

// prunePiEnabledModels removes, from the settings.json that sits next to a
// synced models.json, every enabledModels pattern this gateway owns that the
// catalogue no longer serves.
//
// Pi resolves each pattern against the live catalogue at start and prints
// `No models match pattern "<pattern>"` for one that misses. Nothing reconciled
// that list, so a provider dropping a model left a warning that repeated on
// every start with no visible cause: Fluence retired its `cheapest` routing
// profile, Copilot stopped serving gpt-5.4, and each one kept warning long
// after the catalogue was correct on disk.
//
// The prune stays inside the gateway's ownership: only patterns prefixed with a
// provider key this catalogue declares are candidates, and only when they hold
// no glob character (Pi's matcher is minimatch; proving a glob orphaned here
// would mean reimplementing it). Another provider's pattern, a bare model id, a
// glob, or a settings.json without enabledModels is left untouched.
func prunePiEnabledModels(modelsFile TargetFile, keys []string, models []PiCatalogModel) (bool, error) {
	if len(keys) == 0 {
		return false, nil
	}
	settingsPath := filepath.Join(filepath.Dir(modelsFile.Path), "settings.json")
	if !fileExists(settingsPath) {
		return false, nil
	}
	g, err := resolveGuard(modelsFile.Dir, relFromDir(modelsFile.Dir, settingsPath))
	if err != nil {
		return false, nil
	}
	old, err := readThroughGuard(g)
	if err != nil || len(old) == 0 {
		return false, nil
	}
	p, root, err := parseJSONC(old)
	if err != nil || root.kind != jLBrace {
		return false, nil
	}
	member := findJSONMember(root, "enabledModels")
	if member == nil || member.val.kind != jLBracket {
		return false, nil
	}

	kept := make([]string, 0, len(member.val.elems))
	dropped := 0
	for _, elem := range member.val.elems {
		if elem.kind != jString {
			// A non-string entry is not a pattern this prune can judge, and
			// rewriting the array would drop it. Leave the file alone.
			return false, nil
		}
		t := p.toks[elem.tok]
		pattern, ok := jsonStringValue(old[t.off:t.end()])
		if !ok {
			return false, nil
		}
		if gatewayPattern(pattern, keys) && !catalogServesPattern(pattern, keys, models) {
			dropped++
			continue
		}
		kept = append(kept, pattern)
	}
	if dropped == 0 {
		return false, nil
	}

	edit := Edit{KeyPath: []string{"enabledModels"}, Delete: true}
	if len(kept) > 0 {
		body, err := renderEnabledModelsJSON(kept)
		if err != nil {
			return false, err
		}
		edit = Edit{KeyPath: []string{"enabledModels"}, ValueJSON: body}
	}
	next, err := Patch(old, modelsFile.Format, []Edit{edit})
	if err != nil {
		return false, err
	}
	if bytes.Equal(next, old) {
		return false, nil
	}
	if err := WriteFileAtomic(g, next, hooks{}.withDefaults()); err != nil {
		return false, err
	}
	return true, nil
}

// gatewayPattern reports whether pattern is addressed through one of the
// provider keys this gateway declares ("nox9go/MinionsPRO").
func gatewayPattern(pattern string, keys []string) bool {
	text := strings.ToLower(strings.TrimSpace(pattern))
	for _, key := range keys {
		if strings.HasPrefix(text, strings.ToLower(key)+"/") {
			return true
		}
	}
	return false
}

// catalogServesPattern mirrors Pi's resolution for the shapes this prune can
// decide.
//
// Pi resolves an enabledModels pattern by: trimming it, taking an exact match on
// the provider-qualified "key/id" or the bare model id, matching a minimatch
// glob when it holds * ? [, and otherwise falling back to a fuzzy match that is
// substring containment, case-insensitive, against every model id and display
// name. It then retries the pattern with a trailing ":<level>" removed, and only
// reports no-match when the base fails too (`resolveText` in Pi's
// core/model-resolver.js).
//
// Because that fuzzy fallback exists, "nox9go/haiku" resolves to
// gh/claude-haiku-4.5 and must never be pruned. A pattern therefore counts as
// served when any model name or id contains its model text, when its
// pre-thinking base does, or when it is a glob this cannot evaluate: only a
// pattern no catalog entry could satisfy even fuzzily is removed.
func catalogServesPattern(pattern string, keys []string, models []PiCatalogModel) bool {
	if strings.ContainsAny(pattern, "*?[") {
		return true
	}
	text := piModelText(pattern, keys)
	if text == "" {
		return true
	}
	if fuzzyServed(text, models) {
		return true
	}
	// Pi strips one thinking-level suffix and resolves the base; only a base
	// that does not resolve makes the pattern warn.
	if i := strings.LastIndex(text, ":"); i > 0 {
		return fuzzyServed(strings.TrimSpace(text[:i]), models)
	}
	return false
}

// piModelText trims a pattern and strips a gateway provider prefix from it, the
// way Pi's own splitProviderPrefix does.
func piModelText(pattern string, keys []string) string {
	text := strings.TrimSpace(pattern)
	for _, key := range keys {
		if len(text) > len(key) && strings.EqualFold(text[:len(key)+1], key+"/") {
			return strings.TrimSpace(text[len(key)+1:])
		}
	}
	return text
}

// fuzzyServed is Pi's fuzzy fallback: a case-insensitive substring test against
// every model id and name in the catalogue.
func fuzzyServed(text string, models []PiCatalogModel) bool {
	if text == "" {
		return true
	}
	needle := strings.ToLower(text)
	for _, m := range models {
		if strings.Contains(strings.ToLower(m.ID), needle) || strings.Contains(strings.ToLower(m.Name), needle) {
			return true
		}
	}
	return false
}

func renderEnabledModelsJSON(patterns []string) (string, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("  ", "  ")
	if err := enc.Encode(patterns); err != nil {
		return "", err
	}
	return strings.TrimSpace(buf.String()), nil
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
