# Load Testing

`checker_only: true` runs a bot against a database with no Telegram side at all.
It exists to hold PostgreSQL under a realistic ingestion load for days,
with several bots against one server, each with a database of its own,
and nothing to deliver.

Every one of those databases installs TimescaleDB
and holds a scheduler slot out of `max_worker_processes` for good,
one per database on top of the single launcher the instance runs,
so read the worker-pool section of [TimescaleDB](timescaledb.md)
before adding databases to a server.
A pool that runs out stops granting parallel workers,
and the run's numbers sag with no sign of why in `performance_log`.

The mode is the absence of endpoints, and four guards carry it:

- `applyCheckerOnly` clears `endpoints` and `admin_endpoint` once the config has been checked whole,
  so a config's endpoint block needs no editing and every setup and webhook loop,
  which all key off the bots built from those endpoints, goes quiet on its own.
- `storeNotifications` returns before its insert, so a confirmed change queues nothing.
- `newStartupTimers` leaves the subscription-confirmation and notification-fetch tickers nil.
- `enqueueMessage` returns early, which stops the owner's two startup notices —
  the only sends addressed to `admin_endpoint` rather than to an endpoint's bot.

No notification is queued, claimed or deleted,
so the rows a copy arrives with stay where they are.
The startup resets of `notification_queue.sending` and `pending_subscriptions.checking`
run as they do on any start.

That makes the mode a rehearsal for a cluster migration:
point a copy of the production config at the new server, add `checker_only: true`,
and it applies the migrations and drives the status change ingestion there,
while production keeps serving from the old one.
What it leaves untested is the notification side —
the queue insert, its churn, and the autovacuum pressure that follows.
The rehearsal database proves the ingestion path and is then thrown away.
It holds none of the user-side writes production took meanwhile,
so the cutover starts from a fresh copy, taken with the bot stopped.

## What runs

The checker tick and everything it writes:
streamer upserts, nickname inserts, status change inserts, confirmation,
and the poll error count on `streamers`.
That count rises per streamer within a fetch that succeeded,
on a query error or an unknown status; a fetch that failed writes nothing at all.

Each instance queries the live site at production cadence,
from the host it runs on and with the affiliate credentials it is configured with,
so N instances multiply the site's request load by N.
Point them at a site you are willing to load, and watch for rate limiting.

Notifications are still built, so the per-subscriber fan-out query runs at production volume.
The writes they would drive are not.

That is the rule the guards follow:
a write is kept unless it only serves machinery this run does not have.
That covers the queue rows and the reports counting them, which wait on a sender.

## What does not run

No update ever arrives, so no command, subscription request or payment reaches the bot.

Nothing queues a notification, claims a row or deletes one,
and the reports count on `users` never rises.

Nothing is delivered, so the send-time statements are all absent:
the chat id lookup, the block increment and reset,
the sent message log, and the member count.

The blocked-send dice never rolls and its skip log never runs,
though neither is stopped by a guard: both sit inside the notification build,
behind a test for a translation that an endpointless run never has.

The subscription confirmation pass is not armed,
so no pending subscription is confirmed or denied, and no referral is credited.

## Reading a run

Read it for database load. Nothing is sent, so there is no delivery rate or latency to read.

The numbers live in the `performance_log` table and in `pg_stat_statements`.
`/performance` is a command, so it is unavailable here.

`notifications_count` counts rows built and dropped rather than rows queued,
and counts more of them than production would:
the blocked-send dice that drops a long-blocked chat never rolls,
so every subscriber of every change is counted.
`store_notifications_ms` covers the build and the store together, as it does in every mode,
but here the store returns at once and the build is all of it.

## Config

Copy a production config and set `checker_only: true`,
a `listen_address` of its own for each bot, and a database of its own.
`endpoints` and `admin_endpoint` need no editing: the mode drops them.
`admin_id` stays: it names the row the owner is created as.
Each instance opens two connections, the bot's and the fuzzy search daemon's,
which sits idle here but is still dialled.

`maintain_db_period_seconds` arms the BRIN maintenance,
but only `performance_log` grows in this mode, at two rows a tick:
`sent_message_log` and `received_message_log` get nothing,
and `status_changes`, the table the run does hammer, carries no BRIN index.
Set it to rehearse the statement, not to size it.

The binary needs `--checker-config` as well.
`period_seconds` in the bot config sets the tick,
and `min_request_interval_ms` in the checker config paces the requests within it.

Run it against a copy of production, never production.
It applies the migrations and resets the query stats, as any bot start does.
