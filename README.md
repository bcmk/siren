__[Русский](README.ru.md)__

Telegram bot notifying you whenever BongaCams, Chaturbate, Stripchat and xHamster Live models are online
========================================================================================================

[![Build Status](https://travis-ci.org/bcmk/siren.png)](https://travis-ci.org/bcmk/siren)
[![GoReportCard](http://goreportcard.com/badge/bcmk/siren)](http://goreportcard.com/report/bcmk/siren)

![](res/icons/megaphone128x128.png)

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

* __add__ _model_ID_ — Add model
* __remove__ _model_ID_ — Remove model
* __remove_all__ — Remove all models
* __list__ — Your model subscriptions
* __online__ — Your models online
* __feedback__ _text_ — Send feedback
* __source__ — Show source code
* __language__ — This bot in other languages
* __help__ — Command list

You can find a model ID in an address line of your browser.

For models
----------

A model can add a link on her or his profile page.
A user clicking on it will automatically subscribe to this model.
The user receives a notification whenever the model starts broadcasting.

Format of models links:
* English bot for Chaturbate:  
  https://t.me/ChaturbateAlarmBot?start=m-model_id
* English bot for Stripchat and xHamster Live:  
  https://t.me/StripchatOnlineBot?start=m-model_id
* English bot for BongaCams:  
  https://t.me/BongacamsOnlineBot?start=m-model_id
* Russian bot for Chaturbate:  
  https://t.me/ChaturbateSirenBot?start=m-model_id
* Russian bot for Stripchat and xHamster Live:  
  https://t.me/StripchatSirenBot?start=m-model_id
* Russian bot for BongaCams:  
  https://t.me/BongacamsSirenBot?start=m-model_id

A model must replace _model_id_ to her or his actual model ID.
It is the same as model name in Chaturbate and Stripchat.

Recommended text: "Get a notification in Telegram whenever I'm online ___your link___".

You can use these [icons](https://github.com/bcmk/siren/tree/master/res/icons).

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

Donations
---------

* Bitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE
* Ethereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc
