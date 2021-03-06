__[Русский](README-ru.md)__

<img src="docs/icons/siren-dia.svg" height="23">&ensp;SIREN
===========================================================
Telegram bot for adult webcasts alerts

[![Build Status](https://travis-ci.org/bcmk/siren.png)](https://travis-ci.org/bcmk/siren)
[![GoReportCard](http://goreportcard.com/badge/bcmk/siren)](http://goreportcard.com/report/bcmk/siren)

This is the Telegram bot notifying you whenever cam models are online.
You subscribe to your favorite webcam models with __/add__ command.
We notify you whenever they start broadcasting.
The bot queries models statuses every minute.

Installation
------------

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

* __add__ _MODEL_ID_ — Add model
* __remove__ _MODEL_ID_ — Remove model
* __remove_all__ — Remove all models
* __list__ — Your model subscriptions
* __pics__ — Pictures of your models online
* __week__ _MODEL_ID_ — Working hours in the previous 7 days
* __help__ — Help
* __settings__ — Show settings
* __feedback__ _YOUR_MESSAGE_ — Send feedback

Substitute ___MODEL_ID___ with the actual model ID.
It is the same as __model name__ in Chaturbate and Stripchat.
For BongaCams you can find a model ID in the address line of your browser.

For models
----------

Add a link to your profile page and share it on Twitter, Instagram or other social media.
Telegram users clicking it follow you in our bot.
From that moment they receive a notification whenever you start broadcasting.
Use following links:

* Chaturbate:  
  <pre>https://siren.chat/cb/<b><i>YOUR_USERNAME</i></b></pre>
* Stripchat and xHamster Live:  
  <pre>https://siren.chat/sc/<b><i>YOUR_USERNAME</i></b></pre>
* BongaCams:  
  <pre>https://siren.chat/bc/<b><i>YOUR_USERNAME</i></b></pre>
* LiveJasmin:  
  <pre>https://siren.chat/lj/<b><i>YOUR_USERNAME</i></b></pre>
* CamSoda:  
  <pre>https://siren.chat/cs/<b><i>YOUR_USERNAME</i></b></pre>
* Flirt4Free:  
  <pre>https://siren.chat/f4f/<b><i>YOUR_USERNAME</i></b></pre>
* Streamate:  
  <pre>https://siren.chat/sm/<b><i>YOUR_USERNAME</i></b></pre>

Substitute ___YOUR_USERNAME___ with your actual model ID.
It is the same as model name in Chaturbate and Stripchat.

Recommended text: "Get a notification in Telegram whenever I'm online ___YOUR LINK___".

You can use these [floating icons](https://siren.chat/chic) for your Chaturbate profile.

Write to siren.chat@gmail.com in case of any questions.

Running your own bot
--------------------

Create and setup your bot using [@BotFather](https://telegram.me/BotFather) bot.

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

[Twitter](https://twitter.com/siren_tlg)

[WeCamgirls](https://www.wecamgirls.com/users/sirenbot)

[GitHub Pages](https://siren.chat)

[News Telegram channel](https://t.me/siren_telegram_bot)

[Reddit](https://www.reddit.com/user/siren_tlg)

[AmberCutie](https://www.ambercutie.com/forums/members/siren.53143/)
