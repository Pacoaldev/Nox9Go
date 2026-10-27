package dashboard

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// CatalogConflict explains why a promotion was refused because the id it wants
// to claim is already taken.
//
// The patchers are idempotent, which makes them safe to re-run but also makes
// them the wrong place to notice a duplicate: a promotion whose id already
// exists would skip every file it recognises and still delete the node and
// re-point its connections, reporting success. The conflict has to be caught
// before the first write.
type CatalogConflict struct {
	ID     string   `json:"id"`
	Source []string `json:"sources"`
	Detail string   `json:"detail"`
	// Kinds are the serviceKinds the existing catalog entry declares ("llm",
	// "image", "video", ...). Empty means the entry was found outside
	// PROVIDER_CATALOG, or declares no kinds (which reads as ["llm"] in the web
	// catalog's own default).
	//
	// The promotion dialog uses this to say *what* the id already is. "Already
	// in the catalog" is the wrong advice for a provider that only serves
	// images: the user is not duplicating it, they are about to promote a chat
	// node onto a media id, and the two are different decisions.
	Kinds []string `json:"kinds,omitempty"`
}

func (c *CatalogConflict) Error() string { return c.Detail }

// catalogEntryExists reports where a provider id is already registered, across
// the three places that make an id "taken":
//
//   - PROVIDER_CATALOG in web/src/lib/providers.ts, which renders the card;
//   - KnownProviders in internal/providers/providers.go, which resolves the
//     Check button and the executor;
//   - modelsListURL in the same file, which backs the import buttons.
//
// An id present in only some of them is still a conflict: the promotion exists
// to fill those gaps for a *new* provider, and silently completing a
// half-registered one would hide a bug rather than fix it.
//
// A missing file is not an error — the caller may run against a tree without a
// web checkout — but it is reported so the caller can decide whether the check
// was conclusive.
func catalogEntryExists(root, id string) (*CatalogConflict, error) {
	if strings.TrimSpace(id) == "" {
		return nil, nil
	}
	conflict := &CatalogConflict{ID: id}

	providersTS := filepath.Join(root, "web", "src", "lib", "providers.ts")
	if entries, err := catalogIDsFromProviderCatalog(providersTS); err == nil {
		for _, e := range entries {
			if e == id {
				conflict.Source = append(conflict.Source, "web/src/lib/providers.ts (PROVIDER_CATALOG)")
				break
			}
		}
		// Kinds come from the catalog block itself, so a provider that only
		// exists in KnownProviders (as a media endpoint with no card) still
		// reports no kinds rather than a guess.
		if kinds, err := catalogServiceKinds(providersTS, id); err == nil {
			conflict.Kinds = kinds
		}
	} else if !os.IsNotExist(err) {
		return nil, fmt.Errorf("read providers.ts: %w", err)
	}

	providersGo := filepath.Join(root, "internal", "providers", "providers.go")
	knownIDs, listIDs, err := goRegistryIDs(providersGo)
	if err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("read providers.go: %w", err)
	}
	for _, e := range knownIDs {
		if e == id {
			conflict.Source = append(conflict.Source, "internal/providers/providers.go (KnownProviders)")
			break
		}
	}
	for _, e := range listIDs {
		if e == id {
			conflict.Source = append(conflict.Source, "internal/providers/providers.go (modelsListURL)")
			break
		}
	}

	if len(conflict.Source) == 0 {
		return nil, nil
	}
	conflict.Detail = conflictDetail(id, conflict.Source, conflict.Kinds)
	return conflict, nil
}

// conflictDetail phrases the refusal around what the caller can actually do.
//
// A duplicate chat provider and a media provider that happens to share the id
// need different advice: the first is a naming collision, the second is a
// scope question ("do I want this id to serve images too?"). Naming the
// existing kinds is the part the user cannot see anywhere else in the dialog,
// because the Media Providers section is a different screen.
func conflictDetail(id string, sources, kinds []string) string {
	if media := nonLLMKinds(kinds); len(media) > 0 {
		return fmt.Sprintf(
			"provider id %q is already registered as %s (%s); promoting a chat node onto it would rewrite an existing media provider. Choose a different id, or remove it from Media Providers first",
			id, strings.Join(media, ", "), strings.Join(sources, ", "),
		)
	}
	return fmt.Sprintf(
		"provider id %q is already in the catalog (%s); choose a different id or remove the existing entry first",
		id, strings.Join(sources, ", "),
	)
}

// nonLLMKinds drops "llm" from a kind list. A provider that serves chat and
// images is a plain duplicate, not a media collision, so only the kinds that
// live under Media Providers count as a collision worth calling out.
func nonLLMKinds(kinds []string) []string {
	var out []string
	for _, k := range kinds {
		if k != "llm" {
			out = append(out, k)
		}
	}
	return out
}

