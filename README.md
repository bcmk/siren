__[Русский](README-ru.md)__

Telegram bot notifying you whenever BongaCams, Chaturbate, Stripchat and xHamster Live models are online
========================================================================================================

[![Build Status](https://travis-ci.org/bcmk/siren.png)](https://travis-ci.org/bcmk/siren)
[![GoReportCard](http://goreportcard.com/badge/bcmk/siren)](http://goreportcard.com/report/bcmk/siren)

![](docs/icons/megaphone128x128.png)

You just subscribe to your favorite models with __/add__ command.
Then you receive a notification whenever they are online.
The bot queries models statuses every minute.

Installation
------------

* English bot for Chaturbate: [t.me/ChaturbateAlarmBot](https://t.me/ChaturbateAlarmBot)
* English bot for Stripchat and xHamster Live: [t.me/StripchatOnlineBot](https://t.me/StripchatOnlineBot)
* English bot for BongaCams: [t.me/BongacamsOnlineBot](https://t.me/BongacamsOnlineBot)
* Russian bot for Chaturbate: [t.me/ChaturbateSirenBot](https://t.me/ChaturbateSirenBot)
* Russian bot for Stripchat and xHamster Live: [t.me/StripchatSirenBot](https://t.me/StripchatSirenBot)
* Russian bot for BongaCams: [t.me/BongacamsSirenBot](https://t.me/BongacamsSirenBot)

Commands
--------

* __add__ _MODEL_ID_ — Add model
* __remove__ _MODEL_ID_ — Remove model
* __remove_all__ — Remove all models
* __list__ — Your model subscriptions
* __online__ — Your models online
* __feedback__ _text_ — Send feedback
* __source__ — Show source code
* __language__ — This bot in other languages
* __help__ — Command list

Substitute ___MODEL_ID___ with the actual model ID.
It is the same as __model name__ in Chaturbate and Stripchat.
For BongaCams you can find a model ID in the address line of your browser.

For models
----------

Add a link to your profile page and share it on Twitter, Instagram or other social media.
Telegram users clicking it follow you in our bot.
From that moment they receive a notification whenever you start broadcasting.
Use following links:

* English bot for Chaturbate:  
  <pre>https://t.me/ChaturbateAlarmBot?start=m-<b><i>MODEL_ID</i></b></pre>
* English bot for Stripchat and xHamster Live:  
  <pre>https://t.me/StripchatOnlineBot?start=m-<b><i>MODEL_ID</i></b></pre>
* English bot for BongaCams:  
  <pre>https://t.me/BongacamsOnlineBot?start=m-<b><i>MODEL_ID</i></b></pre>
* Russian bot for Chaturbate:  
  <pre>https://t.me/ChaturbateSirenBot?start=m-<b><i>MODEL_ID</i></b></pre>
* Russian bot for Stripchat and xHamster Live:  
  <pre>https://t.me/StripchatSirenBot?start=m-<b><i>MODEL_ID</i></b></pre>
* Russian bot for BongaCams:  
  <pre>https://t.me/BongacamsSirenBot?start=m-<b><i>MODEL_ID</i></b></pre>

Substitute ___MODEL_ID___ with your actual model ID.
It is the same as model name in Chaturbate and Stripchat.

Recommended text: "Get a notification in Telegram whenever I'm online ___YOUR LINK___".

You can use these [icons](https://github.com/bcmk/siren/tree/master/docs/icons).

Write to sirenbot@protonmail.com in case of any questions.

Running your own bot
--------------------

Create and setup your bot using [@BotFather](https://telegram.me/BotFather) bot.

You need an SSL certificate and a key for your bot.
You can obtain a certificate in Let's Encrypt or other certificate authority.

The bot uses [webhooks](https://core.telegram.org/bots/webhooks) to receive updates.

Create JSON configuration and JSON translation files.
A configuration is described in [config.go](https://github.com/bcmk/siren/tree/master/cmd/bot/config.go).
An example of translation is in [bongacams.json.example](https://github.com/bcmk/siren/tree/master/res/translations/bongacams.json.example).

Build cmd/bot. Run this executable with a path to config file as an argument.

GitHub Pages
------------

[Pages](https://bcmk.github.io/siren)

Social
------

[Twitter](https://twitter.com/sirenbot2)

[Instagram](https://instagram.com/sirenbot)

[WeCamgirls](https://www.wecamgirls.com/users/sirenbot)
