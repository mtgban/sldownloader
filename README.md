# sldownloader
The only drop that matters is a downloaded one

`sldownloader` is small Go tool that retrieves Secret Lair product pages to extracts the **card names** and their **collector numbers** via Scryfall, OCR and a few heuristics. The output format is a filename (or a series of filenames) compatible with [magic-preconstructed-decks](https://github.com/taw/magic-preconstructed-decks/) decklists. These files can be dropped in taw's project folder `data/boosters/sld/sld/` directly for use.

---

## Features

- Scrapes product pages, either from a paginated catalog API or explicit URLs.
- Parses card lists, clening the output of any extra characters.
- Retrieves card names from the page and associates it with Scryfall edition titles.
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
./sldownloader -page 1
```

or with an explict product page URL:

```bash
./sldownloader https://secretlair.wizards.com/eu/en/product/1002048/showcase-bloomburrow
```

---

## License

MIT
