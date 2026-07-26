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

Each entry sets `parse:`.

- `html` — Telegram HTML. Use only `<b>`, `<i>`, `<code>`, `<a>`, `<blockquote>`.
- `raw` — plain text, no markup.
- `markdown` — rarely used.

Optional fields: `disable_preview: true` suppresses link previews;
`image:` attaches a picture; `weight:` biases random selection (ads).

## Punctuation

Match the existing convention exactly.

- A full prose sentence whose last token is an ordinary word ends with a period:
  `Only group admins can change the affiliate link.`
- A short action line that ends in a command takes **no** trailing period,
  whether the command is a clickable `/command` or a `<code>` example:
  - `Manage: /affiliate, reset: /reset_affiliate`
  - `To reset, /reset_affiliate`
  - `<code>/affiliate PASTE_LINK</code>`

A period after a command reads as part of the command
and looks wrong beside the other periodless action lines.

## Commands

- A command is clickable in Telegram only when written bare, outside any tag: `/reset_affiliate`.
- Wrap a command in `<code>` only when it must **not** be clickable —
  because it carries a placeholder or example argument:
  `<code>/affiliate LINK</code>`, `<code>/add CAMNAME</code>`.
- Placeholders are UPPERCASE: `LINK`, `CAMNAME`, `PASTE_LINK`, `YOUR_MESSAGE`.

## Whitespace and templates

Messages are Go `text/template`.
A blank line needs an explicit `{{ print "\n" }}`, as the `settings` message does.
