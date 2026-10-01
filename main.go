package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/PuerkitoBio/goquery"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/otiai10/gosseract/v2"
)

// Wizards can answer 503 with a Retry-After of an hour, which the default
// backoff waits out in full before every retry; never wait past RetryWaitMax
func cappedBackoff(minWait, maxWait time.Duration, attempt int, resp *http.Response) time.Duration {
	return min(retryablehttp.DefaultBackoff(minWait, maxWait, attempt, resp), maxWait)
}

func newRetryClient() *retryablehttp.Client {
	client := retryablehttp.NewClient()
	client.Backoff = cappedBackoff
	return client
}

func getImageBytes(link string) ([]byte, error) {
	retryClient := newRetryClient()
	retryClient.Logger = nil
	resp, err := retryClient.Get(link)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	return io.ReadAll(resp.Body)
}

func extractNumber(fields []string, minLen int) string {
	for _, field := range fields {
		// Finding any of these characters means it's over
		switch field {
		case "™", "©":
			return ""
		}
		if len(field) > minLen {
			_, err := strconv.Atoi(field)
			if err == nil {
				return field
			}
		}
	}
	return ""
}

func getNumberFromLink(link string) (string, error) {
	client := gosseract.NewClient()
	defer client.Close()

	// We only want to find numbers and special terminator characters
	err := client.SetWhitelist("0123456789 ™ ©")
	if err != nil {
		return "", err
	}

	data, err := getImageBytes(link)
	if err != nil {
		return "", err
	}

	err = client.SetImageFromBytes(data)
	if err != nil {
		return "", err
	}

	text, err := client.Text()
	if err != nil {
		return "", err
	}

	fields := strings.Fields(text)
	num := extractNumber(fields, 3)
	if num == "" {
		num = extractNumber(fields, 2)
	}

	return num, nil
}

type CardSet struct {
	Title    string
	Filename string
	Cards    []CardData
}

type CardData struct {
	Name   string
	Number string
	Foil   bool
	Etched bool
	Token  bool
	Count  int
}

// Which finish/type tags cleanLine actually stripped as real variant
// markers, as opposed to a tag word that is part of the card's own name
// (see cardNames)
type detectedTags struct {
	Foil   bool
	Etched bool
	Token  bool
}

// Random prefixes to remove from card names
var nameTags = []string{
	"Full-Text", "Full-Art", "Full-art", "Alt-Art",
	"Reversible", "Old Frame", "Retro Frame",
	"Poster", "Stained Glass",
	"Foil-etched", "Etched", "Foil", "Tokens", "Token",
	"Different", "Hand-Drawn", "Borderless",
	"Showcase", "Left-Handed", "Edition", "cards", "Japanese",
	"Regular Human Guy", "Ichor-E", "DFC", "Italian-language", "*",
	"REVERSIBLE",
}

// Match tags on word boundaries only, so that eg "Edition" does not eat into
// "Expedition", covering both the original and the lowercase form of each tag
var nameTagRegexps = func() []*regexp.Regexp {
	isWordChar := func(c byte) bool {
		return c == '_' || ('0' <= c && c <= '9') ||
			('a' <= c && c <= 'z') || ('A' <= c && c <= 'Z')
	}
	regexps := make([]*regexp.Regexp, 0, len(nameTags))
	for _, tag := range nameTags {
		pattern := regexp.QuoteMeta(tag)
		if lower := strings.ToLower(tag); lower != tag {
			pattern = "(?:" + pattern + "|" + regexp.QuoteMeta(lower) + ")"
		}
		if isWordChar(tag[0]) {
			pattern = `\b` + pattern
		}
		if isWordChar(tag[len(tag)-1]) {
			pattern += `\b`
		}
		regexps = append(regexps, regexp.MustCompile(pattern))
	}
	return regexps
}()

