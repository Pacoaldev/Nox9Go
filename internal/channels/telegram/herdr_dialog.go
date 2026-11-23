package telegram

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
)

// HerdrChoice is one Telegram button that maps to herdr agent send-keys tokens.
type HerdrChoice struct {
	Label string
	Keys  []string
}

var herdrNumberedLine = regexp.MustCompile(`^\s*(\d+)\.\s+(.+)$`)

// ParseHerdrDialog extracts actionable choices from a Pi/Herdr terminal tail.
// Logic is adapted from spancerxing/herdr-telegram-bridge (MIT) — cursor,
// numbered and permission prompts only.
func ParseHerdrDialog(screen string) (title string, choices []HerdrChoice) {
	lines := splitLines(screen)
	if len(lines) == 0 {
		return "", nil
	}

	if ch := parseHerdrCursorDialog(lines); len(ch) > 0 {
		return herdrDialogTitle(lines, ch), ch
	}
	if ch := parseHerdrNumberedDialog(lines); len(ch) > 0 {
		return herdrDialogTitle(lines, ch), ch
	}
	if ch := parseHerdrPermissionDialog(lines); len(ch) > 0 {
		return herdrDialogTitle(lines, ch), ch
	}
	if title, ch := parseAskUserChoiceTranscript(lines); len(ch) > 0 {
		return title, ch
	}
	return "", nil
}

func splitLines(s string) []string {
	raw := strings.Split(s, "\n")
	out := make([]string, 0, len(raw))
	for _, ln := range raw {
		out = append(out, strings.TrimRight(ln, "\r"))
	}
	return out
}

func parseHerdrCursorDialog(lines []string) []HerdrChoice {
	footer := herdrNavigateFooter(lines)
	if footer < 0 {
		return nil
	}
	menu := collectHerdrMenuLines(lines, footer)
	if len(menu) == 0 {
		return nil
	}
	type cursorOpt struct {
		label    string
		selected bool
	}
	var options []cursorOpt
	for i := 0; i < len(menu); i++ {
		raw := menu[i]
		label, sel, ok := stripCursorGlyph(raw)
		if !ok {
			continue
		}
		// Pi shows short parents (Confirmar/Cancelar) with a more indented subtitle line.
		if i+1 < len(menu) && countLeadingSpaces(menu[i+1]) > countLeadingSpaces(raw) {
			sub, _, subOK := stripCursorGlyph(menu[i+1])
			if subOK && isShortMenuParent(label) {
				label = sub
				i++
			}
		}
		options = append(options, cursorOpt{label: label, selected: sel})
	}
	selected := 0
	for i, o := range options {
		if o.selected {
			selected = i
			break
		}
	}
	if len(options) == 0 || len(options) > 9 {
		return nil
	}

	choices := make([]HerdrChoice, 0, len(options))
	for i, o := range options {
		choices = append(choices, HerdrChoice{
			Label: truncateHerdrLabel(o.label, 48),
			Keys:  herdrCursorKeys(selected, i),
		})
	}
	return dedupeHerdrChoices(choices)
}

func herdrNavigateFooter(lines []string) int {
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-10; i-- {
		low := strings.ToLower(lines[i])
		if strings.Contains(low, "navigate") && strings.Contains(low, "select") {
			return i
		}
	}
	return -1
}

func collectHerdrMenuLines(lines []string, footer int) []string {
	var block []string
	for i := footer - 1; i >= 0 && i >= footer-20; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			if len(block) > 0 {
				break
			}
			continue
		}
		if isHerdrBoxSeparator(t) {
			break
		}
		if strings.HasSuffix(t, "?") && !strings.Contains(lines[i], "→") {
			break
		}
		block = append([]string{lines[i]}, block...)
	}
	return block
}

func countLeadingSpaces(s string) int {
	n := 0
	for _, r := range s {
		if r == ' ' {
			n++
			continue
		}
		if r == '\t' {
			n += 4
			continue
		}
		break
	}
	return n
}

