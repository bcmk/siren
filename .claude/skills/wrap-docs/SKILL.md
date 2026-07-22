---
name: wrap-docs
description: >-
  Rewrap changed documentation (markdown, CLAUDE.md, docs/*)
  and code comments to 80 columns max,
  breaking only at discourse-unit boundaries
  per the project's docs splitting rules.
  Use before committing doc or comment changes,
  or when asked to reflow/rewrap/re-split prose.
---

# wrap-docs

Reflow prose in documentation and comments
so every line ends at a sentence or clause boundary
and no line exceeds 80 columns.
Here "docs" means both markdown documentation and code comments.
This is the executable form of the
"Wrap documentation ... and comments at 80 characters" rule
in CLAUDE.md (Code Style).

Only line breaks and whitespace change.
Never alter wording, spelling, or meaning.

## Scope

The target is the working change set, not the whole repo.
What counts as "the changes" depends on context:
uncommitted work in the tree, the last commit,
or the last few unpushed commits.
Figure out which applies from the conversation,
then rewrap only the docs it touches.

Cover both kinds of docs in that change set:

- Markdown docs — `CLAUDE.md`, `README.md`, `docs/*.md`,
  and any other `*.md`.
- Comments in the changed source files.
  Do not sweep unrelated comments across the repo.

## Wrapping rules

1. Hard limit: prose and comment lines are at most 80 columns.
2. Keep each elementary discourse unit — a clause that reads as one unit —
   on a single line when it fits within 80 columns.
3. Never break a line in the middle of a phrase.
4. When a line must break, pick the break point by this precedence,
   highest first:
   1. full stop — `.`, `?`, `!` (sentence boundary)
   2. semicolon — `;`
   3. em-dash — `—`
   4. comma — `,`
   5. natural pause — before a conjunction or preposition
      that opens a new clause, or after a colon
   6. plain space between words (last resort)

   Use the highest-precedence boundary that still keeps the line
   within 80 columns.
   Drop to a lower one only when no higher boundary fits.
   80 columns is a ceiling, not a target:
   never fill to 80 and break at the last space
   when a higher-precedence boundary sits earlier in the line.

## Preserve, do not touch

- List markers (`-`, `*`, `1.`) and their continuation indent.
  CLAUDE.md indents continuation lines to sit under the item text.
- Fenced code blocks and indented code, and tables —
  leave their line structure alone.
- URLs and inline code spans — never break inside them.
  A single token longer than 80 columns (a long URL) may exceed the limit;
  leave it.
- Headings, blank lines, and link reference definitions.
- Front matter and YAML.

## Procedure

1. Collect the target files (see Scope).
2. Rewrap each prose block per the rules.
   When editing comments, rewrap only the paragraphs you are changing;
   for changed markdown files a full-file reflow is fine.
3. Run prettier on markdown so markers and spacing stay normalized:
   `npx prettier --write <files>`.
   Its default `proseWrap: preserve` keeps your line breaks,
   so the wrapping survives.
4. Verify no prose line exceeds 80 columns except unbreakable tokens:
   `awk '{ if (length > 80) print FILENAME":"NR": "length }' <files>`
   then eyeball the hits — long URLs and code are allowed.
5. Report the files touched in one line. Do not paste file contents.
6. When the reflow precedes a commit, stage it,
   then record the run with `.claude/hooks/wrap-docs-ok.sh`.
   A commit hook blocks the commit until that record exists,
   and restaging anything afterwards invalidates it.
