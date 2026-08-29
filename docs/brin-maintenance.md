# BRIN Index Maintenance

The BRIN indexes on the `timestamp` of `sent_message_log`, `received_message_log`,
and `performance_log` rely on physical row order, which matches timestamp order.
If you alter such a table in any way, e.g., drop a column,
and run `vacuum` while the application inserts data, order breaks:
`vacuum` for some reason frees some slots and they get filled with new rows,
creating timestamp inversions.

So don't run `vacuum` on them while the bot is running.
Run `cluster` to fix inversions if they occur (O(n) regardless of existing order).

To detect inversions,
run `sql-scripts/check-timestamp-inversions.sql` with the table as a psql variable:
`psql -v table=sent_message_log -f <script>`.
