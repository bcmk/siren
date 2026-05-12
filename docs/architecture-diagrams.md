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

Requires `graphviz`, `exiftool`, `qpdf`, the **Inter** and **JetBrains Mono NL**
fonts, and `svg2pdf` — the Rust [`svg2pdf-cli`](https://github.com/typst/svg2pdf)
crate, _not_ the older Cairo-based program that also goes by `svg2pdf` (see the
renderer table below). There is no prebuilt package for the Rust one; install a
Rust toolchain (e.g. via [`rustup`](https://rustup.rs)) and then:

```
cargo install svg2pdf-cli    # installs the `svg2pdf` binary into ~/.cargo/bin
```

Make sure `~/.cargo/bin` is on your `PATH`.

## Why the pipeline is `dot -> SVG -> svg2pdf -> PDF`

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

| pipeline                                            | result                                            |
| --------------------------------------------------- | ------------------------------------------------- |
| `dot -Tpdf` (cairo + Pango)                         | bad italics                                       |
| `dot -Tsvg` then `rsvg-convert -f pdf`              | bad italics (same cairo/Pango stack)              |
| `dot -Tps` then `ps2pdf`                            | loses Inter, falls back to Courier                |
| `dot -Tsvg` then `mutool convert`                   | loses Inter, falls back to a serif                |
| `dot -Tsvg` then the Cairo-based `svg2pdf`          | bad italics (same cairo stack — wrong `svg2pdf`!) |
| `dot -Tsvg` then Chrome `--headless --print-to-pdf` | good — but depends on a browser                   |
| `dot -Tsvg` then `inkscape --export-type=pdf`       | good — but pulls in the whole Inkscape app        |
| `dot -Tsvg` then `svg2pdf` (Rust `svg2pdf-cli`)     | good — small CLI, no GUI/browser dependency       |

The Rust `svg2pdf` parses the SVG with resvg's `usvg` and writes the PDF with
`pdf-writer`; it shapes text with `rustybuzz` and places glyphs at full
precision, not cairo's metric-quantized path, so the italics come out clean. It
replaced an `inkscape --export-type=pdf` step that produced the same result but
required installing all of Inkscape. (Chrome's headless print also works; it
just trades the Inkscape dependency for a browser one.)

### Font fallback — keep every glyph inside a covered font

`usvg` does _whole-run_ font fallback: if one glyph in a text run is missing
from the requested font, the **entire run** is re-shaped in a fallback face — a
single uncovered character in a class name drags the whole name into, say,
Menlo. The diagram avoids this by only using fonts that cover every glyph it
draws. The notable case is the `⟨` `⟩` placeholder brackets (U+27E8 / U+27E9):
**Inter has no glyph for them, JetBrains Mono does** — one more reason the
identifier text is set in JetBrains Mono NL (see the conventions below).

## Metadata stripping

`svg2pdf` stamps the PDF with a `/Producer (svg2pdf)` string — no dates, and no
file `/ID` of its own. `scripts/build-diagram` runs `exiftool -all=` to clear
the document info dictionary and then `qpdf --linearize` to rewrite the file so
no superseded metadata objects linger in the bytes (exiftool's PDF edit is an
incremental update — it warns that deleted tags "may be recovered"; the qpdf
rewrite is what actually drops them). `qpdf --deterministic-id` derives the
`/ID` that `--linearize` has to add from a content hash rather than from fresh
randomness. The committed PDFs have no producer string and no filesystem paths.

Note — unlike the old Inkscape step, `svg2pdf` serializes the page's font
resource dictionary in a nondeterministic (hash-map) order, and `qpdf` does not
normalize that, so two rebuilds of an unchanged `.dot` are **not**
byte-identical. Only rebuild and commit the `.pdf` when the `.dot` actually
changed; if a rebuild merely shuffled bytes, `git checkout` it.

## Conventions for new diagrams

- **Fonts:** Inter for prose — the diagram title and the secondary lines inside
  boxes (stereotypes, package paths), set italic. **JetBrains Mono NL** for
  identifiers: type names bold (the box title line), inline code on edge labels
  (field names, method calls) regular. Use the _NL_ (no-ligatures) family — it
  ships a static bold weight; plain "JetBrains Mono" is usually a variable font
  whose bold weight `usvg` can't reach.
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
  Keep the `⟨⟩` inside the JetBrains Mono run — Inter has no glyph for them
  (see the font-fallback note above).
- After editing a `.dot`, rerun `scripts/build-diagram` and commit the updated
  `.pdf` alongside it.
