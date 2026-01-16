## Status values

Checkers can return OR'd statuses (e.g., `StatusNotFound | StatusDenied`).
Before storing in the database, these are normalized to one of three values:
unknown (0), offline (1), or online (2).

## Storing status changes

Status changes are detected by comparing the in-memory cache of online channels
(`unconfirmedOnlineChannels`) against checker results.

### Online list checkers (e.g., Chaturbate)

1. Was in cache but not in result → offline
2. In result but not in cache → online

### Fixed list checkers (e.g., Twitch)

1. Was in cache, not in result, and was requested → offline
2. In result but not in cache → online
3. Not in cache, exists in DB, not in result, not already offline → offline
4. Not requested known channel → unknown (unsubscribed)

## Denormalization

The `channels` table stores the last two statuses from `status_changes`:

- `unconfirmed_status`
- `unconfirmed_timestamp`
- `prev_unconfirmed_status`
- `prev_unconfirmed_timestamp`

This avoids expensive querying of the latest changes from `status_changes`.
These fields are used to find who is online and calculate durations.
Reliable combinations are (offline, online) and (online, offline).
If either status is unknown, duration data is unreliable.

We bulk insert into `status_changes`
and update the denormalized fields in `channels`.

## Constraints

Both `status_changes.status` and `channels.confirmed_status` are constrained
to (0, 1, 2) — unknown, offline, online.

## Invariant: status_changes and channels must be in sync

The unconfirmed statuses in `channels`
must always match the latest entries in `status_changes`.
We use this invariant to ensure correctness.
This invariant is verified by `checkInv` in tests.

## First offline status

For fixed list checkers (e.g., Twitch),
the first offline status must be recorded after subscription
even if the channel was never seen online.
This is essential for calculating online duration.
Without the initial offline timestamp,
we cannot determine how long the channel has been streaming
when we see it online for the first time.
If we have only unknown -> online transition,
the channel could have been online much longer than we detected,
making duration data unreliable.

## Confirmation

Confirmation adds a delay before notifying users of status changes.
This prevents notification spam when channels flicker online/offline.
A status is confirmed only after it remains stable for a configured duration.
Unknown status confirmations are immediate since they don't generate notifications.

## Indexes

Status change insertion and confirmation are performance-critical —
they run on every checker cycle and must complete quickly.
When modifying these queries, ensure indexes are optimized for query performance
(e.g., cover all required fields if index-only scans are possible).
