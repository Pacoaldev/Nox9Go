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
	conflict.Detail = fmt.Sprintf(
		"provider id %q is already in the catalog (%s); choose a different id or remove the existing entry first",
		id, strings.Join(conflict.Source, ", "),
	)
	return conflict, nil
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