// The cuts cleanLine makes besides nameTags
var (
	parenRegexp     = regexp.MustCompile(`\(`)
	foilRegexp      = regexp.MustCompile(`Foil`)
	phyrexianRegexp = regexp.MustCompile(`Phyrexian`)
	flavorRegexp    = regexp.MustCompile(` as `)
	withArtRegexp   = regexp.MustCompile(` with art`)
	slashesRegexp   = regexp.MustCompile(`//`)
)

// Every real card name, from Scryfall, so that cleanLine can tell a variant
// marker or flavor name apart from the same text inside a card's own name
type cardNames struct {
	names map[string]bool
	// For each cut, the names it would break
	containing map[*regexp.Regexp][]string
}

func newCardNames(list []string) *cardNames {
	cuts := append(slices.Clone(nameTagRegexps),
		parenRegexp, foilRegexp, phyrexianRegexp, flavorRegexp, withArtRegexp, slashesRegexp)

	n := &cardNames{
		names:      make(map[string]bool, len(list)),
		containing: make(map[*regexp.Regexp][]string),
	}
	for _, name := range list {
		n.names[name] = true
		// Product pages may list only the front face
		if front, _, found := strings.Cut(name, " // "); found {
			n.names[front] = true
		}
		for _, re := range cuts {
			// A name made only of the cut, like the card Foil, protects nothing
			if match := re.FindString(name); match != "" && match != name {
				n.containing[re] = append(n.containing[re], name)
			}
		}
	}
	return n
}

// Report whether text is exactly a real card name, or the front face of one
func (n *cardNames) is(text string) bool {
	return n != nil && n.names[strings.TrimSpace(text)]
}

// Return the [start, end) of every match of re in text, except those inside
// a real card name that text contains
func (n *cardNames) matches(text string, re *regexp.Regexp) [][]int {
	all := re.FindAllStringIndex(text, -1)
	if n == nil || len(all) == 0 {
		return all
	}
	var free [][]int
	for _, m := range all {
		if !n.protected(text, re, m) {
			free = append(free, m)
		}
	}
	return free
}

func (n *cardNames) protected(text string, re *regexp.Regexp, m []int) bool {
	for _, name := range n.containing[re] {
		for i := 0; i < len(text); {
			j := strings.Index(text[i:], name)
			if j < 0 {
				break
			}
			start := i + j
			if start <= m[0] && m[1] <= start+len(name) {
				return true
			}
			i = start + 1
		}
	}
	return false
}

// Replace the given [start, end) spans of text
func replaceMatches(text string, matches [][]int, replacement string) string {
	var b strings.Builder
	last := 0
	for _, m := range matches {
		b.WriteString(text[last:m[0]])
		b.WriteString(replacement)
		last = m[1]
	}
	b.WriteString(text[last:])
	return b.String()
}

func frontFace(name string) string {
	return strings.TrimSpace(strings.Split(name, " // ")[0])
}

// Turn any unicode white space into a plain one, in its own pass: the
// replacers below are single-pass and never rescan their own output, so eg
// "Secret Lair x " could never match a title where a non-breaking space had
// just been replaced
func normalizeSpaces(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) {
			return ' '
		}
		return r
	}, s)
}

