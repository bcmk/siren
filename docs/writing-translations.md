# Writing translations

All user-facing strings live in `res/translations/`, never in Go source.
Read this before adding or editing a message.

## Files and structure

- `common.{en,ru}.yaml` — messages shared by every site.
- `<site>.{en,ru}.yaml` — per-site messages and overrides (e.g. `chaturbate.en.yaml`).

Per endpoint the loader merges `common` with the site file,
then `noNils` asserts that every field of the `Translations` struct resolves.
A missing key crashes the bot at startup.
Always add a key to **both** `en` and `ru`.

## Parse modes

An entry sets `parse:` where it needs markup; omitting it parses as `raw`.
That is right for an ad, for a sub-template such as `show_kind`,
which takes the parse mode of the message that includes it,
and for a message with no markup.

- `html` — Telegram HTML. Use only `<b>`, `<i>`, `<code>`, `<a>`, `<blockquote>`.
- `raw` — plain text, no markup.
- `markdown` — rarely used.

Optional fields: `disable_preview: true` suppresses link previews;
`image:` attaches a picture; `weight:` biases random selection (ads).

## Punctuation

Match the existing convention exactly.

- A full prose sentence whose last token is an ordinary word ends with a period:
  `The basic service is free.`
- A short action line that ends in a command takes **no** trailing period,
  whether the command is clickable or a `<code>` example:
  - `Your models: {{ command "list" }}, remove them all: {{ command "remove_all" }}`
  - `To remove them all, {{ command "remove_all" }}`
  - `<code>{{ command "add" }} CAMNAME</code>`

A period after a command reads as part of the command
and looks wrong beside the other periodless action lines.

## Commands

- A command is clickable in Telegram only outside any tag:
  `{{ command "remove_all" }}`.
- Wrap it in `<code>` only when it must **not** be clickable —
  because it carries a placeholder or example argument:
  `<code>{{ command "add" }} CAMNAME</code>`, `<code>{{ command "feedback" }} YOUR_MESSAGE</code>`.
- Placeholders are UPPERCASE: `CAMNAME`, `YOUR_MESSAGE`.
- Never write the slash yourself; see Command mentions below.

## Command mentions

Write every command mention as `{{ command "add" }}`, never bare:
`{{ command "add" }} <code>CAMNAME</code>`.
Name the command as the bot registers it, without the slash, as `raw_commands` does.

Use `{{ short_command "add" }}` in the `commands` listing, which is read to be typed:
it writes the least a reader must type, a bare `add` in a private chat
and `/add@SirenBot` in a group or channel.
Bolding the name instead, as `<b>add</b>`, gives a channel reader nothing that works.

A channel drops a bare command, and in a group every bot present answers one,
so a command the bot prints must name the bot.
Translations are parsed once at startup and rendered at dispatch,
where the chat is known: the send path clones the templates,
binds `command` to write `/add` or `/add@botname`, and renders once.
The clone keeps the binding to that one send.

Nothing rewrites rendered text, and a translation says nothing about chats.
A misspelled `command` is a parse error at startup,
where a misspelled field would render `<no value>` to a user,
and a sub-template needs no dot, since a function does not read the data.

Some keys never reach a chat as a message and must not name a command at all.
They render for an API that takes no chat: menu entries, button labels, payment chrome.
The `search_*` keys are not rendered at all, but read as raw text into the web app,
so a mention there would reach the user verbatim.
`unaddressedKeys` in `cmd/bot/command_mention_test.go` lists them.
Nothing derives that list, so a render added with no chat needs a key added by hand.
A key that slips the list is not silent:
`command` is unbound until the send path binds it, so rendering with no chat fails.

`TestTranslationsMarkEveryCommand` fails on a command written bare or bolded as a name,
on a name no command has, and on a mention in any key that never reaches a chat.

## Whitespace and templates

Messages are Go `text/template`.
A blank line needs an explicit `{{ print "\n" }}`, as the `settings` message does.
