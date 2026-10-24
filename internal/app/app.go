package app

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"go.uber.org/fx"
	"go.uber.org/fx/fxevent"
)

// AppModule combines all core modules into a complete application option.
var AppModule = fx.Options(
	ConfigModule,
	DatabaseModule,
	HandlersModule,
	ServerModule,
)

// DefaultFxLogger returns the logging configuration for Fx.
// When FX_LOGGING=true is set, it logs Fx events to stderr; otherwise it uses fx.NopLogger.
func DefaultFxLogger() fx.Option {
	if os.Getenv("FX_LOGGING") == "true" {
		return fx.WithLogger(func() fxevent.Logger {
			return &fxevent.ConsoleLogger{W: os.Stderr}
		})
	}
	return fx.NopLogger
}

// NewApp creates a new fx.App configured with the provided CLI parameters and options.
func NewApp(params CLIParams, opts ...fx.Option) *fx.App {
	allOpts := []fx.Option{
		AppModule,
		fx.Replace(params),
		DefaultFxLogger(),
	}
	allOpts = append(allOpts, opts...)
	return fx.New(allOpts...)
}

// Run starts the Fx application and waits for interrupt signals for graceful shutdown.
func Run(fxApp *fx.App) error {
	startCtx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	if err := fxApp.Start(startCtx); err != nil {
		return err
	}

	signals := make(chan os.Signal, 2)
	signal.Notify(signals, os.Interrupt, syscall.SIGTERM)

	<-signals // first signal -> begin graceful shutdown

	// A second signal force-quits immediately (e.g. a stream stuck mid-drain).
	go func() {
		<-signals
		fmt.Fprintln(os.Stdout, "\n  Force quitting...")
		os.Exit(1)
	}()

	stopCtx, stopCancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer stopCancel()
	return fxApp.Stop(stopCtx)
}
