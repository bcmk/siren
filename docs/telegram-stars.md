# Telegram Stars subscriptions

Users buy extra subscription slots with Telegram Stars (currency `XTR`).
The `/buy_subs` command is enabled by configuring `subs_tiers`;
with no tiers configured, buying is off.
It shows fixed packages defined by `subs_tiers`.
Each tier is a count and a total price in Stars.
`checkConfig` requires counts to ascend,
and the per-subscription price (cost / count) not to rise as the count grows,
so larger packages are cheaper per subscription and discounts stay non-negative.

## Payment flow

1. `/buy_subs` shows an inline keyboard of packages.
2. Tapping a package sends an invoice (`sendInvoice`, currency `XTR`).
3. `pre_checkout_query` is validated and answered (10s deadline).
4. `successful_payment` triggers `GrantStarPaymentSubs` (idempotent).

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
- Invoice payload: `<method>:<product>:<chat_id>:<count>`
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
