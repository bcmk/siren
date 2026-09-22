# Status values

Checkers can return OR'd statuses (e.g., `StatusNotFound | StatusDenied`).
Before storing in the database, these are normalized to one of three values:
unknown (0), offline (1), or online (2).

# Storing status changes

Status changes are detected by comparing the in-memory cache of online streamers
(`unconfirmedOnlineStreamers`) against checker results.

## Online list checkers (e.g., Chaturbate)

1. Was in cache but not in result → offline
2. In result but not in cache → online

## Fixed list checkers (e.g., Twitch)

1. Was in cache, not in result, and was requested → offline
2. In result but not in cache → online
3. Not in cache, exists in DB, not in result, not already offline → offline
4. Not requested known streamer → unknown (unsubscribed)

# Timestamps

A timestamp can lead the clock by seconds, or by as much as the clock stepped back:
when it is not past the streamer's previous one, we advance it a second past that,
so a streamer's timestamps are strictly increasing.
We enforce their uniqueness with the unique index `ix_status_changes_streamer_id_timestamp`.

Readers comparing a timestamp to the present allow for the lead:
`ChangesFromToForStreamers` moves a later change to the window's end,
`streamerDuration` never goes below zero, and confirmation comes later by the lead.

# Previous status

Every row also stores `prev_status`, the status the change left behind.
The upsert of `streamers` returns `prev_unconfirmed_status`,
which the update has already moved aside, so the writer gets it for free.

A row is self-describing, so a period reads without the change preceding it.
The status a window opens in is its first change's `prev_status`,
or the streamer's current status where the window holds no change.
`ChangesFromToForStreamers` reads both, which is why it joins `streamers`.

# Row layout

Columns are ordered four-byte first, then the two-byte statuses.
A row is 40 bytes either way, but the order leaves four bytes of slack
that one more four-byte column can use without growing the row or the covering index.
Putting a `smallint` before an `integer` spends that slack on padding instead.

# Denormalization

The `streamers` table stores the last two statuses from `status_changes`:

- `unconfirmed_status`
- `unconfirmed_timestamp`
- `prev_unconfirmed_status`
- `prev_unconfirmed_timestamp`

This avoids expensive querying of the latest changes from `status_changes`.
These fields are used to find who is online and calculate durations.
Reliable combinations are (offline, online) and (online, offline).
If either status is unknown, duration data is unreliable.

We upsert streamers first to obtain integer IDs,
then bulk insert into `status_changes` with those IDs,
updating the denormalized fields in the same transaction.

# Constraints

`status_changes.status`, `status_changes.prev_status`
and `streamers.confirmed_status` are constrained
to (0, 1, 2) — unknown, offline, online.

We do not use foreign key constraints.
PostgreSQL enforces them via per-row trigger-based lookups,
which adds overhead to every bulk insert into `status_changes`.
Since all inserts go through a single well-tested code path,
application-level consistency is sufficient.

# Invariant: status_changes and streamers must be in sync

The unconfirmed statuses in `streamers`
must always match the latest entries in `status_changes`.
We use this invariant to ensure correctness.
This invariant is verified by `checkInv` in tests.

A second invariant holds within `status_changes` itself:
a row's `prev_status` never equals its `status`,
and always equals the previous row's `status` for that streamer.
No status change is recorded unless the status actually changed.

The bot needs no lock against the compactor:
it only appends rows newer than a streamer's latest, as Timestamps says,
and the compactor edits only old history.
Any other editor of `status_changes` history takes turns with the compactor:
it locks `status_changes_lock` in exclusive mode for its transaction, as each run does.
An editor takes `status_changes_lock` before any lock on or write to `status_changes`:
a run holds `status_changes_lock` while it waits on `status_changes`, so the other order deadlocks.
A run takes the migration lock shared and skips while a migration holds it,
so a migration waits for a run's commit.
The compactor stays stopped from a staged copy's prebuild through its cutover:
the copy spans transactions, and holds the lock only while its migrations run.
An editor writing rows inside a coverage deletes the coverages too,
or the compactor would never examine those rows.

# First offline status