// Derive the card name, removing any special tag, and report which
// finish/type tags were actually found along the way. No cut removes text
// inside a real card name the line contains, eg "Phyrexian Altar" or
// "Isshin, Two Heavens as One"; names may be nil, which protects nothing
func cleanLine(cardLine string, names *cardNames) (string, int, detectedTags, error) {
	var tags detectedTags

	// Unicode characters
	cardLine = normalizeSpaces(cardLine)
	cardLine = strings.ReplaceAll(cardLine, "’", "'")
	cardLine = strings.ReplaceAll(cardLine, "”", "\"")
	cardLine = strings.ReplaceAll(cardLine, "“", "\"")
	cardLine = strings.TrimSpace(cardLine)

	// Only split on the first separator, card names may contain "x " too
	fields := strings.SplitN(cardLine, "x ", 2)
	if len(fields) != 2 {
		return "", 0, tags, errors.New("unexpected line format")
	}

	num, err := strconv.Atoi(fields[0])
	if err != nil {
		return "", 0, tags, errors.New("invalid number in line")
	}
	cardLine = strings.TrimSpace(fields[1])

	// Remove anything appearing after a parenthesis
	if m := names.matches(cardLine, parenRegexp); len(m) > 0 {
		cardLine = cardLine[:m[0][0]]
	}
	if names.is(cardLine) {
		return frontFace(cardLine), num, tags, nil
	}

	// Remove everything before "Foil" to catch variants like Galaxy Textured etc,
	// as long as they are before the card name and not a tag after it
	if m := names.matches(cardLine, foilRegexp); len(m) > 0 &&
		!strings.HasSuffix(cardLine, "Foil Edition") && !strings.HasSuffix(cardLine, "Foil Etched") {
		end := len(cardLine)
		if len(m) > 1 {
			end = m[1][0]
		}
		cardLine = cardLine[m[0][1]:end]
		tags.Foil = true
		if names.is(cardLine) {
			return frontFace(cardLine), num, tags, nil
		}
	}

	// Remove the Phyrexian language tag
	cardLine = replaceMatches(cardLine, names.matches(cardLine, phyrexianRegexp), "")

	// Remove random prefixes from card names
	for i, tag := range nameTags {
		m := names.matches(cardLine, nameTagRegexps[i])
		if len(m) == 0 {
			continue
		}
		cardLine = replaceMatches(cardLine, m, "")
		switch tag {
		case "Foil":
			tags.Foil = true
		case "Etched":
			tags.Etched = true
		case "Foil-etched":
			tags.Foil = true
			tags.Etched = true
		case "Token", "Tokens":
			tags.Token = true
		}
	}

	// Remove flavor names
	if m := names.matches(cardLine, flavorRegexp); len(m) > 0 {
		cardLine = cardLine[:m[0][0]]
	}

	// Bob Ross Drop
	if m := names.matches(cardLine, withArtRegexp); len(m) > 0 {
		cardLine = cardLine[:m[0][0]]
	}

	// Standardize DFC
	if !strings.Contains(cardLine, " // ") {
		cardLine = replaceMatches(cardLine, names.matches(cardLine, slashesRegexp), " // ")
	}
	if strings.Contains(cardLine, " / ") && !strings.Contains(cardLine, " // ") {
		cardLine = strings.ReplaceAll(cardLine, " / ", " // ")
	}

	// Only keep one face of the card
	cardLine = strings.Split(cardLine, " // ")[0]

	// Use upstream sheet name
	cardLine = strings.ReplaceAll(cardLine, "Sticker Sheets", "Sticker sheet")

	// Typo
	cardLine = strings.ReplaceAll(cardLine, "Xenegos", "Xenagos")
	cardLine = strings.ReplaceAll(cardLine, "Death Render", "Deathrender")
	cardLine = strings.ReplaceAll(cardLine, "All is Dust", "All Is Dust")
	cardLine = strings.ReplaceAll(cardLine, "Mistep", "Misstep")
	cardLine = strings.ReplaceAll(cardLine, "Triumph of Hordes", "Triumph of the Hordes")

	return strings.TrimSpace(cardLine), num, tags, nil
}

var replacerStrings = []string{
	// Unicode characters
	"’", "'",
	"‘", "'",
	"®", "",
	"™", "",
	// Non-visible unicode white spaces
	"\uFEFF", "",
	"\u200B", "",
	// Windows special characters
	"<", "",
	">", "",
	"/", "",
	"\\", "",
	"*", "",
	" - ", " ",
	// Compatibility layer
	" Is in ", " is in ",
	" is In ", " is in ",
	"Regular", "",
	"DD ", "",
	"Secret Lair x ", "",
	"(English)", "",
	"English", "",
	" EN", "",
	// Spaces (need to be at the end to capture as much as possible)
	"   ", " ",
	"  ", " ",
}

