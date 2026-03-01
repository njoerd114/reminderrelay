// ReminderRelay is a macOS daemon that syncs Apple Reminders ↔ Home Assistant
// todo lists bidirectionally using last-write-wins conflict resolution.
//
// Usage:
//
//	reminderrelay setup                     # interactive first-run wizard
//	reminderrelay daemon [--config <path>]  # start polling + WebSocket listener
//	reminderrelay sync-once [--config ...]  # single reconcile pass then exit
//	reminderrelay status                    # show daemon & config state
//	reminderrelay uninstall [--purge]       # stop daemon and remove files
//	reminderrelay version                   # print version
//
// Legacy flag-based invocation is still supported for backward compatibility:
//
//	reminderrelay --daemon [--config <path>] [--verbose]
//	reminderrelay --sync-once [--config <path>] [--verbose]
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
	"github.com/njoerd114/reminderrelay/internal/setup"
	"github.com/njoerd114/reminderrelay/internal/state"
	syncp "github.com/njoerd114/reminderrelay/internal/sync"
	"github.com/njoerd114/reminderrelay/internal/telemetry"
)

// version is set at build time via -ldflags "-X main.version=..."
var version = "dev"

func main() {
	if err := run(); err != nil {
		slog.Error("fatal error", "error", err)
		os.Exit(1)
	}
}

// run dispatches to the appropriate subcommand or falls back to legacy flags.
func run() error {
	// No arguments → smart usage.
	if len(os.Args) < 2 {
		return printUsage()
	}

	cmd := os.Args[1]

	// Subcommand dispatch.
	switch cmd {
	case "setup":
		return runSetup()
	case "daemon":
		return runSync(os.Args[2:], true)
	case "sync-once":
		return runSync(os.Args[2:], false)
	case "status":
		return runStatus()
	case "uninstall":
		return runUninstall(os.Args[2:])
	case "version":
		fmt.Println("reminderrelay", version)
		return nil
	}

	// Legacy flag-based dispatch (--daemon, --sync-once).
	if strings.HasPrefix(cmd, "-") {
		return runLegacy()
	}

	return fmt.Errorf("unknown command %q — run 'reminderrelay' for usage", cmd)
}

// printUsage shows help and suggests setup if no config exists.
func printUsage() error {
	cfgPath, _ := config.DefaultPath()
	_, cfgErr := os.Stat(cfgPath)

	fmt.Fprintln(os.Stderr, "ReminderRelay — sync Apple Reminders ↔ Home Assistant")
	fmt.Fprintln(os.Stderr, "")
	fmt.Fprintln(os.Stderr, "Usage:")
	fmt.Fprintln(os.Stderr, "  reminderrelay setup                  Interactive first-run wizard")
	fmt.Fprintln(os.Stderr, "  reminderrelay daemon [--config ...]   Run as continuous daemon")
	fmt.Fprintln(os.Stderr, "  reminderrelay sync-once [--config ..] Single sync pass then exit")
	fmt.Fprintln(os.Stderr, "  reminderrelay status                  Show daemon & config state")
	fmt.Fprintln(os.Stderr, "  reminderrelay uninstall [--purge]     Stop daemon and remove files")
	fmt.Fprintln(os.Stderr, "  reminderrelay version                 Print version")
	fmt.Fprintln(os.Stderr, "")

	if cfgErr != nil {
		fmt.Fprintln(os.Stderr, "No config file found. Run 'reminderrelay setup' to get started.")
	}

	os.Exit(1)
	return nil // unreachable
}

// --- Subcommands -------------------------------------------------------------

// runSetup launches the interactive setup wizard.
func runSetup() error {
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))
	slog.SetDefault(logger)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGTERM, syscall.SIGINT)
	defer stop()

	wiz := setup.NewWizard(os.Stdin, os.Stdout, logger)
	return wiz.Run(ctx)
}

// runSync handles both "daemon" and "sync-once" subcommands.
func runSync(args []string, daemon bool) error {
	fs := flag.NewFlagSet("sync", flag.ExitOnError)
	defaultCfg, _ := config.DefaultPath()
	cfgPath := fs.String("config", defaultCfg, "path to config.yaml")
	verbose := fs.Bool("verbose", false, "enable debug logging")
	if err := fs.Parse(args); err != nil {
		return err
	}

	return startSync(*cfgPath, *verbose, daemon)
}

// runLegacy supports the old --daemon / --sync-once flag interface.
func runLegacy() error {
	defaultCfg, _ := config.DefaultPath()
	cfgPath := flag.String("config", defaultCfg, "path to config.yaml")
	daemon := flag.Bool("daemon", false, "run as a continuous daemon (polling + WebSocket)")
	syncOnce := flag.Bool("sync-once", false, "run a single sync pass then exit")
	verbose := flag.Bool("verbose", false, "enable debug logging")
	flag.Parse()

	if !*daemon && !*syncOnce {
		return printUsage()
	}
	if *daemon && *syncOnce {
		return fmt.Errorf("--daemon and --sync-once are mutually exclusive")
	}

	return startSync(*cfgPath, *verbose, *daemon)
}

