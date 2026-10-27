package dashboard

import (
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"

	json "encoding/json/v2"

	"nox9go/proxy/internal/handlerutil"
)

// CatalogPromotion is the payload the dashboard sends when it moves a custom
// provider node into the built-in catalog.
//
// The six destinations below are independent layers that fail in silence in
// their own way: a provider registered only in the web catalog still answers
// "validation not supported", and one missing from modelsListURL offers a Sync
// button with nothing to sync from. Writing all six from one pass is the whole
// point of this file.
type CatalogPromotion struct {
	// ID is the catalog provider id ("moonshot"), which is also the value
	// stored in providerConnections.provider once the connections move.
	ID string `json:"id"`
	// NodeID is the custom node being promoted (openai-compatible-chat-...).
	NodeID string `json:"nodeId"`
	Name   string `json:"name"`
	Alias  string `json:"alias"`
	Color  string `json:"color"`
	Icon   string `json:"icon"`
	// Category is one of the PROVIDER_CATALOG categories.
	Category string `json:"category"`
	// Website and the notice fields feed the catalog card.
	Website    string `json:"website"`
	NoticeText string `json:"noticeText"`
	APIKeyURL  string `json:"apiKeyUrl"`
	// ServiceKinds maps to PROVIDER_CATALOG serviceKinds (["llm"], ...).
	ServiceKinds []string `json:"serviceKinds"`

	// ChatURL is the completions endpoint written to KnownProviders.BaseURL.
	ChatURL string `json:"chatUrl"`
	// ModelsURL is the listing endpoint written to modelsListURL. Empty means
	// the provider has no OpenAI-shaped catalogue and only the chat entry is
	// written (Replicate is the case that motivated the field).
	ModelsURL string `json:"modelsUrl"`
	// Models are written to ProviderModels as a snapshot; the live import still
	// supersedes them.
	Models []string `json:"models"`

	// AuthHeader and AuthScheme default to Authorization and bearer.
	AuthHeader string `json:"authHeader"`
	AuthScheme string `json:"authScheme"`
}

// PromotionResult reports what was written, so the dialog can show a real diff
// summary instead of a generic success toast.
type PromotionResult struct {
	Files   []string `json:"files"`
	Build   string   `json:"build"`
	BuildOK bool     `json:"buildOk"`
	Output  string   `json:"output,omitempty"`
}

// promotionStep is one destination file and the patch to apply to it.
type promotionStep struct {
	rel string
	fn  func(string) (string, error)
}

// promotionSteps lists the six destinations in write order. providers.go
// appears twice: KnownProviders and modelsListURL are separate maps in the same
// file, and the second pass must see the first pass's output.
func promotionSteps(p CatalogPromotion) []promotionStep {
	return []promotionStep{
		{"internal/providers/providers.go", func(s string) (string, error) { return patchKnownProviders(s, p) }},
		{"internal/providers/providers.go", func(s string) (string, error) { return patchModelsListURL(s, p) }},
		{"internal/providers/registry_models.go", func(s string) (string, error) { return patchProviderModels(s, p) }},
		{"web/src/lib/providers.ts", func(s string) (string, error) { return patchProviderCatalog(s, p) }},
		{"web/src/components/connections/types.ts", func(s string) (string, error) { return patchIconFiles(s, p) }},
		{"web/src/components/connections/ProviderDetailView.svelte", func(s string) (string, error) { return patchModelsListURLs(s, p) }},
	}
}

// RepoRoot resolves the source tree the running binary was built from. The
// dashboard ships as one binary that may run from anywhere, so the root is
// found from the executable's directory upwards and then from the working
// directory. An empty answer means no source tree is reachable, which the
// caller turns into an error rather than a silent no-op.
func RepoRoot() string {
	markers := []string{
		filepath.Join("internal", "providers", "providers.go"),
		filepath.Join("web", "src", "lib", "providers.ts"),
	}
	looksLikeRoot := func(dir string) bool {
		for _, m := range markers {
			if _, err := os.Stat(filepath.Join(dir, m)); err != nil {
				return false
			}
		}
		return true
	}
	walk := func(start string) string {
		dir := start
		for i := 0; i < 6 && dir != "" && dir != filepath.Dir(dir); i++ {
			if looksLikeRoot(dir) {
				return dir
			}
			dir = filepath.Dir(dir)
		}
		return ""
	}

	if exe, err := os.Executable(); err == nil {
		if root := walk(filepath.Dir(exe)); root != "" {
			return root
		}
	}
	if wd, err := os.Getwd(); err == nil {
		return walk(wd)
	}
	return ""
}

