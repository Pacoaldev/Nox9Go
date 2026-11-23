package telegram

import (
	"strings"

	"nox9go/proxy/internal/config"
)

// herdrTelegramRelay reports whether blocker buttons and Herdr CLI hooks are wired.
func herdrTelegramRelay() bool {
	runner := strings.ToLower(strings.TrimSpace(config.DevOrderRunner()))
	if strings.TrimSpace(config.HerdrAgentTarget()) != "" {
		return true
	}
	return strings.Contains(runner, "herdr")
}