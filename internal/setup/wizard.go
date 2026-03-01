package setup

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"time"

	"github.com/njoerd114/reminderrelay/internal/config"
)

// Wizard guides the user through first-run configuration and installation.
type Wizard struct {
	prompt *Prompter
	logger *slog.Logger
	w      io.Writer
}

// NewWizard creates a Wizard wired to the given I/O and logger.
func NewWizard(r io.Reader, w io.Writer, logger *slog.Logger) *Wizard {
	return &Wizard{
		prompt: NewPrompter(r, w),
		logger: logger,
		w:      w,
	}
}

// Run executes the interactive setup wizard. It walks the user through HA
// connection, list mapping, config file creation, and optional daemon install.
func (wiz *Wizard) Run(ctx context.Context) error {
	fmt.Fprintf(wiz.w, "\nWelcome to ReminderRelay Setup!\n")
	fmt.Fprintf(wiz.w, "This wizard will help you configure and install ReminderRelay.\n\n")

	// Check for existing config.
	cfgPath, err := config.DefaultPath()
	if err != nil {
		return fmt.Errorf("resolving config path: %w", err)
	}

	if _, statErr := os.Stat(cfgPath); statErr == nil {
		fmt.Fprintf(wiz.w, "  Existing config found at %s\n", cfgPath)
		if !wiz.prompt.Confirm("Overwrite existing configuration?", false) {
			fmt.Fprintf(wiz.w, "\n  Keeping existing config.\n")
			return wiz.offerDaemonInstall(ctx)
		}
		fmt.Fprintf(wiz.w, "\n")
	}

	// Step 1: Home Assistant connection.
	fmt.Fprintf(wiz.w, "Step 1/4 — Home Assistant Connection\n")

	haURL := wiz.prompt.String("HA URL", "http://homeassistant.local:8123")
	haToken := wiz.prompt.Secret("Access token")

	fmt.Fprintf(wiz.w, "  Connecting to Home Assistant...")
	if err := PingHA(ctx, haURL, haToken); err != nil {
		fmt.Fprintf(wiz.w, " ✗\n")
		return fmt.Errorf("cannot reach Home Assistant: %w\n\n  Check the URL and token, then try again", err)
	}
	fmt.Fprintf(wiz.w, " ✓\n\n")

	// Step 2: Discover & map lists.
	fmt.Fprintf(wiz.w, "Step 2/4 — List Mappings\n")

	listMappings, err := wiz.buildListMappings(ctx, haURL, haToken)
	if err != nil {
		return err
	}

	// Step 3: Poll interval.
	fmt.Fprintf(wiz.w, "Step 3/4 — Poll Interval\n")

	pollStr := wiz.prompt.String("How often to poll Reminders for changes? (10s–5m)", "30s")
	pollInterval, parseErr := time.ParseDuration(pollStr)
	if parseErr != nil {
		pollInterval = 30 * time.Second
		fmt.Fprintf(wiz.w, "  (invalid duration, using default 30s)\n")
	}
	fmt.Fprintf(wiz.w, "\n")

	// Step 4: Write config.
	fmt.Fprintf(wiz.w, "Step 4/4 — Save Configuration\n")

	cfg := &config.Config{
		HAURL:        haURL,
		HAToken:      haToken,
		PollInterval: pollInterval,
		ListMappings: listMappings,
	}

	if err := cfg.Write(cfgPath); err != nil {
		return fmt.Errorf("writing config: %w", err)
	}
	fmt.Fprintf(wiz.w, "  ✓ Config written to %s\n\n", cfgPath)

	return wiz.offerDaemonInstall(ctx)
}

