Feature: Image sending
  How a bot sends the images its messages show.
  docs/image-sending.md explains the mechanics.
  The image-unavailable text is the caption opening with "[[ 🚫 image unavailable ]]".

  Rule: A bot uploads an image once, then sends it by its file id

    Scenario: A fan-out uploads once
      Given a streamer with 200 subscribers on one bot
      When the streamer goes online
      Then the bot uploads the image once
      And sends it to the other 199 chats by its file id

    Scenario Outline: How long a file id serves an image
      Given the bot uploaded <image> and holds its file id
      When a message showing it is queued <when>
      Then the message goes by the file id

      Examples:
        | image                | when                                    |
        | an image from a site | within a minute of the image's download |
        | a shipped image      | within the hour the bot keeps the id    |

    Scenario: An image from a site goes stale
      Given the bot holds a file id for an image from a site
      When a message showing it is queued over a minute after its download
      Then the image downloads and uploads afresh

  Rule: Downloads run in parallel, and each user's alerts go once their own images are in

    Scenario: A user's alert goes as soon as their image is in
      Given a batch of 20 streamers going online, each image taking 0.5 s
      When the first user's image downloads
      Then that user gets the alert at once
      And at most 8 downloads run at a time, 3 per user

    Scenario: A slow image holds back only its own users
      Given user A's alert waits on a slow image
      When user B's image downloads
      Then B gets the alert
      And A's later notifications and ads on that bot wait behind A's alert
      And A's replies are sent meanwhile

  Rule: A /pics answer's images download ahead of waiting alerts

    Scenario: /pics passes an alert wave
      Given an alert wave has queued hundreds of downloads
      When a user types /pics
      Then the answer's images download as soon as download slots free

  Rule: One download serves an image across batches and commands

    Scenario: Two batches and a /pics share one download
      Given a streamer's alerts come in two batches seconds apart
      When someone types /pics within a minute of the download
      Then the image downloads once
      And /pics goes by the file id

  Rule: A timed-out upload pauses its image's uploads

    Scenario: The bot pauses the image's uploads
      When an upload of an image to chat A times out
      Then the bot stops uploading that image for a minute
      And A keeps its place in the send queue
      And A's photo goes again after 2 s, or 3 s in a group or channel

    Scenario Outline: What a photo goes as while its image's uploads are paused
      Given an upload of the photo's image timed out within the last minute
      And the bot holds <ids>
      When the photo reaches its turn
      Then it goes <as>

      Examples:
        | ids                        | as                             |
        | a file id for the image    | by the image's file id         |
        | the placeholder's id alone | with the placeholder by its id |
        | neither id                 | as the image-unavailable text  |

  Rule: The placeholder stands in whenever a photo cannot send its image

    Scenario: A file id gained before the photo's turn beats the placeholder
      Given a photo's image failed to download
      When another chat downloads the image afresh and uploads it before the photo's turn
      Then the photo goes with the real image by that file id

    Scenario Outline: The placeholder stands in
      Given a photo <reason>
      And the bot holds no file id for its image
      Then it goes with the placeholder, by its id or uploaded

      Examples:
        | reason                                                               |
        | whose download failed                                                |
        | whose id was refused                                                 |
        | whose id expired after it was queued                                 |
        | that reached a full download step with no bytes from the last minute |

  Rule: A refused file id falls back

    Scenario: Telegram refuses a file id
      When Telegram answers a send by id with 400 "wrong file identifier"
      Then the send is logged as -11
      And the photo goes again by a newer id, with its bytes, with the placeholder, or as text

  Rule: An ad goes with its image or not at all

    Scenario Outline: An ad is dropped
      Given an ad <case>
      Then the ad is dropped
      And the message behind it still goes

      Examples:
        | case                                         |
        | whose upload timed out                       |
        | to a group that takes no photos              |
        | whose image's uploads are paused, with no id |

    Scenario: The Ukraine ad's upload times out
      Given the bot holds no file id for the Ukraine ad's image
      When the Ukraine ad's upload times out on that bot
      Then it and every Ukraine ad on that bot in the next minute are dropped

    Scenario: An ad sent by id that times out is retried
      Given the bot holds a file id for the ad's image
      When the send times out
      Then the ad goes again after 10 s

  Rule: A download backlog never blocks the bot

    Scenario: The download step is full
      Given the download step holds 100000 messages
      When a new photo is queued
      Then it skips the download
      And it goes with bytes downloaded in the last minute, else with the placeholder, or as text
      And the bot keeps serving replies

  Rule: sent_message_log records image sends

    Scenario: What sent_message_log records
      Then file_upload marks the sends that carried an image's bytes
      And the result -11 marks a refused id
      And the result -12 marks an ad dropped without a send attempt
      And latency counts from entry to the send queue
