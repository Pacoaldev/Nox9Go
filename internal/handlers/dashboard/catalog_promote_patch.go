package dashboard

import (
	"errors"
	"strings"
)

// Separator literals are built from a rune rather than written as escapes: the
// patchers have to reproduce the exact bytes a Go map, a JSON array and a
// Svelte set use, and spelling them once here keeps every insertion readable.
const newlineByte = string(rune(10))

func nll() string { return newlineByte }

// closeBraceLn is the newline plus the brace that ends an indented block. Both
// Go maps and the two TS object literals in this promotion close that way.
func closeBraceLn() string { return newlineByte + "}" }

func closeBrackLn() string { return newlineByte + "]" }

// quote is the double quote the Go maps and JSON catalog entries use.
const quote = string(rune(34))

// squote is the single quote ICON_FILES and ModelsListURLs use as TS keys.
const squote = string(rune(39))

// This file holds one patcher per catalog destination. Each takes the current
// file text and returns the patched text, or the same text when the entry is
// already present. Returning the input unchanged is what makes promotion
// idempotent: re-running it after a partial attempt finishes the job without
// duplicating anything.
//
// Presence is decided by scanning lines for an exact key rather than by a
// regexp: the ids are already validated as [a-z0-9-], so a quoted-key match on
// a trimmed line cannot collide with a comment or a string value.

// hasLineKey reports whether any line, once trimmed, starts with key.
func hasLineKey(src, key string) bool {
	for _, line := range strings.Split(src, newlineByte) {
		if strings.HasPrefix(strings.TrimSpace(line), key) {
			return true
		}
	}
	return false
}

// lineEndingOf reports the newline sequence a file uses. A file containing any
// CRLF is treated as CRLF throughout: mixed endings are a checkout artefact, and
// following the dominant form keeps every insertion consistent.
func lineEndingOf(src string) string {
	if strings.Contains(src, string(rune(13))+newlineByte) {
		return string(rune(13)) + newlineByte
	}
	return newlineByte
}

// blockEnd returns the index of the newline that precedes the first line equal
// to delim, searching forward from start. Anchoring on a whole line rather than
// on the first occurrence of the delimiter protects a map whose values contain
// the same character.
func blockEnd(src, delim string, start int) int {
	needle := newlineByte + delim
	idx := start
	for {
		rel := strings.Index(src[idx:], needle)
		if rel < 0 {
			return -1
		}
		abs := idx + rel
		after := abs + len(needle)
		// The delimiter must end its line, possibly followed by a CR.
		if after >= len(src) {
			return abs
		}
		switch src[after] {
		case newlineByte[0], string(rune(13))[0]:
			return abs
		}
		idx = abs + 1
	}
}

// patchKnownProviders inserts the chat entry into the KnownProviders map.
func patchKnownProviders(src string, p CatalogPromotion) (string, error) {
	if hasLineKey(src, quote+p.ID+quote+":") {
		return src, nil
	}
	// The map ends where the RetryableStatusCodes declaration begins. Anchoring
	// on that comment rather than on a brace keeps the patch correct when a
	// comment is added inside the map above it.
	marker := "// RetryableStatusCodes are HTTP status codes"
	markerIdx := strings.Index(src, marker)
	if markerIdx < 0 {
		return "", errors.New("KnownProviders map end not found")
	}
	// Trim the blank line and any CR so the search is independent of which line
	// ending the checkout uses: CRLF is what these sources have on Windows.
	head := strings.TrimRight(src[:markerIdx], newlineByte+string(rune(13)))
	if !strings.HasSuffix(head, "}") {
		return "", errors.New("KnownProviders closing brace not found")
	}
	// The insertion goes before the newline that precedes the closing brace, so
	// the entry lands inside the map.
	end := len(head) - 1
	for end > 0 && (head[end-1] == '	' || head[end-1] == ' ') {
		end--
	}
	if end > 0 && head[end-1] == string(rune(13))[0] {
		end--
	}
	if end > 0 {
		end--
	}
	eol := lineEndingOf(src)

	authHeader := p.AuthHeader
	if authHeader == "" {
		authHeader = "Authorization"
	}
	authScheme := p.AuthScheme
	if authScheme == "" {
		authScheme = "bearer"
	}
	entry := eol +
		"	// Promoted from a custom provider node by the dashboard Mover a Catalogo" + eol +
		"	// action. Kept in the same shape as its hand-written neighbours so the" + eol +
		"	// generic probe and the executor resolve it without special cases." + eol +
		"	" + goString(p.ID) + ": {" + eol +
		"		BaseURL:    " + goString(p.ChatURL) + "," + eol +
		"		AuthHeader: " + goString(authHeader) + "," + eol +
		"		AuthScheme: " + goString(authScheme) + "," + eol +
		"	},"
	return src[:end] + entry + src[end:], nil
}

// patchModelsListURL inserts the /models discovery endpoint.
func patchModelsListURL(src string, p CatalogPromotion) (string, error) {
	if p.ModelsURL == "" {
		return src, nil
	}
	start := strings.Index(src, "var modelsListURL = map[string]string{")
	if start < 0 {
		return "", errors.New("modelsListURL map not found")
	}
	closeIdx := strings.Index(src[start:], closeBraceLn())
	if closeIdx < 0 {
		return "", errors.New("modelsListURL closing brace not found")
	}
	closeIdx += start
	if hasLineKey(src[start:closeIdx], quote+p.ID+quote+":") {
		return src, nil
	}
	entry := newlineByte + "	" + goString(p.ID) + ": " + goString(p.ModelsURL) + ","
	return src[:closeIdx] + entry + src[closeIdx:], nil
}

// patchProviderModels inserts the registry model list.
func patchProviderModels(src string, p CatalogPromotion) (string, error) {
	if len(p.Models) == 0 {
		return src, nil
	}
	start := strings.Index(src, "var ProviderModels = map[string][]string{")
	if start < 0 {
		return "", errors.New("ProviderModels map not found")
	}
	closeIdx := strings.Index(src[start:], closeBraceLn())
	if closeIdx < 0 {
		return "", errors.New("ProviderModels closing brace not found")
	}
	closeIdx += start
	if hasLineKey(src[start:closeIdx], quote+p.ID+quote+":") {
		return src, nil
	}
	quoted := make([]string, 0, len(p.Models))
	for _, m := range p.Models {
		quoted = append(quoted, goString(m))
	}
	entry := newlineByte + "	" + goString(p.ID) + ": {" + strings.Join(quoted, ", ") + "},"
	return src[:closeIdx] + entry + src[closeIdx:], nil
}

// patchProviderCatalog appends the PROVIDER_CATALOG entry the grid renders.
func patchProviderCatalog(src string, p CatalogPromotion) (string, error) {
	anchor := "export const PROVIDER_CATALOG: ProviderCatalogItem[] = ["
	start := strings.Index(src, anchor)
	if start < 0 {
		return "", errors.New("PROVIDER_CATALOG not found")
	}
	closeIdx := strings.Index(src[start:], closeBrackLn())
	if closeIdx < 0 {
		return "", errors.New("PROVIDER_CATALOG closing bracket not found")
	}
	closeIdx += start
	if hasLineKey(src[start:closeIdx], quote+"id"+quote+": "+quote+p.ID+quote) {
		return src, nil
	}

	kinds := p.ServiceKinds
	if len(kinds) == 0 {
		kinds = []string{"llm"}
	}
	kindList := make([]string, 0, len(kinds))
	for _, k := range kinds {
		kindList = append(kindList, "      "+goString(k))
	}
	notice := ""
	if p.NoticeText != "" || p.APIKeyURL != "" {
		parts := make([]string, 0, 2)
		if p.NoticeText != "" {
			parts = append(parts, quote+"text"+quote+":"+goString(p.NoticeText))
		}
		if p.APIKeyURL != "" {
			parts = append(parts, quote+"apiKeyUrl"+quote+":"+goString(p.APIKeyURL))
		}
		notice = newlineByte + "    " + quote + "notice" + quote + ": {" + strings.Join(parts, ", ") + "},"
	}

	alias := p.Alias
	if alias == "" {
		alias = p.ID
	}
	color := p.Color
	if color == "" {
		color = "#64748B"
	}
	icon := p.Icon
	if icon == "" {
		icon = "hub"
	}

	entry := newlineByte +
		"  {" + newlineByte +
		"    " + quote + "id" + quote + ": " + goString(p.ID) + "," + newlineByte +
		"    " + quote + "name" + quote + ": " + goString(p.Name) + "," + newlineByte +
		"    " + quote + "category" + quote + ": " + goString(p.Category) + "," + newlineByte +
		"    " + quote + "alias" + quote + ": " + goString(alias) + "," + newlineByte +
		"    " + quote + "color" + quote + ": " + goString(color) + "," + newlineByte +
		"    " + quote + "icon" + quote + ": " + goString(icon) + "," + newlineByte +
		"    " + quote + "website" + quote + ": " + goString(p.Website) + "," + notice + newlineByte +
		"    " + quote + "authType" + quote + ": " + quote + "apikey" + quote + "," + newlineByte +
		"    " + quote + "noAuth" + quote + ": false," + newlineByte +
		"    " + quote + "serviceKinds" + quote + ": [" + newlineByte +
		strings.Join(kindList, ","+newlineByte) + newlineByte +
		"    ]" + newlineByte +
		"  },"

	return src[:closeIdx] + entry + newlineByte + src[closeIdx:], nil
}

// patchIconFiles records the real artwork filename.
func patchIconFiles(src string, p CatalogPromotion) (string, error) {
	start := strings.Index(src, "const ICON_FILES: Record<string, string> = {")
	if start < 0 {
		return "", errors.New("ICON_FILES not found")
	}
	closeIdx := strings.Index(src[start:], closeBraceLn())
	if closeIdx < 0 {
		return "", errors.New("ICON_FILES closing brace not found")
	}
	closeIdx += start
	if hasLineKey(src[start:closeIdx], squote+p.ID+squote+":") {
		return src, nil
	}
	entry := newlineByte + "  " + squote + p.ID + squote + ": " + squote + p.ID + ".png" + squote + ","
	return src[:closeIdx] + entry + src[closeIdx:], nil
}

// patchModelsListURLs adds the id to the Svelte set that gates the import
// buttons.
func patchModelsListURLs(src string, p CatalogPromotion) (string, error) {
	if p.ModelsURL == "" {
		return src, nil
	}
	start := strings.Index(src, "const ModelsListURLs = new Set([")
	if start < 0 {
		return "", errors.New("ModelsListURLs set not found")
	}
	closeIdx := strings.Index(src[start:], "])")
	if closeIdx < 0 {
		return "", errors.New("ModelsListURLs closing bracket not found")
	}
	closeIdx += start
	if hasLineKey(src[start:closeIdx], squote+p.ID+squote+",") {
		return src, nil
	}
	entry := newlineByte + "    " + squote + p.ID + squote + ","
	return src[:closeIdx] + entry + src[closeIdx:], nil
}
