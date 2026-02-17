# GitHub Copilot Instructions — ReminderRelay

## Project Purpose

ReminderRelay is a macOS daemon that syncs Apple Reminders ↔ Home Assistant todo lists bidirectionally. It uses last-write-wins conflict resolution, a SQLite state database to track sync metadata, and a hybrid event model (HA WebSocket for instant updates, polling every 30-60s for Reminders).

## Architecture Overview

```
cmd/reminderrelay/      — entry point, flag parsing, wiring
internal/config/        — YAML config loader + validation
internal/state/         — SQLite repository (sync_items table)
internal/reminders/     — Apple Reminders adapter (go-eventkit)
internal/homeassistant/ — Home Assistant adapter (go-ha-client/v2)
internal/sync/          — reconciliation engine + first-run bootstrap
deployment/             — launchd plist, install/uninstall scripts
```

## Architectural Invariants (always enforce)

- **All mutations must go through the state DB.** Never write to Reminders or HA without also updating `sync_items`. An orphaned write breaks future sync cycles.
- **No direct cross-package DB access.** Only `internal/state` may open or query the SQLite database. Other packages receive a `*state.Store` interface.
- **Last-write-wins conflict resolution.** When both sides have changed since `last_synced_at`, compare `ModifiedAt` timestamps — the later timestamp wins. Never silently drop data.
- **Context must be propagated everywhere.** Every function that calls a network or disk resource must accept a `context.Context` as its first parameter.
- **No panics in library code.** Only `main.go` may call `log.Fatal`. All other packages return errors.

## Code Style

- Idiomatic Go: short variable names in small scopes, descriptive names at package level.
- Errors wrapped with `fmt.Errorf("operation description: %w", err)` — never discarded.
- Structured logging via `log/slog` (stdlib). Use `slog.Info`, `slog.Error`, `slog.Debug` with key-value pairs, not format strings.
- No third-party logging frameworks.
- All exported types, functions, and methods must have doc comments.

## What to Flag in Code Review

- **Missing retry logic** on any call to the HA REST API or WebSocket. All network calls must use the 3-attempt exponential backoff helper in `internal/homeassistant`.
- **Missing context propagation** — any function touching network or disk that doesn't accept `context.Context`.
- **State DB writes missing** after a successful Reminders or HA mutation.
- **Untested sync edge cases** — the reconciler should have unit tests covering: create, update, delete, conflict (Reminders wins), conflict (HA wins), and idempotent no-op.
- **Hardcoded paths** — all file paths must derive from `os.UserHomeDir()`, never hardcoded strings.
- **Token or secret values** appearing in log output.

## Key Dependencies

| Package | Purpose |
|---|---|
| `github.com/BRO3886/go-eventkit` | Apple Reminders via EventKit (cgo, macOS only) |
| `github.com/mkelcik/go-ha-client/v2` | Home Assistant REST + WebSocket client |
| `github.com/mattn/go-sqlite3` | SQLite state database (cgo) |
| `gopkg.in/yaml.v3` | YAML config parsing |
| `log/slog` | Structured logging (stdlib) |

## Priority Encoding Convention

Apple Reminders priority is encoded as a description prefix in Home Assistant (which has no native priority field):

| Reminders priority | Description prefix |
|---|---|
| High (1–4) | `[High] ` |
| Medium (5) | `[Medium] ` |
| Low (6–9) | `[Low] ` |
| None (0) | *(no prefix)* |

When reading from HA: strip the prefix before using the description text. When writing to HA: prepend the prefix to the description.