For fixed list checkers (e.g., Twitch),
the first offline status must be recorded after subscription
even if the streamer was never seen online.
This is essential for calculating online duration.
Without the initial offline timestamp,
we cannot determine how long the streamer has been streaming
when we see it online for the first time.
If we have only unknown -> online transition,
the streamer could have been online much longer than we detected,
making duration data unreliable.

# Confirmation

Confirmation adds a delay before notifying users of status changes.
This prevents notification spam when streamers flicker online/offline.
A status is confirmed only after it remains stable for a configured duration.
Unknown status confirmations are immediate since they don't generate notifications.

# Compaction

`compactor` (`cmd/compactor`), one per bot, thins old history.
It deletes each short offline period, the offline row and the online row ending it,
so the online periods around it join.
Both rows go, so `prev_status` stays right without an update.
What counts as short depends on how old the period's end is,
by the compactor's `short_offline_rules`, which defaults to:

| End at least this old | Period shorter than | Rewrite |
| --------------------- | ------------------- | ------- |
| 7 days                | 15 minutes          | no      |
| 30 days               | 30 minutes          | no      |
| 60 days               | 60 minutes          | yes     |

Each rule is an object of `after_days`, `shorter_than_minutes` and `rewrite`, false when absent,
and the list ascends in both numbers.
Only the config file sets the rules:
`XRN_COMPACTOR_SHORT_OFFLINE_RULES` stops the compactor from starting,
and an env var naming a rule's field is ignored.

A period ending less than a week ago stays.
A period after an unknown status or ending in one stays:
deleting it would leave `prev_status` wrong.
A streamer's last two rows stay too, since `streamers` denormalizes them.

## Notation

Every time below is a `status_changes.timestamp` value, in seconds,
and a period ends at its online row's timestamp.

- $X$: the rows of `status_changes`, $x \in X$
  - $\operatorname{ts} x$: its `timestamp`
  - $\operatorname{status} x$: its `status`
  - $\operatorname{prev} x$: its `prev_status`
  - $\operatorname{streamer} x$: its streamer
- $R$: the rules from the config, $r_1, \dots, r_n$, ascending in both age and threshold
  - $\operatorname{age} r_i$: its age
  - $\operatorname{thr} r_i$: its threshold
- $C$: the coverages, the rows of `status_changes_lock`, $c \in C$
  - $\operatorname{age} c$: the age of the rule the coverage was made under
  - $\operatorname{thr} c$: that rule's threshold
  - $[\operatorname{begin} c, \operatorname{end} c)$: what it covers;
    the periods ending there were checked against $\operatorname{thr} c$
- $u_k$: a run, one transaction `period_seconds` after the previous run ends,
  give or take `period_jitter_seconds`, a third of the period by default,
  making one step per rule, youngest first
  - $\mathrm{now}$: its time
  - $\operatorname{ts} u_{k-1}$: the previous run's time, which its coverages store
  - $T_0$: the smallest `status_changes.timestamp`, or $\mathrm{now}$ when the table is empty
  - $\Delta$: `step_seconds`, above `period_seconds` plus a run's duration,
    or a lagging rule never catches up
- $s_i$: a step, the run's work for $r_i$
  - $\operatorname{begin} s_i$: where its band begins
  - $\operatorname{end} s_i$: where its band ends
  - $[\operatorname{begin} s_i, \operatorname{end} s_i)$: its band;
    we examine the periods ending here
  - $\operatorname{cov} s_i$: the coverage we are about to store for the step

The jitter keeps the compactors sharing a server from compacting at once:
a node's restart starts them together, and a fixed period would keep them in step.

The coverage starts at the next older rule's age, the oldest rule's at $T_0$.
The age counts from the later of $\mathrm{now}$ and $\operatorname{ts} u_{k-1}$:
from a run behind the previous one, the start would land behind the coverages holding it.

$$
\operatorname{begin} \operatorname{cov} s_i = \begin{cases}
\max(T_0,\ \max(\mathrm{now}, \operatorname{ts} u_{k-1}) - \operatorname{age} r_{i+1}) & i < n \\
T_0 & i = n
\end{cases}
$$

