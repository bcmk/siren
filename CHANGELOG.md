# Changelog

All notable changes to this project are documented here.
The format is loosely based on [Keep a Changelog](https://keepachangelog.com/).

## v3.12.1 — 2026-07-22

### Fixed

- Rank a chat whose sends keep failing behind healthy ones,
  so a repeatedly timing-out message no longer starves the outgoing queue.

## v3.12.0 — 2026-07-20

### Added

- Record a reply's place in its answer in `sent_message_log.reply_seq`,
  carried through `notification_queue` and `pending_subscriptions`
  so a deferred reply keeps its position.

### Changed

- Send the field customization hint after the pictures it explains,
  rather than ahead of them.

## v3.11.0 — 2026-07-20

### Added

- Record the command a reply answers in `sent_message_log.command`,
  carried through `notification_queue` and `pending_subscriptions`
  so a deferred reply keeps it.
- Record a buy menu tap as `buy_callback`
  and a search web app add as `web_app_add` in `received_message_log`.

### Changed

- Refuse a search or an add from a chat outside the whitelist,
  rather than serving it and skipping the log.
- Require a positive `max_subscriptions_for_pics`.

### Fixed

- Track a capitalized command instead of counting it unnamed.

## v3.10.0 — 2026-07-18

### Changed

- Postpone a send that hits a 429, timeout, or network error,
  re-queuing it per user instead of retrying in place.
- Honor Telegram's retry_after on a 429, capped at 20 minutes.
- Pause all sends for 1 second after any 429 to back off a bot-wide limit.

## v3.9.1 — 2026-07-08

### Fixed

- Scope the startup pg_stat_statements reset to the current database
  instead of the whole cluster.

## v3.9.0 — 2026-07-08

### Added

- Reset pg_stat_statements counters on startup, with a migration creating
  the extension if absent.
- Configurable image download timeout (image_download_timeout_seconds),
  default 5s.

## v3.8.0 — 2026-07-08

### Changed

- Give each user a stable surrogate id and key sends and the child tables
  on it, so a group-to-supergroup upgrade keeps history intact.
- Schedule outgoing sends on the main loop, single-flight, with per-user
  and per-group pacing (groups at Telegram's 20 messages/minute cap).
- Bump golang.org/x/crypto to 0.52.0 and golang.org/x/image to 0.41.0.

### Fixed

- Resurrect subscription and message-log history stranded at the old chat id
  by the v3.7.0 group-to-supergroup migration.

## v3.7.0 — 2026-06-23

### Added

- Migrate a chat's subscriptions and settings to its new ID
  on a group-to-supergroup upgrade.

## v3.6.0 — 2026-06-23

### Changed

- Bump the Go module path to `/v3` to match the v3 release tags.

## v3.5.0 — 2026-06-15

### Changed

- Treat "not enough rights to send text messages" bad requests as a
  non-error: log at debug and track a distinct `no text rights` send
  result instead of an error.

## v3.4.0 — 2026-06-14

### Added

- adapter-mfc: log bulk-applied state in the snapshot-counts heartbeat.

### Fixed

- adapter-mfc: reconnect if the initial bulk dump never arrives,
  instead of serving a failed online list forever
  (new `bulk_arrival_timeout`, default 60s).

## v3.3.0 — 2026-06-03

### Added

- In-bot ad promoting `/buy_subs`.

### Fixed

- Drain pending webhook updates on shutdown.

## v3.2.1 — 2026-06-03

### Added

- Log the Stars buy funnel (invoice, pre-checkout, payment)
  and the silent-message toggle commands.

## v3.2.0 — 2026-06-03

### Added

- `/buy_subs`: buy extra subscription slots with Telegram Stars,
  configured via `subs_tiers`.

## v3.1.5 — 2026-05-19

### Fixed

- `vacuum analyze` on `sent_message_log` and `received_message_log`
  after the v3.1.4 cluster — CLUSTER resets the visibility map,
  degrading Index-Only Scans until VACUUM runs.

## v3.1.4 — 2026-05-18

### Changed

- Clustered `sent_message_log` and `received_message_log` by timestamp
  and tuned BRIN to `pages_per_range = 8`.

## v3.1.3 — 2026-05-18

### Changed

- `vacuum analyze` for `sent_message_log` and `received_message_log`.

## v3.1.1 — 2026-05-18

### Changed

- Reworked `chat_id` indexes on `sent_message_log` /
  `received_message_log` to compound `(chat_id, timestamp)`.

## v3.1.0 — 2026-05-14

### Added

- chaturbate: `QueryStatus` (single-streamer check) routed through a
  round-robin list of proxies. Required `proxies` field added to
  `chaturbate-checker.json`; pods without it will fail to start.

## v3.0.2 — 2026-05-12

### Removed

- `headers` key from the MyFreeCams checker config — never used.
- `block_threshold` key from the bot config — never consulted; the
  per-chat block counter is still tracked.

## v3.0.1 — 2026-05-12

### Changed

- adapter-mfc: the periodic snapshot-counts heartbeat line now reports
  `connection uptime` (time since the live websocket handshake completed,
  or `disconnected` between reconnects) in place of the lifetime
  `incomplete frames` tally, which had served its purpose confirming that
  MFC's mid-frame message flushing is handled correctly.

### Fixed

- Bot shutdown no longer panics when Telegram answers the `deleteWebhook`
  call with HTTP 400; the library expected a `nil` argument there.

## v3.0.0 — 2026-05-11

### Changed

- **Breaking — config layout.** Per-site checker settings (HTTP timeout,
  request interval, API secrets, online query endpoints) moved out of the
  bot's `config.json` into a separate `<site>-checker.json` file
  (searched in the CWD and `~/.config/siren/` unless `--checker-config`
  is given). The bot now requires two flags: `--bot-config <path>` and
  `--checker-config <path>`.
- **Breaking — secret env vars renamed.** Secrets that used to be injected
  via `XRN_SPECIFIC_CONFIG_*` (into the bot config's `specific_config`
  map) are now plain fields on the per-site checker config, with shorter
  `XRN_`-prefixed names:
  - `XRN_SPECIFIC_CONFIG_CLIENT_ID` → `XRN_CLIENT_ID` (Twitch, Kick)
  - `XRN_SPECIFIC_CONFIG_CLIENT_SECRET` → `XRN_CLIENT_SECRET` (Twitch, Kick)
  - `XRN_SPECIFIC_CONFIG_USER_ID` → `XRN_USER_ID` (Stripchat)
  - `XRN_SPECIFIC_CONFIG_PS_ID` → `XRN_PS_ID` (LiveJasmin)
  - `XRN_SPECIFIC_CONFIG_ACCESS_KEY` → `XRN_ACCESS_KEY` (LiveJasmin)

  A deployment still setting the old names gets no secret, silently.

- Checker daemon: when a bulk online/status query fails, the daemon now
  waits `min_request_interval_ms` before serving the next queued request
  instead of serving it immediately.
