## Problem

Telegram clients interpret `__text__` as formatting,
stripping underscores before the message reaches the bot.
Users can't type streamer names containing `__`
in Telegram commands.

## Solution

Telegram Mini App (Web App) opened via inline keyboard button.
User taps "Or Find and Add", searches in the Mini App,
taps a result, and the Mini App calls the add API directly.

## Search implementation

See `../siren-fuzzy-search/docs/search.md`
for the multi-leg approach used in the code.