func isShortMenuParent(label string) bool {
	switch strings.ToLower(strings.TrimSpace(label)) {
	case "confirmar", "cancelar", "confirm", "cancel":
		return true
	default:
		return false
	}
}

func isHerdrBoxSeparator(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return false
	}
	letters := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
	}
	return letters < 2 && strings.Contains(s, "─")
}

func stripCursorGlyph(line string) (label string, selected bool, ok bool) {
	trim := strings.TrimSpace(line)
	if trim == "" || isHerdrOptionNoise(trim) || strings.HasSuffix(trim, "?") {
		return "", false, false
	}
	for _, g := range []string{"→", "›", "❯", ">"} {
		if strings.HasPrefix(trim, g) {
			label = strings.TrimSpace(strings.TrimPrefix(trim, g))
			if label == "" {
				return "", false, false
			}
			return label, true, true
		}
	}
	// Pi marks unselected rows with leading whitespace in the raw line.
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		if isHerdrOptionNoise(trim) {
			return "", false, false
		}
		return trim, false, true
	}
	return "", false, false
}

func isHerdrOptionNoise(s string) bool {
	s = strings.TrimSpace(s)
	if s == "" {
		return true
	}
	letters := 0
	box := 0
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			letters++
		}
		switch r {
		case '│', '┃', '─', '┌', '┐', '└', '┘', '├', '┤', '╭', '╮', '╰', '╯', '■', '□', '●', '○', '━':
			box++
		}
	}
	if box > 0 && letters < 4 {
		return true
	}
	low := strings.ToLower(s)
	if strings.Contains(low, "agente bloqueado") {
		return true
	}
	if strings.Contains(low, "type your answer") || strings.Contains(low, "waiting for input") {
		return true
	}
	return false
}

func dedupeHerdrChoices(choices []HerdrChoice) []HerdrChoice {
	seen := make(map[string]struct{}, len(choices))
	out := make([]HerdrChoice, 0, len(choices))
	for _, ch := range choices {
		low := strings.ToLower(strings.TrimSpace(ch.Label))
		if _, dup := seen[low]; dup {
			continue
		}
		seen[low] = struct{}{}
		out = append(out, ch)
	}
	return out
}

func herdrCursorKeys(selected, target int) []string {
	keys := make([]string, 0, 4)
	switch {
	case target > selected:
		for range target - selected {
			keys = append(keys, "down")
		}
	case target < selected:
		for range selected - target {
			keys = append(keys, "up")
		}
	}
	keys = append(keys, "enter")
	return keys
}

func parseHerdrNumberedDialog(lines []string) []HerdrChoice {
	type numbered struct {
		n     int
		label string
	}
	var found []numbered
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-20; i-- {
		m := herdrNumberedLine.FindStringSubmatch(lines[i])
		if m == nil {
			if len(found) > 0 {
				break
			}
			continue
		}
		n, _ := strconv.Atoi(m[1])
		if n < 1 || n > 9 {
			continue
		}
		found = append([]numbered{{n: n, label: strings.TrimSpace(m[2])}}, found...)
	}
	if len(found) == 0 {
		return nil
	}
	choices := make([]HerdrChoice, 0, len(found))
	for _, item := range found {
		choices = append(choices, HerdrChoice{
			Label: truncateHerdrLabel(item.label, 48),
			Keys:  []string{strconv.Itoa(item.n)},
		})
	}
	return choices
}

func parseHerdrPermissionDialog(lines []string) []HerdrChoice {
	joined := strings.ToLower(strings.Join(lines, "\n"))
	if !strings.Contains(joined, "allow once") && !strings.Contains(joined, "allow guarded") {
		return nil
	}
	var choices []HerdrChoice
	for _, ln := range lines {
		t := strings.TrimSpace(ln)
		low := strings.ToLower(t)
		switch {
		case strings.Contains(low, "allow once"):
			choices = append(choices, HerdrChoice{Label: "Allow once", Keys: []string{"enter"}})
		case strings.Contains(low, "allow for this session"):
			choices = append(choices, HerdrChoice{Label: "Allow session", Keys: []string{"down", "enter"}})
		case strings.Contains(low, "deny") || strings.Contains(low, "reject"):
			choices = append(choices, HerdrChoice{Label: "Deny", Keys: []string{"esc"}})
		}
	}
	if len(choices) == 0 {
		choices = []HerdrChoice{
			{Label: "Yes", Keys: []string{"y"}},
			{Label: "No", Keys: []string{"n"}},
		}
	}
	return choices
}