The step's band starts at the youngest end of a coverage holding that start, or at the start.
Only a coverage with a threshold at least the rule's counts:
its rule already compacted what this one would.
Reconfiguring with a bigger $\operatorname{age} r_{i+1}$
moves $\operatorname{begin} \operatorname{cov} s_i$ to older history than the rule's own coverage,
so the step starts over.
We ignore a coverage beginning younger than $\operatorname{begin} \operatorname{cov} s_i$
in this case for simplicity, although the step's band could jump over it:
it walks it again instead, which is harmless:

$$
\operatorname{begin} s_i = \max\bigl(\{\, \operatorname{end} c : c \in C,\
\operatorname{thr} c \ge \operatorname{thr} r_i,\
\operatorname{begin} c \le \operatorname{begin} \operatorname{cov} s_i \,\}
\cup \{\, \operatorname{begin} \operatorname{cov} s_i \,\}\bigr)
$$

It ends a step further, but not younger than the rule's age, and so does the coverage.
A run ahead of the clock moves the next runs' starts with it,
so a clock jump ahead only delays compaction:

$$
\operatorname{end} s_i = \max\bigl(\operatorname{begin} s_i,\
\min(\operatorname{begin} s_i + \Delta,\
\mathrm{now} - \operatorname{age} r_i)\bigr)
$$

$$
\operatorname{end} \operatorname{cov} s_i = \operatorname{end} s_i
$$

## Consequences

- A step skips a coverage holding its start with a threshold at least its rule's
- History older than the next older rule's age is that rule's:
  its bigger threshold covers this one's.
  So each stretch of a backlog is swept by one rule.
- A coverage not holding the new one's start is forgotten, and its stretch gets walked again.
  That is harmless.
- Once a rule has caught up, its step examines only the time since the last run, one period.
  A restart resumes where it stopped.

## A run

A run makes one step per rule, youngest first, all in one transaction.
The compactor connects as the bot, whose login owns the tables,
and needs `status_changes_lock` and `status_changes_vacuum_queue`,
which the migrator's prebuild or the bot's start creates.

### Locking

A run locks `status_changes_lock` in exclusive mode, or skips when another run holds it.
It also takes the migration lock shared and skips while a migration holds it,
so a migration waits for the run's commit instead.
Then it takes the coverages, deleting them.

### Deleting

For each step $s_i$, the run computes $\operatorname{begin} s_i$ and $\operatorname{end} s_i$
and runs one delete statement over the step's band.
For each $e \in \{\, x \in X : \operatorname{status} x = \mathrm{online},\
\operatorname{prev} x = \mathrm{offline},\
\operatorname{ts} x \in [\operatorname{begin} s_i, \operatorname{end} s_i) \,\}$,
one index probe finds its start $b$, the previous status change of $\operatorname{streamer} e$.
The pair $(b, e)$ goes when:

- $\operatorname{ts} e - \operatorname{ts} b < \operatorname{thr} r_i$;
- $\operatorname{prev} b = \mathrm{online}$;
- $\operatorname{ts} e$ precedes the streamer's `prev_unconfirmed_timestamp`,
  so neither row is among its last two.

No $\operatorname{prev}$ needs an update:
the row after $e$ carries $\mathrm{online}$, the status of the row before $b$.
A period among a streamer's last two rows as a rule passes it waits for the next older rule,
and stays for good only once the oldest rule has passed it.

Every scan has fixed time bounds, planned with their values,
so a step reads only the chunks they overlap, and in time order every page is written once per rule.
Only the oldest row's lookup opens every chunk, so a run makes it before its transaction,
and the locks it takes end with the statement.

### Coverages

For each step,
the run stores $\operatorname{cov} s_i$ with $\operatorname{age} r_i$ and $\operatorname{thr} r_i$,
stamped with the run's time, $\max(\mathrm{now}, \operatorname{ts} u_{k-1})$, and commits.

### Vacuuming

