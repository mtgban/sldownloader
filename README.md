# sldownloader
The only drop that matters is a downloaded one

`sldownloader` is a small Go tool that retrieves Secret Lair product pages and extracts the **card names** and their **collector numbers** via Scryfall, OCR and a few heuristics. The output format is a filename (or a series of filenames) compatible with [magic-preconstructed-decks](https://github.com/taw/magic-preconstructed-decks/) decklists. These files can be dropped in taw's project folder `data/sld/sld/` directly for use.

---

## Features

- Scrapes product pages, either from a paginated catalog API or explicit URLs.
- Parses card lists, cleaning the output of any extra characters.
- Retrieves card names from the page and associates it with Scryfall edition titles.
- Uses OCR on the image gallery, to discover the collector number from the image itself.
- Backfills missing numbers by inferring contiguous sequences when possible.

---

## Installation

The OCR support relies on [gosseract](https://github.com/otiai10/gosseract), so the Tesseract and Leptonica libraries need to be installed first:

```bash
# Debian/Ubuntu
sudo apt-get install -y tesseract-ocr libleptonica-dev libtesseract-dev

# macOS
brew install tesseract leptonica
```

Then install the tool itself:

```bash
go install github.com/mtgban/sldownloader@latest
```

On macOS the headers live in the Homebrew prefix, so point cgo at them:

```bash
CGO_CPPFLAGS="-I$(brew --prefix)/include" CGO_LDFLAGS="-L$(brew --prefix)/lib" go install github.com/mtgban/sldownloader@latest
```

---

## Usage

You can run the tool by setting a starting page from which the catalog will be read (until there is no more data), with `-page 0` starting from the very beginning:

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