// runStatus prints the current daemon and configuration state.
func runStatus() error {
	cfgPath, _ := config.DefaultPath()
	homeDir, _ := os.UserHomeDir()
	dbPath, _ := state.DefaultDBPath()

	fmt.Println("ReminderRelay Status")
	fmt.Println("────────────────────")

	// Daemon state.
	if setup.IsDaemonLoaded() {
		fmt.Println("  Daemon:    running (launchd)")
	} else {
		fmt.Println("  Daemon:    not loaded")
	}

	// Config state.
	if _, err := os.Stat(cfgPath); err == nil {
		if cfg, loadErr := config.Load(cfgPath); loadErr == nil {
			fmt.Printf("  Config:    %s ✓\n", cfgPath)
			fmt.Printf("  HA URL:    %s\n", cfg.HAURL)
			fmt.Printf("  Lists:     %d mapping(s)\n", len(cfg.ListMappings))
			fmt.Printf("  Poll:      %s\n", cfg.PollInterval)
		} else {
			fmt.Printf("  Config:    %s (invalid: %v)\n", cfgPath, loadErr)
		}
	} else {
		fmt.Printf("  Config:    not found (%s)\n", cfgPath)
	}

	// State DB.
	if info, err := os.Stat(dbPath); err == nil {
		fmt.Printf("  State DB:  %s (%s)\n", dbPath, humanSize(info.Size()))
	} else {
		fmt.Printf("  State DB:  not found\n")
	}

	// Plist.
	plistPath := setup.PlistPath(homeDir)
	if _, err := os.Stat(plistPath); err == nil {
		fmt.Printf("  Plist:     %s\n", plistPath)
	} else {
		fmt.Printf("  Plist:     not installed\n")
	}

	// Logs.
	logDir := setup.LogDir(homeDir)
	fmt.Printf("  Logs:      %s\n", logDir)

	return nil
}

// runUninstall stops the daemon and removes installed files.
func runUninstall(args []string) error {
	fs := flag.NewFlagSet("uninstall", flag.ExitOnError)
	purge := fs.Bool("purge", false, "also remove config, state DB, and logs")
	if err := fs.Parse(args); err != nil {
		return err
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}

	fmt.Println("Uninstalling ReminderRelay...")

	// 1. Unload daemon.
	if setup.IsDaemonLoaded() {
		fmt.Println("  Unloading daemon...")
		if err := setup.UnloadDaemon(homeDir); err != nil {
			fmt.Printf("  ⚠ %v\n", err)
		} else {
			fmt.Println("  ✓ Daemon unloaded")
		}
	}

	// 2. Remove plist.
	if err := setup.RemovePlist(homeDir); err != nil {
		fmt.Printf("  ⚠ %v\n", err)
	} else {
		fmt.Println("  ✓ Plist removed")
	}

	// 3. Remove binary.
	fmt.Println("  Removing binary...")
	if err := setup.RemoveBinary(); err != nil {
		fmt.Printf("  ⚠ %v\n", err)
	} else {
		fmt.Println("  ✓ Binary removed")
	}

	// 4. Optional purge.
	if *purge {
		fmt.Println("  Purging config, state DB, and logs...")
		if err := setup.PurgeUserData(homeDir); err != nil {
			fmt.Printf("  ⚠ %v\n", err)
		} else {
			fmt.Println("  ✓ User data purged")
		}
	} else {
		fmt.Println("")
		fmt.Println("  Config and state DB preserved.")
		fmt.Println("  Run with --purge to also remove them:")
		fmt.Println("    reminderrelay uninstall --purge")
	}

	fmt.Println("")
	fmt.Println("✓ ReminderRelay uninstalled.")
	return nil
}

// --- Sync core (shared by subcommand and legacy paths) -----------------------

// startSync is the shared implementation for daemon and sync-once modes.
func startSync(cfgPath string, verbose, daemon bool) error {
	// --- Logger --------------------------------------------------------------

	logLevel := slog.LevelInfo
	if verbose {
		logLevel = slog.LevelDebug
	}
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: logLevel}))
	slog.SetDefault(logger)

	// --- Config --------------------------------------------------------------

	cfg, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("loading config from %q: %w", cfgPath, err)
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

	if !daemon {
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

	// daemon mode
	logger.Info("daemon starting", "poll_interval", cfg.PollInterval)
	if err := engine.Run(ctx); err != nil && !errors.Is(err, context.Canceled) {
		return fmt.Errorf("sync engine: %w", err)
	}
	logger.Info("shutdown complete")
	return nil
}

// humanSize returns a human-readable file size string.
func humanSize(bytes int64) string {
	const unit = 1024
	if bytes < unit {
		return fmt.Sprintf("%d B", bytes)
	}
	div, exp := int64(unit), 0
	for n := bytes / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(bytes)/float64(div), "KMGTPE"[exp])
}
