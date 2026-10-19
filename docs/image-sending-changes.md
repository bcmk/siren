# Image sending: what changed

How the bot behaves differently since the download step and file id reuse came in,
against the version before them.
The list aims to be complete.
`docs/image-sending.md` describes how sending works now.

## One upload per image, not one per chat

- Before: every photo uploaded its image's bytes
- Now: the first upload's file id serves the bot's later photos of the image.
  For an image from a site, it serves messages queued within a minute of the download;
  for a shipped image, any message in the hour the bot keeps the id.
- Example: a streamer with 200 subscribers goes online on one bot.
  Before, 200 uploads; now, 1 upload and 199 sends by id.
  Uploads cause most timeouts, so the 199 sends by id rarely time out.

## Downloads run in parallel, and each user's alerts go once their own images are in

- Before: one goroutine downloaded a whole batch's images one after another, then queued the batch.
  A later batch waited behind it.
- Now: up to 8 downloads run at once, 3 per user.
  A slow image holds back only the notifications and ads of the users waiting on it, on that bot.
- Example: a batch has 20 streamers going online, and each image takes 0.5 s.
  Before, every alert in the batch waited 10 s, for all 20 downloads.
  Now, a user whose image arrives first gets the alert at once.

## A `/pics` answer's images download ahead of waiting alerts

- Before: they downloaded in turn, behind every batch fetched before them
- Now: downloads follow the send queue's order
- Example: an alert wave queues hundreds of downloads, then a user types `/pics`.
  Before, the answer waited for all of them; now, its images download as soon as slots free.

## One download serves an image across batches and commands

- Before: a download served only the photos in its batch
- Now: one download serves every photo queued while it waits or runs,
  and its bytes serve new messages for a minute
- Example: a streamer's alerts come in two batches seconds apart,
  then someone types `/pics` within the minute.
  Before, 2 downloads and a third for `/pics`; now, 1 download, and `/pics` goes by the file id.

## A timed-out upload is not repeated

- Before: a timed-out photo was retried as the same upload after 10 s.
  The chat moved behind other chats in the send queue,
  and it got a second copy if the first arrived after all.
- Now: the bot stops uploading the image for a minute.
  The timed-out chat keeps its place in the send queue,
  and gets the caption alone after 2 s, or 3 s in a group or channel,
  since the photo may have reached it.
  Other chats meanwhile get the photo by id if the bot has one,
  or text opening with "[[ 🚫 image unavailable ]]".
- Example: Telegram is slow, and an upload to chat A times out.
  Before, A got the same upload again 10 s later, possibly as a duplicate,
  and chats B and C uploaded too, and could time out as well.
  Now, A gets the caption, and B and C get the photo by id or the text.

## The placeholder shows only when no id serves the image

- Before: a photo whose download failed always showed the placeholder, uploaded each time
- Now: it shows the real image when the bot holds an id for it.
  Otherwise it shows the placeholder, sent by id once uploaded.
- Example: a site fails for a moment, but another chat uploaded that streamer's image 20 s ago.
  Before, the placeholder; now, the real image by id.

## A refused id is handled

- Before: the bot sent no ids, so Telegram refused none
- Now: a 400 "wrong file identifier" is logged as -11.
  The photo is sent again by a newer id, with its bytes, or as text.

## An ad goes with its image or not at all

- Before: a timed-out ad was retried like any other photo,
  and a group with no photo rights got the ad as text
- Now: an ad is dropped when its upload times out, when its group takes no photos,
  and while its image's uploads are paused.
  An ad sent by id that times out is still retried.
- Example: the Ukraine ad's upload times out.
  Before, it was retried, possibly as a duplicate;
  now, it and every Ukraine ad on that bot in the next minute are dropped.

## A download backlog no longer hangs the bot

- Before: once 1000 fetched batches waited for downloads, the bot hung for good, replies included
- Now: downloads never block the bot.
  Once the download step holds 100000 messages, a new photo skips the download:
  it goes with bytes downloaded in the last minute, or as text.

## Logs

- `sent_message_log` gains `file_upload`, the result -11,
  and the result -12 for an ad dropped without a send attempt
- Latency counts from entry to the send queue.
  In effect it did before, since a message was queued only once its batch had downloaded.
