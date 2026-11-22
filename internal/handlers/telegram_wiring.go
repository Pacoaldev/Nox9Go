package handlers

import (
	"context"
	"fmt"
	"log"
	"sync"

	"nox9go/proxy/internal/auth"
	"nox9go/proxy/internal/channels/telegram"
	"nox9go/proxy/internal/db"
)

// telegramRuntime owns the process's single Telegram supervisor.
//
// It is a value, not a package-level singleton, so a test can build a router and
// a second one in the same binary without sharing a poll loop.
type TelegramRuntime struct {
	mu  sync.Mutex
	sup *telegram.Supervisor
}

// telegramHolder carries the process's runtime from the boot site to the
// router, which is built earlier and cannot receive it as a parameter without
// threading it through every provider. A save before the first start lands on
// a nil runtime and simply takes effect at the next reconcile.
var telegramHolder struct {
	mu  sync.Mutex
	rt  *TelegramRuntime
}

// SetTelegramRuntime publishes the runtime the OnStart hook created.
func SetTelegramRuntime(rt *TelegramRuntime) {
	telegramHolder.mu.Lock()
	telegramHolder.rt = rt
	telegramHolder.mu.Unlock()
}

// SharedTelegramRuntime returns the published runtime, or nil before the first
// start.
func SharedTelegramRuntime() *TelegramRuntime {
	telegramHolder.mu.Lock()
	defer telegramHolder.mu.Unlock()
	return telegramHolder.rt
}

// startTelegramChannel wires and starts the channel for a booted gateway.
//
// It is a no-op when the channel is not configured: no token or no allowlist
// means the poll loop never starts, which is the fail-closed gate expressed at
// the wiring site instead of only inside the supervisor.
func StartTelegramChannel(ctx context.Context, repo *db.Repo, selfURL string) *TelegramRuntime {
	rt := &TelegramRuntime{}
	if repo == nil {
		return rt
	}

	adapter := telegram.NewDBRepo(repo)
	sup := telegram.NewSupervisor(adapter, func(userID int64) bool {
		cfg, err := repo.GetTelegramChannelConfig()
		if err != nil {
			// A storage failure must not authorize anyone. Reporting false keeps
			// the gate closed, which is the only safe direction on an error.
			log.Printf("[telegram] allowlist read failed, refusing: %v", err)
			return false
		}
		for _, id := range cfg.AllowedUserIDs {
			if id == userID {
				return true
			}
		}
		return false
	})
	sup.SetGateway(selfURL, auth.CLIToken())

	rt.sup = sup
	SetTelegramRuntime(rt)
	sup.Reconcile(ctx)
	return rt
}

// Reconcile re-evaluates the loop after a dashboard settings change.
func (rt *TelegramRuntime) Reconcile() {
	if rt == nil || rt.sup == nil {
		return
	}
	rt.sup.Reconcile(context.Background())
}

// Stop halts the poll loop. It is called from the server's shutdown path: a
// goroutine left polling would keep calling Telegram after the gateway is gone.
func (rt *TelegramRuntime) Stop(ctx context.Context) {
	if rt == nil || rt.sup == nil {
		return
	}
	rt.sup.Stop(ctx)
}

// selfBaseURL is the URL a component inside this process uses to reach its own
// HTTP routes.
func selfBaseURL(port int) string {
	return fmt.Sprintf("http://127.0.0.1:%d", port)
}
