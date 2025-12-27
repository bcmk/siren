# BRIN Index Maintenance

BRIN index on `status_changes.timestamp` relies on physical row order.
And it matches timestamp order.
If you alter `status_changes` in any way, e.g., drop `is_latest` column,
and run `vacuum` while the application inserts data, order breaks:
`vacuum` for some reason frees some slots and they get filled with new rows,
creating timestamp inversions.

So don't run `vacuum` on `status_changes` while the bot is running.
Run `cluster` to fix inversions if they occur (O(n) regardless of existing order).

Use `sql-scripts/check-timestamp-inversions.sql` to detect inversions.
