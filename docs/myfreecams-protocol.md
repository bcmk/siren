## MyFreeCams FCS protocol notes

Reverse-engineered observations from running `adapter-mfc`
and capturing live traffic with `scripts/mfc_mitmproxy_addon.py`.
Useful when debugging the daemon or extending FCType handling.

### Bulk dump vs streaming updates

After login, MFC does two things:

1. Sends a single `EXTDATA (81)` frame whose payload points to a deferred
   `MANAGELIST/CAMS` JSON dump. We `GET` that URL over HTTPS and parse it
   into the initial snapshot of online models.
2. Pushes per-model `SESSIONSTATE (20)` frames continuously. Online events
   come as fat objects (`nm`, full `u`/`m`/`x` blocks); state updates and
   offline events come as small partial deltas.

### Delayed offline events (anti-flicker hypothesis)

**Observation:** when the daemon starts fresh, `snapshot.online` grows for roughly 30
minutes before plateauing. After the plateau, it tracks MFC's actual online
population without unbounded drift.

**Working hypothesis (unconfirmed):** MFC buffers `vs=127`
(FCVIDEO.OFFLINE) events for ~15-30 minutes before broadcasting them,
presumably to avoid flicker when a model briefly drops and reconnects. The
warm-up curve and plateau are consistent with this — early in a session we
see online events immediately but offline events only after the buffer
expires, so the count grows; once the buffer has been "primed" with a
steady stream of delayed offlines, the in/out rates balance.

This has **not** been confirmed against MFC documentation or source — it's
just the best fit for the data we've captured
(`scripts/mfc_mitmproxy_addon.py` traces show ~1% of SESSIONSTATEs being
offline events in short windows, which would otherwise imply unbounded
growth).

Practical consequences for the daemon, assuming the hypothesis holds:

- `snapshot.online` is bounded by MFC's actual online population after the warm-up
  window. No periodic prune or forced reconnect is needed.
- `status change[unknown uid]` log lines (offline events for uids we never
  saw online) are expected: the model came online and offline both within
  MFC's delay window before the daemon connected, so we received the
  delayed offline without seeing the corresponding online.

If future observation contradicts the hypothesis (e.g., the count keeps
growing past the warm-up window), revisit and consider a periodic
reconnect or stale-prune strategy.

### Delta SESSIONSTATEs without `nm`

Once MFC has sent the initial full SESSIONSTATE for a model to a connected
client, subsequent updates for that model omit the `nm` field. A new client
that connects mid-session may receive deltas (e.g. `rc` change, `vs`
transition) for uids it has never been told the name of.

The daemon handles this two ways:

1. **Name cache** — every `(uid, nm)` pair we've ever observed (from any
   bulk row, full SESSIONSTATE, or USERNAMELOOKUP response) is kept in
   `snapshot.nameCache`. When a delta arrives for an unknown uid we fill
   the name from the cache instead of issuing a request.
2. **USERNAMELOOKUP fallback** — on a cache miss the daemon sends an
   `FCType=10 (USERNAMELOOKUP)` query addressed by uid. MFC replies with
   a SESSIONSTATE-shaped record on the same FCType; the dispatcher feeds
   it through `snapshot.applyUpdate` which fills the name and updates the
   cache for next time.

The cache is in-memory only. Resolved names are pruned after 24 hours
unseen; in-flight lookup tombstones after 1 hour.

### FCTypes we observe

Captured during a normal browser session against MFC, in rough order of
volume:

| Type | Name             | Daemon handling                                              |
| ---- | ---------------- | ------------------------------------------------------------ |
| 20   | `SESSIONSTATE`   | Full handling (online/offline/state merge into snapshot)     |
| 44   | `ROOMDATA`       | Bulk `{uid: rc}` updates — applied to existing entries       |
| 64   | `TAGS`           | Ignored (model tags; not exposed in the JSON output)         |
| 0    | `NULL`           | Ignored (server-side keepalive responses)                    |
| 81   | `EXTDATA`        | Used for bulk MANAGELIST/CAMS pointer detection              |
| 33   | `MODELGROUP`     | Ignored (empty for guest sessions)                           |
| 30   | `TKX`            | Ignored (per-session token)                                  |
| 7    | `ADDIGNORE`      | Ignored (ignore-list state)                                  |
| 5    | `DETAILS`        | Ignored                                                      |
| 1    | `LOGIN`          | Login response (sessionid)                                   |
| 10   | `USERNAMELOOKUP` | Response handling (paired with our outbound query)           |
| 14   | `MANAGELIST`     | Embedded in EXTDATA envelope; not seen as a top-level FCType |

ROOMDATA carries a `{uid: rc}` map and updates many models' viewer counts
in one frame. It only seems to be sent to clients that have joined a room
or otherwise opted in — the daemon (a passive guest) sees it rarely.

### Wire framing

- **Server → client** frames have a 6-digit zero-padded ASCII length
  prefix, followed by `<type> <from> <to> <arg1> <arg2>[ <url-encoded payload>]`.
- **Client → server** frames (handshake, login, NULL keepalive,
  USERNAMELOOKUP) are the same line format **without** the length prefix
  and end with `\n\x00`.
- Concurrent reads and writes on the websocket are safe; concurrent
  _writes_ are not — `wsSession.write` serialises with a mutex.

### Keepalive

MFC closes idle connections after ~30 seconds. The daemon sends an
`FCType=0 (NULL)` frame every 15 seconds (`0 0 0 0 0\n\x00`) to keep the
session alive, matching MFCAuto's heuristic.