var replacer = strings.NewReplacer(replacerStrings...)

// Generate two strings representing the deck name
// The first output is a compatible, file-system safe string to be used as a filename
// The second output is the upstream name of the deck with as few modifications as possible
func cleanTitle(title string) (string, string) {
	title = normalizeSpaces(title)

	// Keep only the relevant portion of the name, ie we can strip "Extra Life"
	// but not Avatar, when the name is separated by a pipe
	if strings.Contains(title, "|") {
		ogTitle := title
		title = strings.Replace(title, " |", ":", 1)

		if strings.Contains(ogTitle, "Extra Life") {
			title = strings.Split(ogTitle, " | ")[0]
			if strings.HasSuffix(ogTitle, "Foil Edition") {
				title += " Foil Edition"
			}
		}
	}

	// Remove pricing from the title
	title = strings.Split(title, " $")[0]

	// Clean up!
	title = replacer.Replace(title)

	// "Secret Lair High" needs to stay
	if !strings.Contains(title, "High") {
		title = strings.Replace(title, "Secret Lair ", "", 1)
	}

	// Fallout has too many dots and makes searching for it harder
	title = strings.Replace(title, "S.P.E.C.I.A.L.", "SPECIAL", 1)

	// Foil
	if strings.HasSuffix(title, "Foil") {
		title += " Edition"
	}

	originalName := strings.TrimSpace(title)

	title = strings.ReplaceAll(title, ":", "-")
	filename := strings.TrimSpace(title)

	return filename, originalName
}

// In case of error, the input cards is returned as is, so this fuction can be
// reused in a loop multiple times
func processLine(cards []CardData, line string, names *cardNames) ([]CardData, error) {
	var card CardData

	if line == "" {
		return cards, nil
	}

	cardLine, num, tags, err := cleanLine(line, names)
	if err != nil {
		return cards, err
	}

	card.Foil = tags.Foil
	card.Etched = tags.Etched
	card.Token = tags.Token
	card.Name = cardLine
	card.Count = num

	if strings.Contains(line, "Different") {
		for i := 0; i < num; i++ {
			card.Count = 1
			cards = append(cards, card)
			log.Printf("1x %s", card.Name)
		}
	} else {
		// Check if the card was already inserted, if so increase count, else just add it
		idx := -1
		for i := range cards {
			if cards[i].Name == card.Name && cards[i].Foil == card.Foil && cards[i].Etched == card.Etched {
				idx = i
				break
			}
		}
		if idx != -1 {
			cards[idx].Count += num
			log.Printf("0x %s (increased previous count)", card.Name)
		} else {
			cards = append(cards, card)
			log.Printf("%dx %s", card.Count, card.Name)
		}
	}

	return cards, nil
}

// Sort cards by collector number, stably: every card is still unnumbered at
// this point when no edition match was found, so every comparison is equal,
// and the OCR pass right after this call assumes cards[i] still lines up
// with the i-th gallery image - an unstable sort is free to reorder equal
// elements and would silently break that assumption
func sortCardsByNumber(cards []CardData) {
	sort.SliceStable(cards, func(i, j int) bool {
		a, b := collectorNumberValue(cards[i].Number), collectorNumberValue(cards[j].Number)
		if a != b {
			return a < b
		}
		return cards[i].Number < cards[j].Number
	})
}

// Compare collector numbers by their numeric value, so that eg 689 sorts
// before 1005, keeping any unnumbered card at the front
func collectorNumberValue(number string) int {
	i := 0
	for i < len(number) && number[i] >= '0' && number[i] <= '9' {
		i++
	}
	value, err := strconv.Atoi(number[:i])
	if err != nil {
		return 0
	}
	return value
}

