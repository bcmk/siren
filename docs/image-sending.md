# Sending images

Each bot caches its images' Telegram file ids, since uploads cause most timeouts.
A notification or an ad passes two steps, joined in `cmd/bot/sending_pipeline.go`:
the download step, `cmd/bot/image_downloader.go` (`imageDownloader`),
then the send step, `cmd/bot/sender.go` (`imageCache`, `trySend`).
Any other message goes to the send step alone.
A photo is `photoParams` in `cmd/bot/base_chattable.go`.
`docs/image-sending.feature` specifies the behaviour as Gherkin scenarios.

## Telegram's side

- Photo uploads time out more often than text.
  Over the 30 days to 2026-10-18, on all eleven bots, when every photo was uploaded:

  | Sends                                 | Timeout rate |
  | ------------------------------------- | ------------ |
  | `/pics` replies, all photos           | 0.30%        |
  | Alerts to chats with images on, mixed | 0.25%        |
  | Alerts to chats with images off, text | 0.14%        |
  | Other replies, text                   | 0.10%        |
  | Ads, mostly text                      | 0.01%        |

  The photo rows carry 80% of the sends and 96% of the timeouts.
  On the `sc` bot, over the 40 days to 2026-10-17,
  calls to Telegram timed out every night at about 01:11–01:19 UTC.
  Between 01:00 and 01:30 UTC, one `/pics` photo in six timed out while text still got through,
  so large uploads stall when small requests do not.

- A request to the Telegram Bot API times out after `telegram_timeout_seconds`, set to 5 s.
  Over the week to 2026-10-20, with the limit at 10 s,
  the first photo of a `/pics` answer timed out in 1% of uploads, against 0.5% over 30 days,
  and took 2.3 s at p99 and 5.5 s at p99.7 when it succeeded.
  Its latency also counts the send in flight on its bot,
  so those figures bound the upload from above; retries are left out.
  A bot sends one message at a time, so a timeout stalls all its sends;
  5 s halves that stall; at most 0.33% of uploads succeeded after 5 s.
- `sendPhoto` uploads and delivers in one request.
  The Bot API has no way to upload a photo without sending it,
  so a bot learns an image's id only from a real send.
- A file id works in any chat, but only for the bot that uploaded it.
  `file_unique_id` is shared across bots, but nothing can be sent by it.
- A timed-out upload may still have reached the chat: the client cannot tell
- A refused file id comes back as a 400 whose description contains "file identifier"

## How a bot sends an image

1. A photo shows an image from a site, downloaded by its URL, or one shipped with the bot.
   An online alert or a `/pics` answer whose site reported no image shows the placeholder.
   So does a `/pics` answer before the checker's first report after a start,
   as the bot starts with no image URLs.
2. A notification enters the send queue only once its image downloads,
   when its photo shows an image from a site that the bot holds no file id for.
   - Until then it holds back its user's notifications behind it on that bot, in order,
     and the ads too, so an alert's ad never arrives before the alert.
     Other messages are sent meanwhile.
   - Once the download step holds `maxDownloadBacklog` messages,
     a new photo skips the download.
     It takes bytes downloaded within an `imageTTL`, or else goes with the placeholder.
   - One download serves the photos queued while it waits or runs,
     or within an `imageTTL` after it, whichever bots they are for
   - A pool runs at most `maxDownloads` downloads at once
   - Downloads wait in a queue that works like the send queue.
     Photos go in the send queue's order,
     so the downloads of a `/pics` answer start before those of waiting alerts.
     A user running `maxUserDownloads` downloads waits as a cooling user does,
     so a long `/pics` answer leaves the pool to others.
3. When its turn comes, a photo the bot holds a file id for goes by that id and uploads nothing.
4. Otherwise it uploads the image's bytes:
   a shipped image's, or those downloaded for the photo, however long ago.
5. A photo with no id, and no bytes it may upload, goes with the image-unavailable placeholder,
   `res/images/image-unavailable.<lang>.png`, built by `scripts/build-image-unavailable`.
   That happens when its download failed, when its id was refused or expired after it was queued,
   when the download step was full as it was queued,
   or while the bot has stopped uploading its image.
   In that last case the placeholder goes by its id alone, as uploads are stalling.
   A photo that cannot send the placeholder either
   goes as text opening with the image-unavailable line.
   An ad is dropped instead of either.
6. Telegram answers an upload with the photo's sizes, and the bot keeps the largest one's file id.

An image from a site shows only in messages queued within `imageTTL`, a minute, of its download,
since the site may put a new image under the same URL.
A message queued in time shows it however late it is sent.
So a `/pics` answer sent minutes after its alerts uploads its images again.
A shipped image never changes, so its id serves any message.
A bot keeps an id for `imageIDTTL`, an hour.

## When it fails

- **The download fails:** the photos waiting for it go with the placeholder.
  A photo the bot gains a file id for meanwhile goes by it instead.
  The next alert of that URL tries again.
- **Telegram refuses the id:** the photo goes by an id the bot gains meanwhile,
  uploads the bytes downloaded for it, or else goes with the placeholder
- **The upload times out:** the bot stops uploading the image for an `imageTTL`.
  Photos of it meanwhile go by a file id, with the placeholder by its id, or else as text.
  The timed-out photo goes the same way after `timedOutUploadPostpone`,
  or `groupCooldown` in a group or channel.

## Logs

- `sent_message_log.result` is -11 for a refused id and -12 for an ad dropped without a send attempt
- `sent_message_log.file_upload` marks the sends that carried an image's bytes
- `sent_message_log.latency` counts from when a message enters the send queue,
  so it leaves out a held notification's wait for its image
- `performance_log` times each download, kind 2, with `failed` in its data
