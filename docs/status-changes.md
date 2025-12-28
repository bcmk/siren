## Status values

Checkers can return OR'd statuses (e.g., `StatusNotFound | StatusDenied`).
Before storing in the database, these are transformed into one of three values:
unknown (0), offline (1), or online (2).

## Denormalization

The `models` table stores the last two statuses from `status_changes`:

- `unconfirmed_status`
- `unconfirmed_timestamp`
- `prev_unconfirmed_status`
- `prev_unconfirmed_timestamp`

This avoids expensive querying of the latest changes from `status_changes`.
These fields are used to find who is online and calculate durations.
Reliable combinations are (offline, online) and (online, offline).
If either status is unknown, duration data is unreliable.

We bulk insert into `status_changes`
and update the denormalized fields in `models`.

## Constraints

The `status_changes.status` column is constrained to (0, 1, 2) — unknown,
offline, online.

The `models.confirmed_status` column is constrained to (1, 2) — offline, online.
We use fast partial indexes for online statuses.
Since confirmed statuses are only used for notifications,
we sacrifice some correctness for performance (both speed and disk space).
We treat unknown unconfirmed status as offline for confirmation.
This allows simple XOR-ing in online/not-online space
when finding models that need confirmation.
