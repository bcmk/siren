__[Русский](README.ru.md)__

Telegram bot notifying you whenever BongaCams, Chaturbate, Stripchat and xHamster Live models are online
========================================================================================================

[![Build Status](https://travis-ci.org/bcmk/siren.png)](https://travis-ci.org/bcmk/siren)
[![GoReportCard](http://goreportcard.com/badge/bcmk/siren)](http://goreportcard.com/report/bcmk/siren)

Telegram bot notifies you whenever your favorite BongaCams, Chaturbate or Stripchat models are online.
The bot queries models statuses every minute.

Installation
------------

* English bot for Chaturbate: https://t.me/ChaturbateAlarmBot
* English bot for Stripchat and xHamster Live: https://t.me/StripchatOnlineBot
* English bot for BongaCams: https://t.me/BongacamsOnlineBot
* Russian bot for Chaturbate: https://t.me/ChaturbateSirenBot
* Russian bot for Stripchat and xHamster Live: https://t.me/StripchatSirenBot
* Russian bot for BongaCams: https://t.me/BongacamsSirenBot

Commands
--------

* __add__ _model ID_ — Add model
* __remove__ _model ID_ — Remove model
* __remove_all__ — Remove all models
* __list__ — Your subscriptions list
* __donate__ — Donation instructions
* __feedback__ _text_ — Send feedback
* __source__ — Show source code
* __language__ — This bot in other languages
* __help__ — Command list

You can find a model ID in an address line of your browser.

Running your own bot
--------------------

Create and setup your bot using [@BotFather](https://telegram.me/BotFather) bot.

You need a certificate and a key for your bot.
You can build them using the script [buildkeys](scripts/buildkeys).

The bot uses [webhooks](https://core.telegram.org/bots/webhooks) to receive updates.
You can find the script to setup a webhook for your bot at [setwebhook](scripts/setwebhook).

Create JSON configuration and JSON translation files.
A configuration is described in [config.go](cmd/bot/config.go).
An example of translation is in [bongacams-translation.json.example](strings/bongacams-translation.json.example).

Build cmd/bot. Run this executable with a path to config file as an argument.

Donations
---------

* Bitcoin: 1PG5Th1vUQN1DkcHHAd21KA7CzwkMZwchE
* Ethereum: 0x95af5ca0c64f3415431409926629a546a1bf99fc
