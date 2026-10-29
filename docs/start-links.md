# Start links

Telegram deep links open a bot with a payload: `t.me/<bot>?start=<payload>`.
Tapping one makes Telegram send the bot the message `/start <payload>`.
The bot treats the payload three ways.

## Payload kinds

- **empty** — `/start` with no payload just sends the start message.
- **referral id** — a payload that is not a model link
  is treated as a referrer.
  When it is another chat's id on the same bot
  and comes from a new user in a private chat,
  the referral is applied, so the referrer gets credit.
  `docs/referrals.feature` specifies referrals as Gherkin scenarios.
- **`m-<model>`** — a subscribe deep link.
  The bot strips the `m-` prefix, takes `<model>` as a nickname,
  and auto-subscribes the user to that streamer.
  In a group or channel only a group admin can use it;
  anyone else is refused, subscriptions being the chat's own setup.
  Where the chat lets any member subscribe, any member can use it.
  On success it also records a referral event for the model.

So `start=m-<model>` is the "subscribe to this model" link shown on the site
and in messages.

A chat is greeted once the start message is queued for it.
For simplicity,
a chat counts as greeted if the bot held a record of it,
such as a logged message or a subscription, before recording greetings.
A greeted chat gets no start message when the payload has its own answer:
an `m-<model>` link, or a referral id in a private chat.
Deleting the conversation in Telegram does not reset this.

## Where it is recorded

A successful `m-<model>` auto-subscribe writes a referral event,
so the database counts subscribes that completed in the bot,
not raw link taps.
