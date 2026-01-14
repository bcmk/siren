## Status values

Checkers can return OR'd statuses (e.g., `StatusNotFound | StatusDenied`).
Before storing in the database, these are normalized to one of three values:
unknown (0), offline (1), or online (2).

## Storing status changes

1. Normalize statuses
2. For fixed list checkers, mark known channels without subscriptions as unknown
3. Store online to offline transitions
4. Store checker statuses that differ from DB

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

## Confirmation

Confirmation adds a delay before notifying users of status changes.
This prevents notification spam when channels flicker online/offline.
A status is confirmed only after it remains stable for a configured duration.
Unknown status confirmations are immediate since they don't generate notifications.
