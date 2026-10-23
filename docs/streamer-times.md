# Streamer times

`siren-check` prints when a streamer went online or offline,
and when the current show kind began.
Each site reports these in its own fields.
This doc records what each field means and the live observations behind it.

## Stripchat

- `wentIdleAt` on the cam (`/api/front/v2/models/<id>/cam`) is when the model's last stream ended,
  whether the model went to idle or straight to off.
  A switch between idle and off never moves it.
  `WentOfflineAt` reads it.
- `statusChangedAt` is when the current status began,
  and moves on every status change, idle to off and public to private alike:
  a live model's status is its show kind.
  So an offline model's `statusChangedAt` can be later than its stream's end,
  and `ShowKindSince` reads it, not `WentOnlineAt`.
- `get-statuses` lists only live models and lags the cam:
  polled every 10 s, it listed a model 19–36 s after the cam's `statusChangedAt`.

Observed on 2026-10-24 and 2026-10-25:

| Switch                         | Cases | `wentIdleAt`              |
| ------------------------------ | ----- | ------------------------- |
| Live to idle or off            | 70    | moved to the stream's end |
| Idle to off, no stream between | 32    | kept                      |
| Off to idle, no stream between | 4     | kept                      |

A stream that starts and ends between two polls
makes a switch between idle and off seem to move `wentIdleAt`.
The table leaves out two such switches, one on each day;
both fit such a stream.

## Chaturbate

- `last_broadcast` on the biocontext (`/api/biocontext/<name>/`) is, for an offline room,
  when its last stream ended, as a local time in UTC-7 with no zone.
  On 2026-10-25, a room last seen live at 12:58:26 UTC and first seen offline at 13:00:23
  had a `last_broadcast` of 05:59:45, which is 12:59:45 UTC.
  It stays UTC-7 under US standard time too:
  for rooms the bot last saw go offline in January 2026,
  `last_broadcast` read in UTC-7 lands within minutes of the recorded change, and none fits UTC-8.
  The check leaves out rooms that streamed again unrecorded,
  whose `last_broadcast` falls more than a few minutes after the recorded change.
- For a live or away room it can belong to the running stream or an earlier one,
  so `WentOfflineAt` reads it for an offline room alone.
  On 2026-10-25, 3 of 25 live rooms had one inside the running stream,
  and the rest one from 15 h to 4 days old.

## CAM4

- `lastBroadcast` on the profile (`/rest/v1.0/profile/<name>/info`), in Unix ms,
  is when the model's last stream started, live or not.
  No field says when a stream ended.
- On 2026-10-25, for 15 live rooms, the minutes since `lastBroadcast`
  matched the directory's minutes on air (`broadcastTime` on `/api/directoryCams`) to within 1.4.

## Checking again

- Stripchat: poll `get-statuses` for a few hundred live models every 10 s,
  and the cams of those whose stream ended, logging each change with both times.
- Chaturbate: poll the biocontext of the rooms live longest, one request every 1.5 s,
  as faster polling is refused,
  and compare `last_broadcast` with when a room was first seen offline.
  For winter, take rooms whose `unconfirmed_status` is offline
  with an `unconfirmed_timestamp` from December to February,
  and compare `last_broadcast` with that timestamp.
  Leave out rooms whose `last_broadcast` falls more than a few minutes after that timestamp:
  they streamed again unrecorded.
- CAM4: compare the minutes since a live room's `lastBroadcast` with its `broadcastTime`.
