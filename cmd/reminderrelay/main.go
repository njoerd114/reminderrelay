// ReminderRelay is a macOS daemon that syncs Apple Reminders ↔ Home Assistant
// todo lists bidirectionally using last-write-wins conflict resolution.
//
// Usage:
//
//	reminderrelay [--config <path>] [--daemon | --sync-once]
//	reminderrelay --daemon          # start polling + WebSocket listener
//	reminderrelay --sync-once       # single reconcile pass then exit
package main

import (
	"bufio"
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/njoerd114/reminderrelay/internal/config"
	"github.com/njoerd114/reminderrelay/internal/homeassistant"
	"github.com/njoerd114/reminderrelay/internal/reminders"
	"github.com/njoerd114/reminderrelay/internal/state"
	syncp "github.com/njoerd114/reminderrelay/internal/sync"
	"github.com/njoerd114/reminderrelay/internal/telemetry"
)

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run is the entry point extracted from main so errors can propagate cleanly.
func run() error {
	// --- Flags ---------------------------------------------------------------

	defaultCfg, _ := config.DefaultPath()
	cfgPath := flag.String("config", defaultCfg, "path to config.yaml")
	daemon := flag.Bool("daemon", false, "run as a continuous daemon (polling + WebSocket)")
	syncOnce := flag.Bool("sync-once", false, "run a single sync pass then exit")
	verbose := flag.Bool("verbose", false, "enable debug logging")
	flag.Parse()

	if !*daemon && !*syncOnce {
		fmt.Fprintln(os.Stderr, "usage: reminderrelay [--config <path>] [--daemon | --sync-once]")
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "  --daemon      run as a continuous daemon")
		fmt.Fprintln(os.Stderr, "  --sync-once   run a single sync pass then exit")
		os.Exit(1)
	}
	if *daemon && *syncOnce {
		return fmt.Errorf("--daemon and --sync-once are mutually exclusive")
	}

	// --- Logger --------------------------------------------------------------

	logLevel := slog.LevelInfo
	if *verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// --- Config --------------------------------------------------------------

	cfg, err := config.Load(*cfgPath)
	if err != nil {
		return fmt.Errorf("loading config from %q: %w", *cfgPath, err)
	}
	logger.Info("config loaded",
		"ha_url", cfg.HAURL,
		"poll_interval", cfg.PollInterval,
		"lists", len(cfg.ListMappings),
	)

	// --- Telemetry (optional) ------------------------------------------------

	if cfg.Telemetry != nil {
		telCfg := telemetry.Config{
			OTLPEndpoint: cfg.Telemetry.OTLPEndpoint,
			Insecure:     cfg.Telemetry.Insecure,
			ServiceName:  cfg.Telemetry.ServiceName,
			Headers:      cfg.Telemetry.Headers,
		}
		shutdownTel, err := telemetry.Setup(context.Background(), telCfg)
		if err != nil {
			logger.Error("telemetry setup failed, continuing without telemetry", "error", err)
		} else {
			logger.Info("telemetry enabled", "endpoint", cfg.Telemetry.OTLPEndpoint)
			defer func() {
				flushCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				if err := shutdownTel(flushCtx); err != nil {
					logger.Error("telemetry shutdown error", "error", err)
				}
			}()
		}
	}

	// --- State DB ------------------------------------------------------------

	dbPath, err := state.DefaultDBPath()
	if err != nil {
		return fmt.Errorf("resolving state DB path: %w", err)
	}
	store, err := state.Open(dbPath)
	if err != nil {
		return fmt.Errorf("opening state DB at %q: %w", dbPath, err)
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			logger.Error("closing state DB", "error", closeErr)
		}
	}()
	logger.Info("state DB opened", "path", dbPath)

	// --- Reminders adapter ---------------------------------------------------

	logger.Info("initialising Apple Reminders client (may trigger permissions prompt)…")
	remAdapter, err := reminders.NewAdapter(logger)
	if err != nil && strings.Contains(err.Error(), "access denied") {
		// macOS has denied Reminders access (TCC). Open System Settings to the
		// correct privacy page so the user can flip the switch, then retry once.
		fmt.Fprintln(os.Stderr, "")
		fmt.Fprintln(os.Stderr, "⚠️  Reminders access is denied.")
		fmt.Fprintln(os.Stderr, "   Opening System Settings → Privacy & Security → Reminders…")
		_ = exec.Command("open", "x-apple.systempreferences:com.apple.preference.security?Privacy_Reminders").Start()
		fmt.Fprint(os.Stderr, "   Press Enter after granting access to retry: ")
		_, _ = bufio.NewReader(os.Stdin).ReadString('\n')
		remAdapter, err = reminders.NewAdapter(logger)
	}
	if err != nil {
		return fmt.Errorf("initialising Reminders client: %w", err)
	}
	logger.Info("Reminders client ready")

	// --- Home Assistant adapter & connectivity check -------------------------

	haAdapter, err := homeassistant.NewAdapter(cfg.HAURL, cfg.HAToken, logger)
	if err != nil {
		return fmt.Errorf("initialising Home Assistant client: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	logger.Info("pinging Home Assistant…", "url", cfg.HAURL)
	if err := haAdapter.Ping(ctx); err != nil {
		return fmt.Errorf("connecting to Home Assistant at %q: %w\n\nCheck ha_url and ha_token in your config file", cfg.HAURL, err)
	}
	logger.Info("Home Assistant reachable")

	// --- First-run bootstrap -------------------------------------------------

	bootstrap := syncp.NewBootstrap(remAdapter, haAdapter, store, logger, os.Stdin, os.Stdout)
	if _, err := bootstrap.Run(ctx, cfg.ListMappings); err != nil {
		return fmt.Errorf("first-run bootstrap: %w", err)
	}

	// --- Sync engine ---------------------------------------------------------

	reconciler := syncp.NewReconciler(remAdapter, haAdapter, store, logger)
	engine := syncp.NewEngine(reconciler, haAdapter, cfg.ListMappings, cfg.PollInterval, logger)

	// --- Dispatch mode -------------------------------------------------------

	if *syncOnce {
		logger.Info("running single sync pass")
		stats, err := engine.RunOnce(ctx)
		logger.Info("sync complete",
			"created", stats.Created,
			"updated", stats.Updated,
			"deleted", stats.Deleted,
			"conflicts", stats.Conflicts,
			"errors", stats.Errors,
		)
		return err
	}

	// --daemon
	logger.Info("daemon starting", "poll_interval", cfg.PollInterval)
	if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("sync engine: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}