// Compare card names ignoring case and punctuation, so that eg a spurious
// comma in "Dosan, the Falling Leaf" still matches the Scryfall name
func normalizeCardName(name string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			return unicode.ToLower(r)
		}
		return -1
	}, name)
}

// Retrieve the canonical Scryfall spelling of a name from the results, as
// long as it only differs in case or punctuation from the scraped one
func canonicalName(results []CardData, name string) string {
	for _, result := range results {
		if result.Name != name && normalizeCardName(result.Name) == normalizeCardName(name) {
			log.Printf("Adopting Scryfall spelling '%s' over '%s'", result.Name, name)
			return result.Name
		}
	}
	return name
}

// When Scryfall's edition search returns a different card count than what
// was scraped, the scraped list is replaced wholesale by the search
// results (see scrapeProduct) - but each replacement card still needs a
// Foil/Etched finish. Try to inherit it from the scraped card with the
// same name, so a mixed-finish product doesn't get every card's finish
// flattened to whatever the first scraped card happened to be; only fall
// back to that default when no scraped card matches at all.
func inheritFinish(scraped []CardData, results []CardData) []CardData {
	for i := range results {
		var foil, etched bool
		if len(scraped) > 0 {
			foil, etched = scraped[0].Foil, scraped[0].Etched
		}
		for _, orig := range scraped {
			if normalizeCardName(orig.Name) == normalizeCardName(results[i].Name) {
				foil, etched = orig.Foil, orig.Etched
				break
			}
		}
		results[i].Foil = foil
		results[i].Etched = etched
		results[i].Count = 1
	}
	return results
}

// Assign the collector numbers found on Scryfall to the scraped cards,
// preferring exact name matches so that similar names cannot steal each
// other's slot, then retrying while ignoring case and punctuation, in which
// case the Scryfall spelling of the name is adopted too. Finally, if the
// two rounds above leave exactly one card and exactly one result
// unmatched, pair them by elimination and adopt Scryfall's name outright,
// even though it may differ from the scraped one by more than punctuation
// - this is what recovers from a genuine source-page typo (eg "Kutzil,
// Malament Exemplar" scraped for the real "Kutzil, Malamet Exemplar"),
// which normalizeCardName alone cannot bridge. It is safe specifically
// because cards and results always start out the same length (the only
// caller only reaches this function when that holds): every match removes
// one entry from each side, so if exactly one remains on one side, exactly
// one remains on the other too, and there is no other candidate either
// one of them could be.
func matchCardNumbers(cards, results []CardData) {
	for i := range cards {
		for j := range results {
			if results[j].Number != "" && cards[i].Name == results[j].Name {
				cards[i].Number = results[j].Number

				// Reset so we can skip on reuse
				results[j].Number = ""
				break
			}
		}
	}

	for i := range cards {
		if cards[i].Number != "" {
			continue
		}
		for j := range results {
			if results[j].Number != "" && normalizeCardName(cards[i].Name) == normalizeCardName(results[j].Name) {
				log.Printf("Adopting Scryfall spelling '%s' over '%s'", results[j].Name, cards[i].Name)
				cards[i].Name = results[j].Name
				cards[i].Number = results[j].Number

				// Reset so we can skip on reuse
				results[j].Number = ""
				break
			}
		}
	}

	var unmatchedCards []int
	for i := range cards {
		if cards[i].Number == "" {
			unmatchedCards = append(unmatchedCards, i)
		}
	}
	var unmatchedResults []int
	for j := range results {
		if results[j].Number != "" {
			unmatchedResults = append(unmatchedResults, j)
		}
	}
	if len(unmatchedCards) == 1 && len(unmatchedResults) == 1 {
		i, j := unmatchedCards[0], unmatchedResults[0]
		log.Printf("By elimination, adopting Scryfall spelling '%s' over '%s'", results[j].Name, cards[i].Name)
		cards[i].Name = results[j].Name
		cards[i].Number = results[j].Number
	}
}