A run vacuums before its transaction,
at a 20 ms vacuum cost delay, the same as the prebuild's.
Autovacuum is not relied on: the compactor knows which chunks it dirtied,
so it vacuums exactly those, on its own backend and cost delay,
rather than tuning autovacuum per deployment and sharing its workers with every database.
A vacuum frees space for reuse alone, and a closed chunk takes no inserts,
so only a rewrite, below, gives the space compaction frees back to the disk.

A run vacuums at most one chunk, so vacuums never pile up in one run.
The chunks a step finishes, as below, go into `status_changes_vacuum_queue`,
in the same transaction as the deletes, each chunk once.
Each run starts by vacuuming the chunk at the front of the queue,
so the newest dead rows it finds are a period old.
Right after a run's deletes,
any query started before them would keep the rows
and send the chunk back to the queue on every run.

A chunk leaves the queue only after a vacuum has removed all its dead rows,
so a restart, a shutdown or a long query never loses a vacuum.
A vacuum cannot remove deleted rows that some other session's query,
started before the deletes, can still see.
In that case the chunk goes to the back of the queue, and a later vacuum removes the rows.
Until then, their pages stay out of the visibility map.

A vacuum never overlaps a migration: it takes the migration lock shared.
A migration waits for at most the one chunk being vacuumed,
and a vacuum the lock turns away leaves its chunk in the queue.
A vacuum waits at most 5 s for its chunk, which another session seldom holds,
and a chunk held longer goes to the back of the queue, so the chunks behind it go on.

A step finishes chunk $k$ when $\operatorname{end} k + \operatorname{thr} r_i + \delta$,
the chunk's end plus the rule's threshold and the vacuum delay $\delta$,
lies in $(\operatorname{begin} s_i,\ \operatorname{end} s_i]$.
A deleted period starts at most a threshold before its end,
so no later delete of the rule reaches the chunk, and each rule vacuums a chunk once.
After a change of the rules or of the delay,
a chunk whose end a band has jumped past waits for an older rule's vacuum,
and past the oldest rule for autovacuum.
Until its vacuum, a chunk's compacted pages stay out of the visibility map,
so the bot's index-only scans read them from the heap:
for a week plus the threshold, the delay and a period,
or, after a jump, until an older rule's vacuum.

The delay staggers the bots sharing a server, whose chunk ends fall at the same moments.
It is hashed from the database's name, below `vacuum_delay_max_seconds`, an hour by default,
and counts the history a band passes, which a caught-up rule passes at the clock's pace.

A vacuum forces its index pass with `index_cleanup on`:
under 2% of pages with dead rows, PostgreSQL skips it and leaves those pages not all-visible.

### Rewriting

A rule with `rewrite` rewrites each chunk it finishes with `vacuum full`, then vacuums it.
The rewrite gives the chunk's free space back to the disk
and leaves its visibility map empty, which the vacuum fills.
A queued rewrite runs only while some rule has `rewrite`, so turning it off stops those too.

A rewrite locks the chunk out for its course, seconds for a week of a large bot,
and ignores the cost delay.
So the compactor refuses `rewrite` on a rule under 36 days old,
where the bot's month grid, reading 35 days back, would wait on it.
The bot plans each read with its window, so it locks only the chunks the window holds.

A rewrite never waits for its lock:
waiting, it would queue the chunk's readers behind it and fail `pg_dump`'s nowait locks.
A chunk another session holds goes to the back of the queue.
Once the rewrite is done, or the disk has no room for its copy,
the chunk is left to the vacuum alone,
so a restart or a requeue for dead rows doesn't rewrite it again.

A rewrite copies the rows that remain, so its time follows them, not the rows deleted.
On a rule deleting little, it costs nearly a full copy for little space.

### Logging

Each run that commits logs a `performance_log` row of kind 4 with its duration,
the chunks vacuumed, one or none, the vacuum's time if it vacuumed a chunk, the rows deleted,
each step's end, lag and delete time, the time after the vacuum, and the chunks left queued.

# Indexes

Status change insertion and confirmation are performance-critical —
they run on every checker cycle and must complete quickly.
When modifying these queries, ensure indexes are optimized for query performance
(e.g., cover all required fields if index-only scans are possible).
