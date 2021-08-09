__[Русский](README-ru.md)__

<img src="cmd/site/icons/siren-dia.svg" height="23">&ensp;SIREN
===============================================================
Telegram bot for webcasts alerts

[![Build Status](https://travis-ci.org/bcmk/siren.png)](https://travis-ci.org/bcmk/siren)
[![GoReportCard](https://goreportcard.com/badge/bcmk/siren)](https://goreportcard.com/report/bcmk/siren)

This is the Telegram bot notifying you whenever webcasts are online.
You subscribe to your favorite streamers with __/add__ command.
We notify you whenever they start broadcasting.
The bot queries channels statuses every minute.

Installation
------------

* Twitch: [t.me/TwitchSirenBot](https://t.me/TwitchSirenBot)
* Chaturbate #1: [t.me/ChaturbateAlarmBot](https://t.me/ChaturbateAlarmBot)
* Chaturbate #2: [t.me/ChaturbateAlertsBot](https://t.me/ChaturbateAlertsBot)
* Stripchat and xHamster Live: [t.me/StripchatOnlineBot](https://t.me/StripchatOnlineBot)
* BongaCams: [t.me/BongacamsOnlineBot](https://t.me/BongacamsOnlineBot)
* LiveJasmin: [t.me/LiveJasminSirenBot](https://t.me/LiveJasminSirenBot)
* CamSoda: [t.me/CamSodaSirenBot](https://t.me/CamSodaSirenBot)
* Flirt4Free: [t.me/Flirt4FreeSirenBot](https://t.me/Flirt4FreeSirenBot)
* Streamate: [t.me/StreamateSirenBot](https://t.me/StreamateSirenBot)

Commands
--------

* __add__ _CHANNEL_ — Subscribe to a channel
* __remove__ _CHANNEL_ — Remove a channel
* __remove_all__ — Remove all subscriptions
* __list__ — List your subscriptions
* __pics__ — Pictures of your online subscriptions
* __week__ _CHANNEL_ — Online hours in the previous 7 days
* __help__ — Help
* __settings__ — Show settings
* __feedback__ _YOUR_MESSAGE_ — Send feedback

Substitute ___CHANNEL___ with the actual channel or model ID.
It is the same as __model name__ in Chaturbate and Stripchat.
For BongaCams you can find a model ID in the address line of your browser.

For streamers
-------------

Here are details [For streamers](https://siren.chat/streamer)

We offer several ways you can use our bot:

* You can share your own notification link to your users. They will be notified in Telegram whenever you are online
* You can add our bot to your Telegram channel or group. Subscribers will be notified whenever you are online
* You can use our floting icons in your Chaturbate profile via [Chaturbate icons constructor](https://siren.chat/chic)

Write to siren.chat@gmail.com in case of any questions.

Running your own bot
--------------------

Create and set up your bot using [@BotFather](https://telegram.me/BotFather) bot.

You need an SSL certificate and a key for your bot.
You can obtain a certificate in Let's Encrypt or other certificate authority.

The bot uses [webhooks](https://core.telegram.org/bots/webhooks) to receive updates.

Create JSON configuration and YAML translation files.
A configuration is described in [config.go](https://github.com/bcmk/siren/tree/master/cmd/bot/config.go).
An example of translation are in [common.en.yaml](https://github.com/bcmk/siren/tree/master/res/translations/common.en.yaml) and [chaturbate.en.yaml](https://github.com/bcmk/siren/tree/master/res/translations/chaturbate.en.yaml).

Build cmd/bot. Run this executable with a path to config file as an argument.

Privacy policy
--------------

We do not store any sensitive personal information.
We store only your Telegram chat ID that is essential for core functionality of the bot.
Telegram chat ID is just a number which we use to send you notifications.

Links
-----

[Site](https://siren.chat)

[Twitter](https://twitter.com/siren_tlg)

[WeCamgirls](https://www.wecamgirls.com/users/sirenbot)

[News Telegram channel](https://t.me/siren_telegram_bot)

[Reddit](https://www.reddit.com/user/siren_tlg)

[AmberCutie](https://www.ambercutie.com/forums/members/siren.53143/)
