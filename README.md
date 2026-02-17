# ReminderRelay

Bidirectional sync daemon that keeps **Apple Reminders** and **Home Assistant** todo lists in sync — automatically, in the background, on macOS.

```
Apple Reminders  ←──────────────────→  Home Assistant
   (EventKit)          ReminderRelay      (REST + WebSocket)
```

## Features

- **Bidirectional sync** — changes made in either app appear in the other within seconds.
- **Last-write-wins conflict resolution** — the side that changed most recently wins; no silent data loss.
- **Real-time HA updates** — WebSocket subscription for instant propagation from HA → Reminders.
- **Polling for Reminders changes** — configurable 10 s – 5 m interval (default 30 s).
- **Priority mapping** — Apple Reminders priorities are encoded as `[High]`, `[Medium]`, `[Low]` prefixes in HA descriptions.
- **First-run bootstrap** — interactive wizard that matches existing items between both sides by title and prompts before writing anything.
- **Persistent state database** — SQLite tracks sync metadata so resuming after a restart is safe.

## Prerequisites

| Requirement | Version |
|---|---|
| macOS | 13 Ventura or later |
| Apple ID / iCloud | Signed in with Reminders enabled |
| Home Assistant | ≥ 2023.11 (Todo integration required) |
| HA long-lived access token | Profile → Security → Long-Lived Access Tokens |

## Quick Start

### 1. Install devbox (once)

```bash
curl -fsSL https://get.jetify.com/devbox | bash
```

### 2. Clone and enter the dev shell

```bash
git clone https://github.com/njoerd114/reminderrelay.git
cd reminderrelay
devbox shell
```

### 3. Create your config

```bash
mkdir -p ~/.config/reminderrelay
cp config.example.yaml ~/.config/reminderrelay/config.yaml
$EDITOR ~/.config/reminderrelay/config.yaml
```

Key fields:

```yaml
ha_url: "http://homeassistant.local:8123"
ha_token: "your-long-lived-access-token-here"
poll_interval: 30s
list_mappings:
  "Shopping": "todo.shopping"
  "Work":     "todo.work_tasks"
```

### 4. Test connectivity (dry run)

```bash
just sync-once
```

This does one full sync cycle and exits.  
On first run you will be prompted to review and confirm bootstrap matches — nothing is written until you type **y**.

### 5. Install as a background daemon

```bash
just install
```

That's it. ReminderRelay is now running and will restart automatically when you log in.

## Configuration Reference

| Key | Type | Default | Description |
|---|---|---|---|
| `ha_url` | string | — | Home Assistant base URL (`http://…` or `https://…`) |
| `ha_token` | string | — | Long-lived access token |
| `poll_interval` | duration | `30s` | How often Reminders are polled (10 s – 5 m) |
| `list_mappings` | map | — | `"Reminders list name": "todo.entity_id"` |

## Discovering Your HA Entity IDs

1. Open Home Assistant → **Settings → Devices & services → Entities**.
2. Filter by domain **todo**.
3. Copy the entity IDs (e.g. `todo.shopping`) into `list_mappings`.

Or run:

```bash
just sync-once -- --verbose 2>&1 | grep "entity"
```

## Priority Encoding

Apple Reminders supports four priority levels.  
Home Assistant todo has no native priority field, so ReminderRelay encodes priority as a prefix in the task description:

| Reminders priority | Description prefix |
|---|---|
| High | `[High] ` |
| Medium | `[Medium] ` |
| Low | `[Low] ` |
| None | *(no prefix)* |

## Justfile Recipes

```bash
just build        # compile binary
just test         # run all tests
just lint         # run golangci-lint
just run          # run daemon in foreground (Ctrl-C to stop)
just sync-once    # run one sync cycle and exit
just install      # build + install + load launchd agent
just uninstall    # unload + remove binary and plist
```

## Logs

| Location | Contents |
|---|---|
| `~/Library/Logs/reminderrelay/output.log` | Info and debug output |
| `~/Library/Logs/reminderrelay/errors.log` | Errors and warnings |

Tail logs live:

```bash
tail -f ~/Library/Logs/reminderrelay/errors.log
```

## Uninstall

```bash
just uninstall                             # remove daemon + binary
bash deployment/uninstall.sh --purge       # also remove config + DB + logs
```

## Troubleshooting

### Reminders access denied (TCC)

macOS requires explicit permission for apps to access Reminders.  
On first run a system dialog appears — click **OK**.  
If you previously denied access:

1. Open **System Settings → Privacy & Security → Reminders**.
2. Enable access for Terminal (or your shell app).

### HA connection refused

- Confirm `ha_url` is reachable: `curl -s <ha_url>/api/ -H "Authorization: Bearer <token>"`
- Ensure the token has not expired or been revoked.

### Items duplicated after restart

This usually means the state database was deleted while items still existed in both systems. Remove the DB and re-run the bootstrap:

```bash
rm ~/.local/share/reminderrelay/state.db
just sync-once
```

### Sync is slow

Decrease `poll_interval` (minimum `10s`). Real-time HA → Reminders flow is already push-based via WebSocket; the interval only affects Reminders → HA propagation.

## Architecture

```
cmd/reminderrelay/        Entry point, signal handling, wiring
internal/config/          YAML config loader + validation
internal/state/           SQLite repository (WAL mode)
internal/model/           Shared Item type, priority encoding, content hash
internal/reminders/       Apple Reminders adapter (EventKit via cgo)
internal/homeassistant/   HA REST + WebSocket adapter, retry logic
internal/sync/            Reconciler, bootstrap wizard, daemon engine
deployment/               launchd plist, install/uninstall scripts
```

## License

MIT — see [LICENSE](LICENSE).