func scrapeProduct(ctx context.Context, headers []scryfallHeader, names *cardNames, link string, doOCR bool) (*CardSet, error) {
	resp, err := newRetryClient().Get(link)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}
	var cardSet CardSet

	title := doc.Find(`h1[class="product-title"]`).Text()
	cardSet.Filename, cardSet.Title = cleanTitle(title)

	log.Println(cardSet.Title)

	var cards []CardData
	doc.Find(`div[class="force-overflow"] ul li`).Each(func(_ int, s *goquery.Selection) {
		line := s.Text()
		cards, err = processLine(cards, line, names)
		if err != nil {
			log.Printf("%s - %s", line, err.Error())
		}
	})

	if len(cards) == 0 {
		// Fallback if there were no bullet points
		productInfo, _ := doc.Find(`div[id="collapse2"] div[class="force-overflow"] p[class="product-information"]`).Html()
		for _, line := range strings.Split(productInfo, "<br/>") {
			cards, err = processLine(cards, line, names)
			if err != nil {
				log.Printf("%s - %s", line, err.Error())
			}
		}
	}

	if len(cards) == 0 {
		return nil, errors.New("no cards found")
	}

	foundMatch := false
	cleanTitle := cardSet.Title
	cleanTitle = strings.ReplaceAll(cleanTitle, " Foil Edition", "")
	cleanTitle = strings.ReplaceAll(cleanTitle, " Raised", "")
	cleanTitle = strings.ReplaceAll(cleanTitle, " Galaxy", "")

	for _, header := range headers {
		a := strings.ToLower(cleanTitle)
		b := strings.ToLower(header.Title)
		if !fuzzy.Match(a, b) && !strings.Contains(a, b) && !strings.Contains(b, a) {
			continue
		}

		results, err := searchURI(ctx, header.URI)
		if err != nil {
			log.Println(err.Error())
			continue
		}
		if len(results) == 0 {
			log.Println("empty result set from Scryfall, ignoring")
			continue
		}

		log.Printf("Found these possible card numbers: %+v", results)
		if len(results) != len(cards) {
			log.Println("... but the contents differ, we trust Scryfall...")
			cards = inheritFinish(cards, results)
		} else {
			matchCardNumbers(cards, results)
		}
		foundMatch = true
		break
	}
	if !foundMatch {
		log.Println(cleanTitle, "was not found, will try OCR")
		doOCR = true
	}

	sortCardsByNumber(cards)

	cardSet.Cards = cards

	if doOCR {
		// Sometimes pages have twice as many images because they are front and back,
		// but we're interested in only the front to grab the number, so set a flag
		// that makes the later chunk skip duplicated images
		foldMode := false
		galleryTitle := doc.Find(`h2[class="pdp_title"]`).Text()
		if strings.Contains(galleryTitle, " (") {
			fields := strings.Fields(galleryTitle)
			expectedNum := fields[len(fields)-1]
			expectedNum = strings.TrimLeft(expectedNum, "(")
			expectedNum = strings.TrimRight(expectedNum, ")")
			expectedNumber, _ := strconv.Atoi(expectedNum)
			if expectedNumber/2 == len(cards) {
				foldMode = true
			}
		}

		// Find numbers by pulling images and OCR numbers out
		doc.Find(`figure a`).EachWithBreak(func(i int, s *goquery.Selection) bool {
			if foldMode {
				i = i / 2
			}
			if i >= len(cards) {
				log.Println("Found more images than loaded cards, something may be off")
				return false
			}

			if cards[i].Number != "" {
				return true
			}

			imgLink, found := s.Attr("href")
			if !found {
				return true
			}
			if strings.HasPrefix(imgLink, "/") {
				imgLink = "https://secretlair.wizards.com" + imgLink
			}

			num, err := getNumberFromLink(imgLink)
			if err != nil {
				log.Println(imgLink, err)
				return true
			}

			res, err := search(ctx, fmt.Sprintf("%s cn:%s", cards[i].Name, num))
			if err != nil || len(res) == 0 {
				log.Println("validation failed:", err)
				return true
			}

			cards[i].Name = canonicalName(res, cards[i].Name)
			cards[i].Number = num
			return true
		})
	}

	// Validate numbers and backfill if needed
	foundNum := 0
	for _, card := range cards {
		if card.Number != "" {
			foundNum++
		}
	}
	if foundNum != len(cards) {
		log.Println("Couldn't parse all images, trying to backfill...")

		// Find the longest number among those founds and the position
		num := ""
		pos := -1
		for i, card := range cards {
			if card.Number != "" {
				if len(card.Number) > len(num) {
					num = card.Number
					pos = i
				}
			}
		}

		// If we found something derive the number for the others
		if num != "" {
			cn, _ := strconv.Atoi(strings.TrimLeft(num, "0"))
			if cn > 0 {
				for j := range cards {
					if cards[j].Number != "" {
						continue
					}
					num = fmt.Sprint(cn + j - pos)

					res, err := search(ctx, fmt.Sprintf("%s cn:%s", cards[j].Name, num))
					if err != nil || len(res) == 0 {
						log.Println("validation failed:", err)
						continue
					}
					cards[j].Name = canonicalName(res, cards[j].Name)
					cards[j].Number = num
				}
			}
		} else {
			log.Println("...worth a shot")
		}
	}

	return &cardSet, nil
}