var promotionIDPattern = regexp.MustCompile("^[a-z0-9][a-z0-9-]*$")

// validatePromotion rejects a payload that cannot produce a coherent catalog
// entry, before any file is touched.
func validatePromotion(p *CatalogPromotion) error {
	if strings.TrimSpace(p.ID) == "" {
		return errors.New("missing provider id")
	}
	if !promotionIDPattern.MatchString(p.ID) {
		return fmt.Errorf("provider id %q must be lowercase letters, digits and dashes", p.ID)
	}
	if strings.TrimSpace(p.NodeID) == "" {
		return errors.New("missing node id")
	}
	if !strings.HasPrefix(p.ChatURL, "https://") {
		return fmt.Errorf("chat URL must be https, got %q", p.ChatURL)
	}
	if p.ModelsURL != "" && !strings.HasPrefix(p.ModelsURL, "https://") {
		return fmt.Errorf("models URL must be https, got %q", p.ModelsURL)
	}
	switch p.Category {
	case "apikey", "oauth", "free", "freeTier", "webCookie":
	default:
		return fmt.Errorf("unsupported category %q", p.Category)
	}
	return nil
}

// PreviewCatalogPromotion reports which files would change, without writing
// anything. The dialog calls it so a promotion the patchers would refuse (a
// moved anchor, a read-only tree) is caught before the user commits.
func PreviewCatalogPromotion(root string, p CatalogPromotion) ([]string, error) {
	if err := validatePromotion(&p); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("source tree not found")
	}
	var changed []string
	seen := map[string]bool{}
	for _, st := range promotionSteps(p) {
		b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(st.rel)))
		if err != nil {
			return nil, fmt.Errorf("read %s: %w", st.rel, err)
		}
		next, err := st.fn(string(b))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", st.rel, err)
		}
		if next != string(b) && !seen[st.rel] {
			seen[st.rel] = true
			changed = append(changed, st.rel)
		}
	}
	return changed, nil
}

// ApplyCatalogPromotion writes the six destinations and, when requested,
// rebuilds both binaries. Every file is read and patched in memory first and
// only a fully patched set is flushed, so a mid-way error cannot leave the tree
// with a Go file that no longer compiles against the others.
func ApplyCatalogPromotion(root string, p CatalogPromotion, rebuild bool) (*PromotionResult, error) {
	if err := validatePromotion(&p); err != nil {
		return nil, err
	}
	if root == "" {
		return nil, errors.New("source tree not found: run the dashboard from the repository checkout")
	}

	staged := map[string]string{}
	var order []string
	for _, st := range promotionSteps(p) {
		cur, ok := staged[st.rel]
		if !ok {
			b, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(st.rel)))
			if err != nil {
				return nil, fmt.Errorf("read %s: %w", st.rel, err)
			}
			cur = string(b)
		}
		next, err := st.fn(cur)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", st.rel, err)
		}
		if next == cur {
			continue
		}
		if _, seen := staged[st.rel]; !seen {
			order = append(order, st.rel)
		}
		staged[st.rel] = next
	}

	res := &PromotionResult{Files: order, BuildOK: true}
	if len(staged) == 0 {
		res.Build = "skipped (entries already present)"
		return res, nil
	}

	for _, rel := range order {
		if err := writeFileAtomic(filepath.Join(root, filepath.FromSlash(rel)), []byte(staged[rel])); err != nil {
			return nil, fmt.Errorf("write %s: %w", rel, err)
		}
	}

	if !rebuild {
		res.Build = "skipped (rebuild=false)"
		return res, nil
	}
	out, ok := runPromotionBuild(root)
	res.Build = "make build && make tray"
	res.BuildOK = ok
	res.Output = out
	return res, nil
}