func herdrDialogTitle(lines []string, choices []HerdrChoice) string {
	if len(choices) == 0 {
		return ""
	}
	footer := len(lines)
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-8; i-- {
		low := strings.ToLower(lines[i])
		if strings.Contains(low, "navigate") && strings.Contains(low, "select") {
			footer = i
			break
		}
	}
	for i := footer - 1; i >= 0 && i >= footer-20; i-- {
		t := strings.TrimSpace(lines[i])
		if t == "" {
			continue
		}
		if _, _, ok := stripCursorGlyph(lines[i]); ok || herdrNumberedLine.MatchString(t) {
			continue
		}
		if isHerdrOptionNoise(t) || isShortMenuParent(t) {
			continue
		}
		return t
	}
	return ""
}

var (
	askUserChoiceLine     = regexp.MustCompile(`(?i)ask_user_choice\s+(.+?)\s*\((.+)\)\s*$`)
	askUserChoiceAnswered = regexp.MustCompile(`(?m)^\s*✓\s*\d+\.`)
)

// parseAskUserChoiceTranscript recovers Pi confirm menus from the scrollback when
// Herdr read omits the live TUI overlay (ask_user_choice / agent_status idle).
func parseAskUserChoiceTranscript(lines []string) (title string, choices []HerdrChoice) {
	idx := -1
	var m []string
	for i := len(lines) - 1; i >= 0 && i >= len(lines)-80; i-- {
		if !strings.Contains(lines[i], "ask_user_choice") {
			continue
		}
		m = askUserChoiceLine.FindStringSubmatch(strings.TrimSpace(lines[i]))
		if len(m) == 3 {
			idx = i
			break
		}
	}
	if idx < 0 {
		return "", nil
	}
	// A ✓ N. line after the tool call means the operator already answered.
	for i := idx + 1; i < len(lines); i++ {
		if askUserChoiceAnswered.MatchString(lines[i]) {
			return "", nil
		}
	}
	title = strings.TrimSpace(m[1])
	opts := splitPiParenChoiceOptions(strings.TrimSpace(m[2]))
	if len(opts) == 0 {
		return "", nil
	}
	choices = make([]HerdrChoice, 0, len(opts))
	for i, label := range opts {
		choices = append(choices, HerdrChoice{
			Label: truncateHerdrLabel(label, 48),
			Keys:  herdrCursorKeys(0, i),
		})
	}
	return title, dedupeHerdrChoices(choices)
}

func splitPiParenChoiceOptions(inner string) []string {
	inner = strings.TrimSpace(inner)
	if inner == "" {
		return nil
	}
	const noSep = ", No,"
	if i := strings.Index(inner, noSep); i >= 0 {
		first := strings.TrimSpace(inner[:i])
		second := strings.TrimSpace("No," + inner[i+len(noSep):])
		return []string{first, second}
	}
	const yesSep = ", Yes,"
	if i := strings.Index(inner, yesSep); i >= 0 {
		first := strings.TrimSpace(inner[:i])
		second := strings.TrimSpace("Yes," + inner[i+len(yesSep):])
		return []string{first, second}
	}
	parts := strings.Split(inner, ",")
	if len(parts) <= 2 {
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if t := strings.TrimSpace(p); t != "" {
				out = append(out, t)
			}
		}
		return out
	}
	return []string{strings.TrimSpace(inner)}
}

func truncateHerdrLabel(s string, maxRunes int) string {
	s = strings.TrimSpace(s)
	r := []rune(s)
	if len(r) <= maxRunes {
		return s
	}
	return string(r[:maxRunes-1]) + "…"
}