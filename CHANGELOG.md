# Changelog

All notable changes to this project are documented here.
The format is loosely based on [Keep a Changelog](https://keepachangelog.com/).

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
