# Start links

Telegram deep links open a bot with a payload: `t.me/<bot>?start=<payload>`.
Tapping one makes Telegram send the bot the message `/start <payload>`.
The bot treats the payload three ways.

## Payload kinds

- **empty** — `/start` with no payload just sends the welcome message.
- **referral code** — a payload that is not a model link
  is treated as a referrer.
  When it is valid and not the user's own code the referral is applied,
  so the inviter gets credit.
- **`m-<model>`** — a subscribe deep link.
  The bot strips the `m-` prefix, takes `<model>` as a nickname,
  and auto-subscribes the user to that streamer.
  On success it also records a referral event for the model.

So `start=m-<model>` is the "subscribe to this model" link shown on the site
and in messages.

## Where it is recorded

A successful `m-<model>` auto-subscribe writes a referral event,
so the database counts subscribes that completed in the bot,
not raw link taps.