// Render a decklist in the upstream text format
func formatCards(cardSet *CardSet, link, releaseDate string) string {
	var b strings.Builder
	fmt.Fprintf(&b, "// NAME: %s\n", cardSet.Title)
	fmt.Fprintf(&b, "// SOURCE: %s\n", link)
	if releaseDate != "" {
		fmt.Fprintf(&b, "// DATE: %s\n", releaseDate)
	}
	for _, card := range cardSet.Cards {
		number := ""
		if card.Number != "" {
			number = ":" + card.Number
		}
		fmt.Fprintf(&b, "%d [SLD%s] %s", card.Count, number, card.Name)
		if card.Foil {
			b.WriteString(" [foil]")
		}
		if card.Etched {
			b.WriteString(" [etched]")
		}
		if card.Token {
			b.WriteString(" [token]")
		}
		b.WriteString("\n")
	}
	return b.String()
}

func dumpCards(cardSet *CardSet, link, releaseDate, filename string) error {
	text := formatCards(cardSet, link, releaseDate)
	if filename == "" {
		_, err := io.WriteString(os.Stdout, text)
		return err
	}

	filename += ".txt"
	file, err := os.Create(filename)
	if err != nil {
		return err
	}
	_, err = io.WriteString(file, text)
	if closeErr := file.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		// Never leave a truncated decklist behind for the sync to commit
		os.Remove(filename)
		return err
	}

	log.Printf("Created '%s' (%s)", filename, releaseDate)
	return nil
}

