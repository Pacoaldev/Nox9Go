package telegram

import (
	"fmt"

	"nox9go/proxy/internal/db"
)

// Hub dock: three persistent buttons; navigation lives in the inline panel.
const (
	dockHubMenu  = "📋 Menú"
	dockHubMode  = "🔄 Modo"
	dockHubKeys  = "⌨️ Teclado"
	dockHubKeysOn = "⌨️ Mostrar"
)

var chatPlaceholderHints = []string{
	"Chateá o tocá Menú en /panel…",
	"Probá: «resumí el último error»",
	"Probá: «qué modelo tengo activo»",
}

var agentPlaceholderHints = []string{
	"Escribí una orden para Pi o tocá Menú…",
	"Probá: «revisá tests de telegram»",
	"Probá: «C&P con changelog»",
}

func hubDockRows() [][]string {
	return [][]string{{dockHubMenu, dockHubMode, dockHubKeys}}
}

func placeholderHint(mode db.BotMode, index int) string {
	hints := chatPlaceholderHints
	if mode == db.ModeAgent {
		hints = agentPlaceholderHints
	}
	if len(hints) == 0 {
		return dockPlaceholder(mode)
	}
	index = index % len(hints)
	return hints[index]
}

func dockHintFor(mode db.BotMode) string {
	if mode == db.ModeAgent {
		return "Hub Agente — Menú · Modo · Teclado"
	}
	return "Hub Chat — Menú · Modo · Teclado"
}

func (s *Supervisor) rememberDevFeedLine(chatID int64, stamp, line string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.lastDev == nil {
		s.lastDev = make(map[int64]string)
	}
	s.lastDev[chatID] = fmt.Sprintf("[%s] %s", stamp, line)
}

func (s *Supervisor) lastDevFeedLine(chatID int64) string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.lastDev[chatID]
}