// writeFileAtomic writes through a temporary file in the same directory so a
// crash mid-write cannot truncate a source file the running binary was built
// from.
func writeFileAtomic(path string, data []byte) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, ".promote-*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if st, err := os.Stat(path); err == nil {
		_ = os.Chmod(name, st.Mode())
	}
	return os.Rename(name, path)
}

// runPromotionBuild rebuilds the web bundle and both binaries. web-build is
// fingerprint-driven, so an unchanged frontend is a no-op there and a changed
// one is picked up without the caller having to know.
func runPromotionBuild(root string) (string, bool) {
	var log strings.Builder
	for _, target := range []string{"build", "tray"} {
		cmd := exec.Command("make", target)
		cmd.Dir = root
		out, err := cmd.CombinedOutput()
		log.Write(out)
		if err != nil {
			log.WriteString(newlineByte + err.Error())
			return log.String(), false
		}
	}
	return log.String(), true
}

// HandlePromoteToCatalog handles POST /api/provider-nodes/promote.
//
// A dry run answers with the files that would change and writes nothing. The
// full call writes the six destinations, rebuilds, then moves the node's
// connections and custom models onto the new provider id and deletes the node.
// The migration runs only after a successful write, so a failed patch never
// leaves connections pointing at an id the registry does not know.
func (h *DashboardHandler) HandlePromoteToCatalog(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "failed to read body")
		return
	}
	defer r.Body.Close()

	var req struct {
		CatalogPromotion
		DryRun  bool  `json:"dryRun"`
		Rebuild *bool `json:"rebuild"`
	}
	if err := json.Unmarshal(body, &req); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, "invalid JSON")
		return
	}
	if err := validatePromotion(&req.CatalogPromotion); err != nil {
		handlerutil.WriteJSONError(w, http.StatusBadRequest, err.Error())
		return
	}

	node, _, err := h.Repo.GetProviderNodeByID(req.NodeID)
	if err != nil || node == nil {
		handlerutil.WriteJSONError(w, http.StatusNotFound, "provider node not found")
		return
	}

	root := RepoRoot()
	if root == "" {
		handlerutil.WriteJSONError(w, http.StatusConflict,
			"source tree not found: run the dashboard from the repository checkout")
		return
	}

	// A taken id is refused before anything is written, dry run included: the
	// patchers are idempotent, so an existing entry would otherwise skip the
	// files it recognises, let the migration run, and delete the node while
	// reporting success.
	conflict, err := catalogEntryExists(root, req.ID)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if conflict != nil {
		handlerutil.WriteJSON(w, http.StatusConflict, map[string]any{
			"error":   conflict.Detail,
			"code":    "PROVIDER_ALREADY_IN_CATALOG",
			"id":      conflict.ID,
			"sources": conflict.Source,
		})
		return
	}

	if req.DryRun {
		files, err := PreviewCatalogPromotion(root, req.CatalogPromotion)
		if err != nil {
			handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
			return
		}
		handlerutil.WriteJSON(w, http.StatusOK, map[string]any{
			"dryRun": true,
			"files":  files,
			"repo":   root,
		})
		return
	}

	rebuild := true
	if req.Rebuild != nil {
		rebuild = *req.Rebuild
	}
	res, err := ApplyCatalogPromotion(root, req.CatalogPromotion, rebuild)
	if err != nil {
		handlerutil.WriteJSONError(w, http.StatusInternalServerError, err.Error())
		return
	}

	moved, modelsMoved, moveErr := h.MoveNodeConnectionsToCatalog(r.Context(), req.NodeID, req.ID)
	payload := map[string]any{
		"files":       res.Files,
		"build":       res.Build,
		"buildOk":     res.BuildOK,
		"output":      res.Output,
		"moved":       moved,
		"modelsMoved": modelsMoved,
	}
	if moveErr != nil {
		payload["warning"] = "catalog written but node migration failed: " + moveErr.Error()
	}
	handlerutil.WriteJSON(w, http.StatusOK, payload)
}