func run() int {
	pageOpt := flag.Int("page", -1, "Which page to start from (0 for the very beginning)")
	doOCROpt := flag.Bool("ocr", false, "Enable OCR to derive collector numbers")
	flag.Parse()

	ctx := context.Background()

	headers, err := loadScryfallHeaders(ctx)
	if err != nil {
		log.Println("Unable to query scryfall")
		return 1
	}
	log.Println("Parsed Scryfall set page,", len(headers), "products found")

	names, err := loadCardNames(ctx)
	if err != nil {
		log.Println("Unable to load the Scryfall card names:", err)
		return 1
	}
	log.Println("Loaded", len(names.names), "Scryfall card names and front faces")

	if args := flag.Args(); len(args) > 0 {
		exitCode := 0
		for i, arg := range args {
			cardSet, err := scrapeProduct(ctx, headers, names, arg, *doOCROpt)
			if err != nil {
				log.Println("page", i, "-", err)
				exitCode = 1
				continue
			}

			err = dumpCards(cardSet, arg, "", "")
			if err != nil {
				log.Println(err)
				exitCode = 1
			}
		}
		return exitCode
	}

	if *pageOpt < 0 {
		log.Println("Missing starting -page argument")
		return 1
	}

	i := *pageOpt
	for {
		resp, err := getProducts(i * maxItemsInResp)
		if err != nil {
			log.Println(err)
			break
		}
		i++

		if len(resp.Products) == 0 {
			break
		}

		for _, product := range resp.Products {
			releaseDate := product.ReleaseDate.Format("2006-01-02")

			shouldSkip := false
			for _, desc := range product.Descriptions {
				// Skip any bundle and special releases
				if strings.Contains(desc.Title, "Bundle") ||
					strings.Contains(desc.Title, "BUNDLE") ||
					strings.Contains(desc.Title, "Festival in a Box") ||
					strings.Contains(desc.Title, "Transformers TCG") ||
					strings.Contains(desc.Title, "DRAGON’S ENDGAME") ||
					(strings.Contains(desc.Title, "Secret Lair") && strings.Contains(desc.Title, "Deck")) ||
					strings.Contains(desc.Title, "They're Just Like Us but") ||
					strings.Contains(desc.Title, "Heads I Win, Tails") ||
					strings.Contains(desc.Title, "Deluxe Collection") ||
					strings.Contains(desc.Title, "Heroes of the Borderlands") ||
					strings.Contains(desc.Title, "Welcome to the Hellfire Club") ||
					strings.Contains(desc.Title, "D&D Sapphire Anniversary") ||
					strings.Contains(desc.Title, "Fan Merch") ||
					strings.Contains(desc.Title, "30th Anniversary Edition") ||
					strings.Contains(desc.Title, "Japanese") ||
					strings.Contains(desc.Title, " JP") ||
					strings.Contains(desc.Title, " SP") ||
					strings.Contains(desc.Title, "Countdown Kit") ||
					strings.Contains(desc.Title, "The Zeta Set") {
					shouldSkip = true
					fmt.Printf("\"%s\",%s\n", desc.Title, releaseDate)
					break
				}
			}
			if shouldSkip {
				continue
			}

			link := "https://secretlair.wizards.com/us/product/" + product.ProductID
			cardSet, err := scrapeProduct(ctx, headers, names, link, *doOCROpt)
			if err != nil {
				log.Println("page", i-1, "-", err)
				continue
			}

			err = dumpCards(cardSet, link, releaseDate, cardSet.Filename)
			if err != nil {
				log.Println(err)
				continue
			}
		}
	}

	fmt.Fprintln(os.Stdout, "In the future you can start from page", i-2)

	return 0
}

func main() {
	os.Exit(run())
}

const (
	maxItemsInResp = 50
	scalefastURL   = "https://storesearch.eu.scalefast.com/StoreSearch?userID=10751401&locale=en_US&currency=USD&crit=ALL&sort=release_date&count=50&env=prod&offset="
)

type ScalefastResponse struct {
	Count    int `json:"count"`
	Total    int `json:"total"`
	Products []struct {
		ProductID    string    `json:"productID"`
		ReleaseDate  time.Time `json:"release_date"`
		Descriptions []struct {
			Lang  string `json:"lang"`
			Title string `json:"title"`
		} `json:"descriptions"`
	} `json:"products"`
}

func getProducts(offset int) (*ScalefastResponse, error) {
	retryClient := newRetryClient()
	retryClient.Logger = nil

	resp, err := retryClient.Get(scalefastURL + fmt.Sprint(offset))
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	var response ScalefastResponse
	err = json.Unmarshal(data, &response)
	if err != nil {
		return nil, err
	}

	return &response, nil
}
