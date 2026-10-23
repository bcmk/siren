# Telegram send stalls

Sometimes Telegram times out or fails a bot's messages for minutes on end.
It happens in two ways: a nightly stall window that hits every bot, and a rare stall of one bot.
The bot resends a message that timed out or got a 5xx answer, so a stall delays messages.
It never resends a maintenance notice, nor an ad upload that timed out.

## The nightly window

Nearly every night from 01:10 to 01:20 UTC, messages on every bot time out far more often,
peaking at 01:12–01:13.
Over the three weeks to 2026-10-23,
4–47% of a bot's attempts to send a message timed out in the window,
against 0–5% over the rest of the day, and a message waited up to 6 minutes.
Photo uploads time out most often:

| Messages in the window, all bots      | Timeout rate |
| ------------------------------------- | ------------ |
| Photo uploads                         | 20%          |
| Other messages: text, or photos by id | 8%           |
| Text replies to commands              | 0%           |

From 00:50 to 01:00 UTC, each row stays at 0.2% or below.

The window showed on nearly every day of the 120 days to 2026-10-23, at the same UTC time.
Its cause is unknown, and moving from DigitalOcean to Scaleway did not change it.

## A single bot's stall

Rarely, one bot's messages stall while the other bots' go through.
On 2026-10-22, from 20:48 to 20:56 UTC, 38–86% of the `cb` bot's attempts failed each minute,
text and photos alike, and notifications waited up to 9 minutes.
Its keep-alive `getMe` on the same connection never failed,
and the stall ended in a burst of `502 Bad Gateway` answers, so it was on Telegram's side.
In the three weeks before, no other stall showed outside the nightly window.

## Finding them

`sent_message_log` holds each attempt to send a message.
This query lists a bot's minutes with at least five attempts where a third or more failed:

```sql
select
	to_timestamp(timestamp / 60 * 60) as minute,
	avg((result in (-3, -2, -13, -1))::int) as failed
from sent_message_log
where timestamp >= extract(epoch from now() - interval '1 day')::bigint
and result not in (-6, -10, -12)
group by 1
having count(*) >= 5 and avg((result in (-3, -2, -13, -1))::int) >= 1.0 / 3
order by 1
```

Results -3, -2 and -13 are a timeout, a network error and a 5xx answer.
Result -1 is an unknown error, which held the 5xx answers before -13 existed.
Results -6, -10 and -12 never reach Telegram, so the query leaves them out.
Three such minutes in a row make a single bot's stall.
The nightly window can stay below that bar and shows as a raised share of timeouts instead.
A failed keep-alive call shows in the bot's log as `keep-alive getMe failed`.