// buildListMappings discovers Reminders lists and HA entities, then lets the
// user pair them interactively.
func (wiz *Wizard) buildListMappings(ctx context.Context, haURL, haToken string) (map[string]string, error) {
	// Discover Reminders lists.
	fmt.Fprintf(wiz.w, "  Discovering Reminders lists (may trigger permissions prompt)...\n")
	remLists, remErr := DiscoverRemindersLists(wiz.logger)
	if remErr != nil {
		wiz.logger.Warn("could not discover Reminders lists", "error", remErr)
		fmt.Fprintf(wiz.w, "  ⚠ Could not list Reminders — you can type list names manually.\n")
	} else {
		fmt.Fprintf(wiz.w, "  Found %d Reminders list(s):\n", len(remLists))
		for _, l := range remLists {
			fmt.Fprintf(wiz.w, "    • %s (%d items)\n", l.Title, l.Count)
		}
	}
	fmt.Fprintf(wiz.w, "\n")

	// Discover HA todo entities.
	fmt.Fprintf(wiz.w, "  Discovering HA todo entities...\n")
	haEntities, haErr := DiscoverHATodoEntities(ctx, haURL, haToken)
	if haErr != nil {
		wiz.logger.Warn("could not discover HA entities", "error", haErr)
		fmt.Fprintf(wiz.w, "  ⚠ Could not list HA entities — you can type entity IDs manually.\n")
	} else {
		fmt.Fprintf(wiz.w, "  Found %d HA todo entity/entities:\n", len(haEntities))
		for _, e := range haEntities {
			fmt.Fprintf(wiz.w, "    • %s\n", e)
		}
	}
	fmt.Fprintf(wiz.w, "\n")

	// Interactive mapping.
	fmt.Fprintf(wiz.w, "  Map Reminders lists to HA entities (empty Reminders name to finish):\n\n")

	mappings := make(map[string]string)
	haEntityNames := make([]string, len(haEntities))
	for i, e := range haEntities {
		haEntityNames[i] = e.String()
	}

	for {
		var remName string
		if remErr == nil && len(remLists) > 0 {
			// Show selection from discovered lists.
			remOptions := make([]string, len(remLists))
			for i, l := range remLists {
				remOptions[i] = fmt.Sprintf("%s (%d items)", l.Title, l.Count)
			}
			remOptions = append(remOptions, "(done — finish mapping)")

			idx, err := wiz.prompt.Select("Reminders list", remOptions)
			if err != nil {
				return nil, fmt.Errorf("selecting Reminders list: %w", err)
			}
			if idx == len(remOptions)-1 {
				break // done
			}
			remName = remLists[idx].Title
		} else {
			remName = wiz.prompt.String("Reminders list (empty to finish)", "")
			if remName == "" {
				break
			}
		}

		var entityID string
		if haErr == nil && len(haEntities) > 0 {
			idx, err := wiz.prompt.Select(fmt.Sprintf("HA entity for %q", remName), haEntityNames)
			if err != nil {
				return nil, fmt.Errorf("selecting HA entity: %w", err)
			}
			entityID = haEntities[idx].EntityID
		} else {
			entityID = wiz.prompt.String("HA entity ID (e.g. todo.shopping)", "")
			if entityID == "" {
				continue
			}
		}

		mappings[remName] = entityID
		fmt.Fprintf(wiz.w, "  ✓ Mapped %q → %s\n\n", remName, entityID)
	}

	if len(mappings) == 0 {
		return nil, fmt.Errorf("at least one list mapping is required")
	}
	fmt.Fprintf(wiz.w, "\n")
	return mappings, nil
}

// offerDaemonInstall asks the user whether to install as a background daemon.
func (wiz *Wizard) offerDaemonInstall(_ context.Context) error {
	if !wiz.prompt.Confirm("Install as background daemon (starts on login)?", true) {
		fmt.Fprintf(wiz.w, "\n  Skipping daemon install.\n")
		fmt.Fprintf(wiz.w, "  You can run manually with: reminderrelay daemon\n")
		fmt.Fprintf(wiz.w, "  Or install later with:     reminderrelay setup\n\n")
		return nil
	}

	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("resolving home directory: %w", err)
	}

	fmt.Fprintf(wiz.w, "\n")

	// Install binary.
	fmt.Fprintf(wiz.w, "  Installing binary to %s...\n", BinaryInstallPath())
	if err := InstallBinary(); err != nil {
		return fmt.Errorf("installing binary: %w", err)
	}
	fmt.Fprintf(wiz.w, "  ✓ Binary installed\n")

	// Write plist.
	if err := WritePlist(homeDir); err != nil {
		return fmt.Errorf("writing plist: %w", err)
	}
	fmt.Fprintf(wiz.w, "  ✓ LaunchAgent plist written\n")

	// Create log directory.
	if err := CreateLogDir(homeDir); err != nil {
		return fmt.Errorf("creating log directory: %w", err)
	}
	fmt.Fprintf(wiz.w, "  ✓ Log directory created\n")

	// Load daemon.
	if err := LoadDaemon(homeDir); err != nil {
		return fmt.Errorf("loading daemon: %w", err)
	}
	fmt.Fprintf(wiz.w, "  ✓ Daemon loaded — running now\n")

	cfgPath, _ := config.DefaultPath()
	fmt.Fprintf(wiz.w, "\nSetup complete! ReminderRelay is syncing in the background.\n")
	fmt.Fprintf(wiz.w, "  Config:  %s\n", cfgPath)
	fmt.Fprintf(wiz.w, "  Logs:    %s\n", LogDir(homeDir))
	fmt.Fprintf(wiz.w, "  Status:  reminderrelay status\n")
	fmt.Fprintf(wiz.w, "  Remove:  reminderrelay uninstall\n\n")

	return nil
}
