# sldownloader
The only drop that matters is a downloaded one

`sldownloader` is a small Go tool that retrieves Secret Lair product pages and extracts the **card names** and their **collector numbers** via Scryfall, OCR and a few heuristics. It writes one decklist per product in the format of [magic-preconstructed-decks](https://github.com/taw/magic-preconstructed-decks/), ready to drop into taw's `data/sld/sld/` folder.

---

## Features

- Scrapes product pages, either from a paginated catalog API or explicit URLs.
- Parses card lists, cleaning the output of any extra characters.
- Matches each product to its Scryfall edition to number the cards, keeping Scryfall's spelling of each name.
- Uses OCR on the image gallery, to discover the collector number from the image itself.
- Backfills missing numbers by inferring contiguous sequences when possible.
- Runs daily in GitHub Actions and opens a PR upstream with any new drops, listing products that failed.

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

From a checkout, `make build` builds `./sldownloader` with those flags set, and `make check` runs the tests and linters.

---

## Usage

You can run the tool by setting a starting page from which the catalog will be read (until there is no more data), with `-page 0` starting from the very beginning. It writes a `.txt` decklist per product into the current directory:

```bash
./sldownloader -page 1
```

or with explicit product page URLs, printing each decklist to stdout:

```bash
./sldownloader https://secretlair.wizards.com/eu/en/product/1002048/showcase-bloomburrow
```

`./sldownloader -version` prints the version and commit the binary was built from.

---

## License

MIT
