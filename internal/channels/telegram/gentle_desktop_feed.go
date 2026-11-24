package telegram

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"nox9go/proxy/internal/config"
)

type gentleDesktopFeed struct {
	UpdatedAt       time.Time `json:"-"`
	UpdatedAtRaw    string    `json:"updatedAt"`
	Identity        struct {
		Cwd      string `json:"cwd"`
		AgentDir string `json:"agentDir"`
	} `json:"identity"`
	Activity struct {
		State        string `json:"state"`
		BlockedLabel string `json:"blockedLabel"`
	} `json:"activity"`
	PendingQuestion *gentleDesktopPendingQuestion `json:"pendingQuestion"`
}

type gentleDesktopPendingQuestion struct {
	Question   string `json:"question"`
	ToolCallID string `json:"toolCallId"`
	Options    []struct {
		Label       string `json:"label"`
		Description string `json:"description"`
	} `json:"options"`
}

func gentleDesktopSessionDirs() []string {
	home, err := os.UserHomeDir()
	if err != nil {
		return nil
	}
	var dirs []string
	if p := strings.TrimSpace(os.Getenv("GENTLE_SHELL_HOME")); p != "" {
		dirs = append(dirs, filepath.Join(p, "agent", "desktop", "sessions"))
	}
	dirs = append(dirs,
		filepath.Join(home, ".gentle-shell", "agent", "desktop", "sessions"),
		filepath.Join(home, ".pi", "agent", "desktop", "sessions"),
	)
	seen := make(map[string]struct{}, len(dirs))
	var out []string
	for _, d := range dirs {
		d = filepath.Clean(d)
		if _, ok := seen[d]; ok {
			continue
		}
		seen[d] = struct{}{}
		out = append(out, d)
	}
	return out
}

// GentleDesktopBlocker is an open choice question from a desktop feed snapshot.
type GentleDesktopBlocker struct {
	Title      string
	Choices    []HerdrChoice
	ToolCallID string
	SessionID  string
	AgentDir   string
}

// readGentleDesktopBlockedQuestion scans Gentle Shell desktop feed snapshots for an
// open ask_user_choice / ask_user_question (Herdr agent_status often stays idle).
func readGentleDesktopBlockedQuestion() (GentleDesktopBlocker, bool) {
	var empty GentleDesktopBlocker
	exclude := strings.TrimSpace(config.HerdrAgentExcludeCwd())
	var best *gentleDesktopFeed
	var bestPath string
	for _, dir := range gentleDesktopSessionDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if ent.IsDir() || !strings.HasSuffix(ent.Name(), ".json") {
				continue
			}
			path := filepath.Join(dir, ent.Name())
			feed, err := parseGentleDesktopFeed(path)
			if err != nil || feed.PendingQuestion == nil {
				continue
			}
			if feed.Activity.State != "blocked" && feed.Activity.State != "waiting" {
				continue
			}
			if exclude != "" && gentlePathsEqual(feed.Identity.Cwd, exclude) {
				continue
			}
			if best == nil || feed.UpdatedAt.After(best.UpdatedAt) {
				dup := feed
				best = &dup
				bestPath = path
			}
		}
	}
	_ = bestPath
	if best == nil || best.PendingQuestion == nil {
		return empty, false
	}
	pq := best.PendingQuestion
	opts := pq.Options
	if len(opts) == 0 || len(opts) > 9 {
		return empty, false
	}
	title := strings.TrimSpace(pq.Question)
	if title == "" {
		title = strings.TrimSpace(best.Activity.BlockedLabel)
	}
	choices := make([]HerdrChoice, 0, len(opts))
	for i, o := range opts {
		label := strings.TrimSpace(o.Label)
		if label == "" {
			label = strings.TrimSpace(o.Description)
		}
		if label == "" {
			continue
		}
		choices = append(choices, HerdrChoice{
			Label: truncateHerdrLabel(label, 48),
			Keys:  herdrCursorKeys(0, i),
		})
	}
	choices = dedupeHerdrChoices(choices)
	if len(choices) == 0 {
		return empty, false
	}
	sessionID := strings.TrimSuffix(filepath.Base(bestPath), ".json")
	return GentleDesktopBlocker{
		Title:      title,
		Choices:    choices,
		ToolCallID: strings.TrimSpace(pq.ToolCallID),
		SessionID:  sessionID,
		AgentDir:   strings.TrimSpace(best.Identity.AgentDir),
	}, true
}

func parseGentleDesktopFeed(path string) (gentleDesktopFeed, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return gentleDesktopFeed{}, err
	}
	var feed gentleDesktopFeed
	if err := json.Unmarshal(raw, &feed); err != nil {
		return gentleDesktopFeed{}, err
	}
	if feed.UpdatedAtRaw != "" {
		if t, err := time.Parse(time.RFC3339Nano, feed.UpdatedAtRaw); err == nil {
			feed.UpdatedAt = t
		} else if t, err := time.Parse(time.RFC3339, feed.UpdatedAtRaw); err == nil {
			feed.UpdatedAt = t
		}
	}
	if feed.UpdatedAt.IsZero() {
		if fi, err := os.Stat(path); err == nil {
			feed.UpdatedAt = fi.ModTime()
		}
	}
	return feed, nil
}

func gentlePathsEqual(a, b string) bool {
	a = strings.TrimSpace(a)
	b = strings.TrimSpace(b)
	if a == "" || b == "" {
		return false
	}
	return strings.EqualFold(filepath.Clean(a), filepath.Clean(b))
}

// ponytail: scan is O(n sessions); fine for a small desktop/sessions folder.
func gentleDesktopFeedPaths() []string {
	var paths []string
	for _, dir := range gentleDesktopSessionDirs() {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, ent := range entries {
			if !ent.IsDir() && strings.HasSuffix(ent.Name(), ".json") {
				paths = append(paths, filepath.Join(dir, ent.Name()))
			}
		}
	}
	sort.Strings(paths)
	return paths
}