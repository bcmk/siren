---
name: tidy-docs
description: >-
  Tidy changed docs (markdown, CLAUDE.md, docs/*, .claude/skills/*) and code comments:
  trim each to the terse minimum,
  then wrap to 100 columns at discourse-unit boundaries.
  Use before committing doc or comment changes,
  or when asked to trim, reflow, or rewrap prose.
---

# tidy-docs

Two passes over changed docs and comments: trim, then wrap.
"Docs" means markdown and code comments both.
Trimming removes words; wrapping moves only line breaks.
Neither changes meaning.
This file follows its own rules — read it as a worked example.

## Scope

Only the working change set: changed markdown, and comments in changed source files.
Never sweep unrelated comments.

## Trim

For a code comment, one line is the default, or none — cut one that adds little over the code.
Keep a second line only when the code needs it.
Markdown has no such default: keep the prose the doc needs, cut only the bloat.

Either way, cut:

- a clause restating what the next lines (or the heading) show;
- a sentence repeating the one before it;
- rationale the reader does not need;
- "handles", "helper that", "this function" filler.

Keep what the thing is, a non-obvious edge case,
and the doc comment an exported identifier requires.

## Wrap

Lines at most 100 columns.
Keep each discourse unit — a clause read as one — on its own line when it fits.
Never break mid-phrase.
When a line must break, cut where the spoken pause is longest —
the order below tracks that pause, and you take the highest that still fits:

1. full stop — `.` `?` `!`
2. semicolon — `;`
3. em-dash — `—`
4. comma — `,`
5. natural pause — before a clause-opening conjunction or preposition, or after a colon
6. plain space (last resort)

100 is a ceiling, not a target:
never fill to 100 and break at the last space when a higher boundary sits earlier.

The commonest mistake is ending a line at a plain space mid-phrase —
`by / pasting`, `read as / one`, `out of whatever / the site's` —
when a comma or a natural pause sits earlier.
Break there, and name each break's boundary before you keep it.

## Leave alone

List markers and their indent, fenced or indented code, tables, URLs, inline code spans, headings,
blank lines, link definitions, front matter, YAML.
A lone token over 100 (a URL) may exceed it.

## Procedure

1. Collect the changed files (Scope).
2. Trim, then wrap. Rewrap only changed comment paragraphs;
   a changed markdown file may be reflowed whole.
3. Re-read Wrap, then re-check every line:
   it ends at the highest boundary that fits and splits no phrase. Fix any that does not.
4. `npx prettier --write <md files>`.
5. Check widths: `awk '{ if (length > 100) print FILENAME":"NR }' <files>`
   — URLs and code may exceed.
6. Report touched files in one line.
7. Before a commit, stage, then run `.claude/hooks/tidy-docs-approve.sh`.
   The gate blocks until that approval matches the staged content.
   A line rewritten after step 3 goes back to step 3, however small the edit:
   step 5 measures width, which is half the rule, and the approval speaks for every staged line.
