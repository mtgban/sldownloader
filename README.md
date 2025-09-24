# sldownloader
The only drop that matters is a downloaded one

`sldownloader` is small Go tool that retrieves Secret Lair product pages to extracts the **card names** and their **collector numbers** via OCR and a few heuristics. The output format is a filename (or a series of filenames) compatible with [magic-preconstructed-decks](https://github.com/taw/magic-preconstructed-decks/) decklists.

Given the nature of OCR, results on the extracted numbers may vary, and should always be validated. However the output files from a run should be pretty stable, and they can be dropped in taw's project folder `data/boosters/sld/sld/` directly for use.

---

## Features

- Scrapes product pages, either from a paginated catalog API or explicit URLs.
- Parses card lists, clening the output of any extra characters.
- Uses OCR on the image gallery, to discover the collector number from the image itself.
- Backfills missing numbers by inferring contiguous sequences when possible.

---

## Installation

```bash
go install github.com/mtgban/sldownloader.git
```

---

## Usage

You can run the tool by setting a starting page from which the catalog will be read (until there is no more data):

```bash
./sld-scraper -page 1
```

or with an explict product page URL:

```bash
./sld-scraper https://secretlair.wizards.com/eu/en/product/1002048/showcase-bloomburrow
```

---

## License

MIT
