The `models` table stores the last two statuses from `status_changes`:

- `unconfirmed_status`
- `unconfirmed_timestamp`
- `prev_unconfirmed_status`
- `prev_unconfirmed_timestamp`

This avoids expensive queries scanning `status_changes` for the latest changes.
The denormalized fields are used to find who is online and calculate durations.
If any of two statuses is unknown, the data is unreliable.
Only reliable combinations are (offline, online) and (online, offline).

`InsertStatusChanges` bulk inserts into `status_changes` and updates the
denormalized fields in `models`.