// catalogIDsFromProviderCatalog returns the "id" values of every
// PROVIDER_CATALOG entry.
func catalogIDsFromProviderCatalog(path string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(b)
	anchor := "export const PROVIDER_CATALOG: ProviderCatalogItem[] = ["
	start := strings.Index(src, anchor)
	if start < 0 {
		return nil, fmt.Errorf("PROVIDER_CATALOG not found")
	}
	end := blockEnd(src, "]", start)
	if end < 0 {
		return nil, fmt.Errorf("PROVIDER_CATALOG closing bracket not found")
	}
	key := quote + "id" + quote + ": " + quote
	var ids []string
	for _, line := range strings.Split(src[start:end], newlineByte) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key) {
			continue
		}
		rest := trimmed[len(key):]
		if cut := strings.Index(rest, quote); cut > 0 {
			ids = append(ids, rest[:cut])
		}
	}
	return ids, nil
}

// catalogServiceKinds returns the serviceKinds declared by one PROVIDER_CATALOG
// entry, in file order.
//
// The read is deliberately narrow: it locates the entry by its id line, takes
// the block up to the next entry, and collects the quoted values inside the
// "serviceKinds" array. Any other JSON in the entry (modelsFetcher, regions,
// notice) is skipped because the keys are matched on a trimmed line prefix,
// the same rule the patchers use. A provider with no serviceKinds field
// reports no kinds; the web catalog defaults that to ["llm"], but the caller
// is a conflict message and saying "llm" for an entry that never declared it
// would overstate what the file says.
func catalogServiceKinds(path, id string) ([]string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	src := string(b)
	anchor := "export const PROVIDER_CATALOG: ProviderCatalogItem[] = ["
	start := strings.Index(src, anchor)
	if start < 0 {
		return nil, fmt.Errorf("PROVIDER_CATALOG not found")
	}
	end := blockEnd(src, "]", start)
	if end < 0 {
		return nil, fmt.Errorf("PROVIDER_CATALOG closing bracket not found")
	}
	body := src[start:end]

	idKey := quote + "id" + quote + ": " + quote + id + quote
	idAt := strings.Index(body, idKey)
	if idAt < 0 {
		return nil, nil
	}
	// The entry runs from the "id" line back to the nearest opening brace of a
	// catalog item and forward to the next one. Scanning forward for the next
	// id keeps a serviceKinds array of a later provider out of this result.
	itemStart := strings.LastIndex(body[:idAt], newlineByte+"  {")
	if itemStart < 0 {
		return nil, nil
	}
	itemEnd := strings.Index(body[idAt:], newlineByte+"  },")
	if itemEnd < 0 {
		return nil, nil
	}
	item := body[itemStart : idAt+itemEnd]

	kindsAt := strings.Index(item, quote+"serviceKinds"+quote)
	if kindsAt < 0 {
		return nil, nil
	}
	arrStart := strings.Index(item[kindsAt:], "[")
	if arrStart < 0 {
		return nil, nil
	}
	arrStart += kindsAt
	arrEnd := strings.Index(item[arrStart:], "]")
	if arrEnd < 0 {
		return nil, nil
	}

	var kinds []string
	rest := item[arrStart+1 : arrStart+arrEnd]
	for {
		open := strings.Index(rest, quote)
		if open < 0 {
			break
		}
		closeIdx := strings.Index(rest[open+1:], quote)
		if closeIdx < 0 {
			break
		}
		kinds = append(kinds, rest[open+1:open+1+closeIdx])
		rest = rest[open+1+closeIdx+1:]
	}
	return kinds, nil
}

// goRegistryIDs returns the keys of KnownProviders and of modelsListURL.
//
// Both maps are read with the same line scan the patchers use: a key is a
// trimmed line that starts with a quoted id followed by a colon, which no
// comment or string value in either map satisfies.
func goRegistryIDs(path string) (known []string, list []string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	src := string(b)

	knownStart := strings.Index(src, "var KnownProviders = map[string]ProviderConfig{")
	if knownStart >= 0 {
		if end := blockEnd(src, "}", knownStart); end > 0 {
			known = quotedKeys(src[knownStart:end])
		}
	}
	listStart := strings.Index(src, "var modelsListURL = map[string]string{")
	if listStart >= 0 {
		if end := blockEnd(src, "}", listStart); end > 0 {
			list = quotedKeys(src[listStart:end])
		}
	}
	return known, list, nil
}

// quotedKeys collects the first quoted token of every line that begins with one.
func quotedKeys(block string) []string {
	key := quote
	var out []string
	for _, line := range strings.Split(block, newlineByte) {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, key) {
			continue
		}
		rest := trimmed[len(key):]
		if cut := strings.Index(rest, key); cut > 0 {
			out = append(out, rest[:cut])
		}
	}
	return out
}
