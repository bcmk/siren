# Telegram Stars subscriptions

Users buy extra subscription slots with Telegram Stars (currency `XTR`).
In a group or channel, a purchase raises the chat's limit, not the payer's.
`/more_subs` shows the referral link and, with `subs_tiers` configured, a package per tier;
without them, buying is off.
Each tier is a count and a total price in Stars.
`checkConfig` requires counts to ascend,
and the per-subscription price (cost / count) not to rise as the count grows,
so larger packages are cheaper per subscription and discounts stay non-negative.
`docs/more-subs.feature` specifies `/more_subs` and buying as Gherkin scenarios.

## Payment flow

1. `/more_subs` shows an inline keyboard of packages.
2. Tapping a package sends an invoice (`sendInvoice`, currency `XTR`).
   In a group or channel only an admin's tap sends one, since the invoice posts into the chat.
   A non-admin, per Telegram, gets a notice only they see.
   In a group or channel, a tap gets nothing if asking Telegram fails or the bot is shutting down.
3. `pre_checkout_query` is validated and answered (10s deadline).
4. `successful_payment` triggers `GrantStarPaymentSubs` (idempotent).
   It credits the chat the invoice is for, wherever the receipt arrives;
   in a channel the receipt comes as a channel post.

Each step is named in `received_message_log`:
`buy_callback`, `invoice`, `pre_checkout`, `successful_payment`.
The `buy_callback` and `successful_payment` replies
carry that name in `sent_message_log.command`;
the invoice and pre-checkout answers go straight to the Bot API,
so they are not logged as sent.

During the startup migration window,
`rejectForRedeliveryWhileMigrating` rejects payments
and group-to-supergroup migration messages with HTTP 503,
so Telegram redelivers them once the schema is ready.
We cannot defer in-process:
the webhook library returns 200 as soon as the update is enqueued,
so failing the HTTP request is the only way to get a redelivery.
`pre_checkout_query` has a ~10s deadline
and is answered immediately (rejected) instead.

## Identifiers and extensibility

Two strings are namespaced so other payment methods and products can be added
without format changes:

- Inline-button callback data: `buy:<method>:<arg>` (e.g. `buy:stars:10`).
  `handleBuyCallback` dispatches on `<method>`.
- Invoice payload: `<method>:<product>:<tg_chat_id>:<count>`
  (e.g. `stars:subs:123:10`), parsed by `parseStarsPayload`.

The `star_payments` ledger is product-agnostic:
it stores `product` and `quantity` rather than a subscription-specific column,
so the generic charge record stays the same as products are added.

Today the only product is `subs` (the `productSubs` constant).
Both `handlePreCheckoutQuery` and `handleSuccessfulPayment` guard on it,
so a payload for any other product is rejected
rather than miscredited as subscriptions.
Adding a product means extending those guards (and the crediting dispatch),
not just the payload format.

## Known gap: credit failure and refunds

Crediting happens in one transaction,
so a failed credit rolls back the `star_payments` insert
and the charge is not stored locally.
The bot logs the `telegram_payment_charge_id` before crediting,
so the logs still hold a breadcrumb.

Telegram retains the transaction regardless,
and Star refunds have no tight deadline,
so recovery does not need to be synchronous.

`handleSuccessfulPayment` re-checks the paid amount against the tier
and credits the charge regardless,
logging a warning on a mismatch (tiers changed between invoice and payment).
The reconciliation job can use that log to review such charges.

### Follow-up: reconciliation job (not yet implemented)

Build a periodic reconciliation that:

1. lists recent charges via `getStarTransactions`,
2. compares them against `star_payments`,
3. for any charge with no credited row, either credits it (preferred),
   or refunds it via `refundStarPayment`.

This covers both the credit-failure case,
and any `successful_payment` update lost to a crash,
since Telegram is the source of truth.
