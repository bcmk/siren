# Architecture diagrams

`docs/` holds Graphviz source for architecture diagrams as `docs/<name>.dot`,
with the rendered output checked in alongside as `docs/<name>.pdf`. Current
diagrams:

- `checker-architecture` — the `internal/checkers` type relationships (the
  `Checker` / `CheckerConfig` interfaces, the generic `BaseChecker[T]` struct,
  the per-site config and checker structs) and how `cmd/bot`'s `worker` uses
  them.

## Building

```
scripts/build-diagram                       # rebuild every docs/*.dot
scripts/build-diagram checker-architecture  # rebuild one diagram
```

Requires `graphviz`, `inkscape`, `exiftool`, and `qpdf`.

## Why the pipeline is `dot -> SVG -> Inkscape -> PDF`

Do not use `dot -Tpdf`. Graphviz's PDF (and PNG, and PS2) output goes through
the cairo + Pango stack, and that stack lays out italic text with visibly uneven
letter spacing — e.g. Inter Italic's `f`, whose ink overhangs its advance width,
collides with the following glyph; other fonts crowd around `/`. It is a known
cairo/Pango limitation: cairo quantizes glyph metrics, so at the body sizes used
here glyphs end up touching on one side with a gap on the other, and Pango 1.44's
switch to HarfBuzz for glyph extents made the small-size behaviour worse for many
fonts ("text looks stretched"). It is not specific to Inter, and it is not fixed
by newer freetype/harfbuzz/pango/librsvg (cairo itself has had no relevant fix),
by fontconfig hint overrides (`hintmetrics=false`, `hintstyle=hintnone`), or by
rendering the diagram at a larger scale.

Renderers checked:

| pipeline                                            | result                                 |
| --------------------------------------------------- | -------------------------------------- |
| `dot -Tpdf` (cairo + Pango)                         | bad italics                            |
| `dot -Tsvg` then `rsvg-convert -f pdf`              | bad italics (same cairo/Pango stack)   |
| `dot -Tps` then `ps2pdf`                            | loses Inter, falls back to Courier     |
| `dot -Tsvg` then `mutool convert`                   | loses Inter, falls back to a serif     |
| `dot -Tsvg` then Chrome `--headless --print-to-pdf` | good                                   |
| `dot -Tsvg` then `inkscape --export-type=pdf`       | good — and no extra browser dependency |

Inkscape's PDF export does its own glyph layout (double-precision positioning,
not cairo's metric-quantized path), so it renders the italics cleanly. Hence the
pipeline.

## Metadata stripping

Both cairo and Inkscape stamp the PDF with a `/Producer` string and a
`/CreationDate` (including the local timezone offset). `scripts/build-diagram`
runs `exiftool -all=` to clear the document info dictionary and then
`qpdf --linearize` to rewrite the file so no superseded metadata objects linger
in the bytes (exiftool's PDF edit is an incremental update — it warns that
deleted tags "may be recovered"; the qpdf rewrite is what actually drops them).
`qpdf --deterministic-id` then makes the output byte-reproducible (the file
identifier is a content hash, not a fresh UUID), so rebuilding an unchanged
diagram is a no-op in git. The committed PDFs have no creation date, no producer
string, and no filesystem paths.

## Conventions for new diagrams

- **Font:** Inter. Class/box names bold;
  secondary lines (stereotypes, package paths) italic.
- **Palette** — matching the dataflow-diagram conventions used elsewhere:
  white node borders, `#666666` edges, fills from `#e0ebff` (blue), `#d0f0cc` (green),
  `#fee3d4` (peach), `#fff0d6` (yellow), `#ffd777` (gold).
  Put the per-diagram colour key in a comment at the top of the `.dot`.
- **UML edge vocabulary** (for class/type relationship diagrams):
  - dashed line + hollow triangle → realization (implements an interface)
  - solid line + filled diamond → composition / ownership;
    Go struct embedding is drawn this way (label it `embeds`)
  - solid line + open diamond → aggregation (an injected or shared reference,
    e.g. `worker.checker`)
  - dashed line + open arrow → dependency (uses / creates / drives)
- Prefer collapsing many near-identical types into one box;
  use a placeholder name with the varying part in mathematical angle brackets
  (e.g. `⟨Site⟩Checker` covers `ChaturbateChecker`, `StripchatChecker`, …).
- After editing a `.dot`, rerun `scripts/build-diagram` and commit the updated
  `.pdf` alongside it.
