package main

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"image/draw"
	"image/png"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"runtime/debug"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/BlueMonday/go-scryfall"
	"github.com/PuerkitoBio/goquery"
	"github.com/hashicorp/go-retryablehttp"
	"go.uber.org/ratelimit"
)

// A few real card names, standing in for Scryfall's catalog
var testNames = newCardNames([]string{
	"Nyx Lotus", "Expedition Map", "Brainstorm", "Sol Ring", "Foil",
	"Etched Champion", "Etched Host Doombringer", "Lavinia, Foil to Conspiracy",
	"Phyrexian Tower", "Phyrexian Altar", "Vorinclex, Voice of Hunger",
	"Growing Rites of Itlimoc // Itlimoc, Cradle of the Sun", "Triumph of the Hordes",
	"Isshin, Two Heavens as One", "Fight as One", "B.F.M. (Big Furry Monster)",
	"SP//dr, Piloted by Peni", "Finally! Left-Handed Magic Cards",
})

func TestCleanLine(t *testing.T) {
	tests := []struct {
		line  string
		name  string
		count int
		tags  detectedTags
	}{
		// Card names containing "x " survive the count separator split
		{"1x Nyx Lotus", "Nyx Lotus", 1, detectedTags{}},
		// Tags are only removed on word boundaries
		{"2x Borderless Expedition Map", "Expedition Map", 2, detectedTags{}},
		// Card names containing a tag word are preserved, and are not
		// reported as a detected finish tag either
		{"1x Foil Etched Champion", "Etched Champion", 1, detectedTags{Foil: true}},
		{"1x Lavinia, Foil to Conspiracy", "Lavinia, Foil to Conspiracy", 1, detectedTags{}},
		// Regular tag stripping still works, and is reported
		{"3x Showcase Brainstorm (Retro Frame)", "Brainstorm", 3, detectedTags{}},
		{"1x Galaxy Foil Sol Ring", "Sol Ring", 1, detectedTags{Foil: true}},
		{"1x Foil Etched Sol Ring", "Sol Ring", 1, detectedTags{Foil: true, Etched: true}},
		{"4x Borderless Werewolf token", "Werewolf", 4, detectedTags{Token: true}},
		{"1x Phyrexian Tower", "Phyrexian Tower", 1, detectedTags{}},
		{"1x Phyrexian Vorinclex, Voice of Hunger", "Vorinclex, Voice of Hunger", 1, detectedTags{}},
		{"1x Growing Rites of Itlimoc // Itlimoc, Cradle of the Sun", "Growing Rites of Itlimoc", 1, detectedTags{}},
		{"1x Triumph of Hordes", "Triumph of the Hordes", 1, detectedTags{}},
		// Non-breaking spaces are normalized away
		{"1x\u00a0Sol Ring", "Sol Ring", 1, detectedTags{}},
		// No cut reaches into a real card name: Phyrexian, " as ",
		// parentheses, slashes and tag words that are part of the name stay
		{"1x Phyrexian Altar", "Phyrexian Altar", 1, detectedTags{}},
		{"1x Showcase Phyrexian Altar (Retro Frame)", "Phyrexian Altar", 1, detectedTags{}},
		{"1x Isshin, Two Heavens as One", "Isshin, Two Heavens as One", 1, detectedTags{}},
		{"1x Fight as One", "Fight as One", 1, detectedTags{}},
		{"1x B.F.M. (Big Furry Monster)", "B.F.M. (Big Furry Monster)", 1, detectedTags{}},
		{"1x SP//dr, Piloted by Peni", "SP//dr, Piloted by Peni", 1, detectedTags{}},
		{"1x Finally! Left-Handed Magic Cards", "Finally! Left-Handed Magic Cards", 1, detectedTags{}},
		{"1x Borderless Etched Champion", "Etched Champion", 1, detectedTags{}},
		{"1x Etched Host Doombringer", "Etched Host Doombringer", 1, detectedTags{}},
		{"1x Foil", "Foil", 1, detectedTags{}},
		// ...while the same text outside the name is still cut
		{"1x Foil Lavinia, Foil to Conspiracy", "Lavinia, Foil to Conspiracy", 1, detectedTags{Foil: true}},
		{"1x Sol Ring as The One Ring", "Sol Ring", 1, detectedTags{}},
		{"1x Etched Sol Ring", "Sol Ring", 1, detectedTags{Etched: true}},
	}

	for _, tt := range tests {
		name, count, tags, err := cleanLine(tt.line, testNames)
		if err != nil {
			t.Errorf("cleanLine(%q) returned error: %v", tt.line, err)
			continue
		}
		if name != tt.name || count != tt.count {
			t.Errorf("cleanLine(%q) = %q, %d - expected %q, %d", tt.line, name, count, tt.name, tt.count)
		}
		if tags != tt.tags {
			t.Errorf("cleanLine(%q) tags = %+v - expected %+v", tt.line, tags, tt.tags)
		}
	}
}

func TestCleanLineErrors(t *testing.T) {
	for _, line := range []string{
		"no count here",
		"Includes the following",
	} {
		_, _, _, err := cleanLine(line, testNames)
		if err == nil {
			t.Errorf("cleanLine(%q) expected an error", line)
		}
	}
}

func TestProcessLineMerge(t *testing.T) {
	var cards []CardData
	var err error
	for _, line := range []string{
		"1x Sol Ring",
		"2x Sol Ring",
		"1x Foil Sol Ring",
	} {
		cards, err = processLine(cards, line, testNames)
		if err != nil {
			t.Fatalf("processLine(%q) returned error: %v", line, err)
		}
	}

	if len(cards) != 2 {
		t.Fatalf("expected 2 entries (nonfoil and foil), got %d: %+v", len(cards), cards)
	}
	if cards[0].Foil || cards[0].Count != 3 {
		t.Errorf("expected 3x nonfoil Sol Ring, got %+v", cards[0])
	}
	if !cards[1].Foil || cards[1].Count != 1 {
		t.Errorf("expected 1x foil Sol Ring, got %+v", cards[1])
	}
}

func TestProcessLineFinishDetection(t *testing.T) {
	tests := []struct {
		line   string
		foil   bool
		etched bool
	}{
		// A card whose own name contains "Foil" must not be flagged as a
		// foil-finish printing just because the raw line contains that
		// substring
		{"1x Lavinia, Foil to Conspiracy", false, false},
		// An actual foil-finish prefix is still detected
		{"1x Foil Sol Ring", true, false},
		// A card whose own name contains "Etched" must not be flagged as
		// an etched-finish printing
		{"1x Foil Etched Champion", true, false},
	}

	for _, tt := range tests {
		cards, err := processLine(nil, tt.line, testNames)
		if err != nil {
			t.Fatalf("processLine(%q) returned error: %v", tt.line, err)
		}
		if len(cards) != 1 {
			t.Fatalf("processLine(%q) produced %d cards, expected 1", tt.line, len(cards))
		}
		if cards[0].Foil != tt.foil || cards[0].Etched != tt.etched {
			t.Errorf("processLine(%q) = %+v - expected Foil=%v Etched=%v", tt.line, cards[0], tt.foil, tt.etched)
		}
	}
}

func TestSortCardsByNumber(t *testing.T) {
	cards := []CardData{
		{Name: "Alpha"},
		{Name: "Beta"},
		{Name: "Gamma", Number: "689"},
		{Name: "Delta", Number: "1005"},
		{Name: "Epsilon"},
	}
	sortCardsByNumber(cards)

	// Numbered cards sort numerically (689 before 1005, not lexically),
	// and unnumbered cards - all "equal" under this ordering - keep their
	// original relative order: the OCR pass right after this call in
	// scrapeProduct assumes cards[i] still lines up with the i-th gallery
	// image, which only holds for a stable sort
	want := []string{"Alpha", "Beta", "Epsilon", "Gamma", "Delta"}
	for i, name := range want {
		if cards[i].Name != name {
			t.Errorf("cards[%d].Name = %q, want %q", i, cards[i].Name, name)
		}
	}

	// A short or already ordered input comes out the same from an unstable
	// sort too; unnumbered cards interleaved with numbered ones in reverse
	// order is an input sort.Slice reorders, so this fails without
	// SliceStable
	cards = nil
	for i := range 10 {
		cards = append(cards,
			CardData{Name: fmt.Sprint("unnumbered ", i)},
			CardData{Name: fmt.Sprint("numbered ", i), Number: fmt.Sprint(900 - i)})
	}
	sortCardsByNumber(cards)
	for i := range 10 {
		if want := fmt.Sprint("unnumbered ", i); cards[i].Name != want {
			t.Errorf("cards[%d].Name = %q, want %q", i, cards[i].Name, want)
		}
		if want := fmt.Sprint("numbered ", 9-i); cards[10+i].Name != want {
			t.Errorf("cards[%d].Name = %q, want %q", 10+i, cards[10+i].Name, want)
		}
	}
}

func TestInheritFinish(t *testing.T) {
	scraped := []CardData{
		{Name: "Sol Ring", Foil: false},
		{Name: "Mox Diamond", Foil: true},
	}
	results := []CardData{
		{Name: "Sol Ring", Number: "100"},
		{Name: "Mox Diamond", Number: "101"},
		{Name: "Black Lotus", Number: "102"},
	}

	got := inheritFinish(scraped, results)

	if len(got) != 3 {
		t.Fatalf("expected 3 cards, got %d", len(got))
	}
	if got[0].Foil {
		t.Errorf("expected Sol Ring to keep its own (nonfoil) finish, got %+v", got[0])
	}
	if !got[1].Foil {
		t.Errorf("expected Mox Diamond to keep its own (foil) finish, got %+v", got[1])
	}
	// No scraped card named "Black Lotus" - falls back to the first
	// scraped card's finish (documented best-effort default)
	if got[2].Foil != scraped[0].Foil {
		t.Errorf("expected the fallback finish for an unmatched card, got %+v", got[2])
	}
	for _, card := range got {
		if card.Count != 1 {
			t.Errorf("expected every replacement card to have Count 1, got %+v", card)
		}
	}

	// The scraped name only matches ignoring punctuation, which is enough
	// to inherit its finish
	got = inheritFinish(
		[]CardData{{Name: "Sol Ring"}, {Name: "Dosan, the Falling Leaf", Foil: true}},
		[]CardData{{Name: "Dosan the Falling Leaf", Number: "2404"}})
	if !got[0].Foil {
		t.Errorf("expected the punctuation-only match to inherit its foil finish, got %+v", got[0])
	}

	// Nothing to replace, or nothing scraped to inherit from
	if got := inheritFinish(scraped, nil); len(got) != 0 {
		t.Errorf("expected no cards from no results, got %+v", got)
	}
	got = inheritFinish(nil, []CardData{{Name: "Sol Ring", Number: "100"}})
	if len(got) != 1 || got[0].Foil || got[0].Etched || got[0].Count != 1 {
		t.Errorf("expected a nonfoil single card with nothing scraped, got %+v", got)
	}
}

func TestCollectorNumberValue(t *testing.T) {
	tests := []struct {
		number string
		value  int
	}{
		{"", 0},
		{"689", 689},
		{"1005", 1005},
		{"119a", 119},
		// A foil-only star suffix, and a number that does not start with a
		// digit, which sorts with the unnumbered cards
		{"123★", 123},
		{"A1", 0},
	}

	for _, tt := range tests {
		if value := collectorNumberValue(tt.number); value != tt.value {
			t.Errorf("collectorNumberValue(%q) = %d - expected %d", tt.number, value, tt.value)
		}
	}
}

func TestNormalizeCardName(t *testing.T) {
	if normalizeCardName("Dosan, the Falling Leaf") != normalizeCardName("Dosan the Falling Leaf") {
		t.Errorf("expected names to match ignoring punctuation")
	}
	if normalizeCardName("Fog") == normalizeCardName("Fog Bank") {
		t.Errorf("different names should not match")
	}
}

func TestCanonicalName(t *testing.T) {
	results := []CardData{
		{Name: "Fog Bank", Number: "123"},
		{Name: "Dosan the Falling Leaf", Number: "2404"},
	}

	if name := canonicalName(results, "Dosan, the Falling Leaf"); name != "Dosan the Falling Leaf" {
		t.Errorf("expected the Scryfall spelling, got %q", name)
	}
	// A name close to a result but not equivalent must be left alone
	if name := canonicalName(results, "Fog"); name != "Fog" {
		t.Errorf("expected the name to be untouched, got %q", name)
	}
}

func TestMatchCardNumbers(t *testing.T) {
	cards := []CardData{
		{Name: "Dosan, the Falling Leaf"},
		{Name: "Azusa, Lost but Seeking"},
		// Two simultaneous mismatches: with more than one card and more
		// than one result left after the first two rounds, pairing them
		// up would be a guess, not an elimination - neither should match
		{Name: "Fog"},
		{Name: "Frost Breath"},
	}
	results := []CardData{
		{Name: "Azusa, Lost but Seeking", Number: "2403"},
		{Name: "Dosan the Falling Leaf", Number: "2404"},
		{Name: "Fog Bank", Number: "2405"},
		{Name: "Winter's Grasp", Number: "2406"},
	}

	matchCardNumbers(cards, results)

	if cards[0].Name != "Dosan the Falling Leaf" || cards[0].Number != "2404" {
		t.Errorf("expected the Scryfall spelling and number to be adopted, got %+v", cards[0])
	}
	if cards[1].Name != "Azusa, Lost but Seeking" || cards[1].Number != "2403" {
		t.Errorf("expected an exact match, got %+v", cards[1])
	}
	if cards[2].Name != "Fog" || cards[2].Number != "" {
		t.Errorf("expected no match while more than one card is ambiguous, got %+v", cards[2])
	}
	if cards[3].Name != "Frost Breath" || cards[3].Number != "" {
		t.Errorf("expected no match while more than one card is ambiguous, got %+v", cards[3])
	}
}

func TestMatchCardNumbersByElimination(t *testing.T) {
	// Reproduces the real production case: the Wizards product page
	// misspelled "Kutzil, Malamet Exemplar" as "Kutzil, Malament
	// Exemplar" - a genuine letter-level typo, not just punctuation or
	// case, so normalizeCardName cannot bridge it. Every other card in
	// the product matches cleanly, leaving exactly one card and exactly
	// one result unmatched on each side.
	cards := []CardData{
		{Name: "Sisay, Weatherlight Captain"},
		{Name: "Silence"},
		{Name: "Emiel the Blessed"},
		{Name: "Hajar, Loyal Bodyguard"},
		{Name: "Kutzil, Malament Exemplar"},
		{Name: "Sol Ring"},
	}
	results := []CardData{
		{Name: "Sisay, Weatherlight Captain", Number: "2778"},
		{Name: "Silence", Number: "2779"},
		{Name: "Emiel the Blessed", Number: "2780"},
		{Name: "Hajar, Loyal Bodyguard", Number: "2781"},
		{Name: "Kutzil, Malamet Exemplar", Number: "2782"},
		{Name: "Sol Ring", Number: "2783"},
	}

	matchCardNumbers(cards, results)

	if cards[4].Name != "Kutzil, Malamet Exemplar" || cards[4].Number != "2782" {
		t.Errorf("expected the sole leftover pair to be matched by elimination, got %+v", cards[4])
	}
	for i, card := range cards {
		if i == 4 {
			continue
		}
		if card.Number == "" {
			t.Errorf("expected every other card to already be matched, got %+v", card)
		}
	}
}

func TestExtractNumber(t *testing.T) {
	if num := extractNumber([]string{"0123", "™"}, 3); num != "0123" {
		t.Errorf("expected 0123, got %q", num)
	}
	if num := extractNumber([]string{"™", "456"}, 2); num != "" {
		t.Errorf("expected termination on ™, got %q", num)
	}
}

func TestCleanTitle(t *testing.T) {
	tests := []struct {
		title    string
		filename string
		name     string
	}{
		{"Secret Lair x Lofi Girl: Beats to Cast To", "Lofi Girl- Beats to Cast To", "Lofi Girl: Beats to Cast To"},
		// Non-breaking spaces used to hide the prefix from the replacer
		{"Secret\u00a0Lair x Lofi Girl: Beats to Cast To\u00a0Foil Edition", "Lofi Girl- Beats to Cast To Foil Edition", "Lofi Girl: Beats to Cast To Foil Edition"},
		// Narrow no-break space, previously handled by the replacer itself
		{"Secret\u202fLair x Cosmic Chill by Robin Eisenberg", "Cosmic Chill by Robin Eisenberg", "Cosmic Chill by Robin Eisenberg"},
	}

	for _, tt := range tests {
		filename, name := cleanTitle(tt.title)
		if filename != tt.filename || name != tt.name {
			t.Errorf("cleanTitle(%q) = %q, %q - expected %q, %q", tt.title, filename, name, tt.filename, tt.name)
		}
	}
}

func TestRetryClientCapsRetryAfter(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.Header().Set("Retry-After", "3600")
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client := newRetryClient()
	client.Logger = nil
	client.RetryWaitMax = 10 * time.Millisecond

	// The server asks for an hour, the retry must settle for RetryWaitMax
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	req, err := retryablehttp.NewRequestWithContext(ctx, http.MethodGet, server.URL, nil)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Do(req)
	if err != nil {
		t.Fatalf("expected the retry to happen within RetryWaitMax, got %v", err)
	}
	resp.Body.Close()

	if n := attempts.Load(); n != 2 {
		t.Errorf("expected 2 attempts, got %d", n)
	}
}

// An image with no digits must not come back as an empty number, which
// would be sent to Scryfall as "<name> cn:"
func TestGetNumberFromLinkBlankImage(t *testing.T) {
	img := image.NewGray(image.Rect(0, 0, 300, 100))
	draw.Draw(img, img.Bounds(), image.White, image.Point{}, draw.Src)
	var data bytes.Buffer
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "image/png")
		if _, err := w.Write(data.Bytes()); err != nil {
			t.Error(err)
		}
	}))
	defer server.Close()

	client, err := newOCRClient()
	if err != nil {
		t.Fatal(err)
	}
	defer client.Close()

	// One client reads every image of a product
	for range 2 {
		num, err := getNumberFromLink(context.Background(), client, server.URL)
		if err == nil {
			t.Errorf("expected an error for an image with no number, got %q", num)
		}
	}
}

func TestFormatCards(t *testing.T) {
	cardSet := &CardSet{
		Title: "Lofi Girl: Beats to Cast To",
		Cards: []CardData{
			{Name: "Felidar Guardian", Number: "2821", Count: 1},
			{Name: "Werewolf", Count: 4, Token: true},
			{Name: "Sol Ring", Number: "2822", Count: 2, Foil: true, Etched: true},
			{Name: "Witch Enchanter", Number: "2655a", Count: 1, Foil: true},
		},
	}
	want := `// NAME: Lofi Girl: Beats to Cast To
// SOURCE: https://secretlair.wizards.com/us/product/1254382
// DATE: 2026-09-01
1 [SLD:2821] Felidar Guardian
4 [SLD] Werewolf [token]
2 [SLD:2822] Sol Ring [foil] [etched]
1 [SLD:2655a] Witch Enchanter [foil]
`
	got := formatCards(cardSet, "https://secretlair.wizards.com/us/product/1254382", "2026-09-01")
	if got != want {
		t.Errorf("formatCards() =\n%s\nwant\n%s", got, want)
	}

	// No DATE line at all without a release date
	got = formatCards(&CardSet{Title: "X"}, "link", "")
	if want := "// NAME: X\n// SOURCE: link\n"; got != want {
		t.Errorf("formatCards() without a date = %q, want %q", got, want)
	}
}

func TestDumpCardsFile(t *testing.T) {
	t.Chdir(t.TempDir())
	cardSet := &CardSet{
		Title: "Lofi Girl: Beats to Cast To",
		Cards: []CardData{{Name: "Felidar Guardian", Number: "2821", Count: 1}},
	}

	if err := dumpCards(cardSet, "link", "2026-09-01", "Lofi Girl- Beats to Cast To"); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile("Lofi Girl- Beats to Cast To.txt")
	if err != nil {
		t.Fatal(err)
	}
	if want := formatCards(cardSet, "link", "2026-09-01"); string(data) != want {
		t.Errorf("file contents = %q, want %q", data, want)
	}
}

func TestCrawlReport(t *testing.T) {
	tests := []struct {
		name        string
		start       int
		lastPage    int   // -1: no page had products
		failedPages []int // in crawl order
		resumePage  int
	}{
		// Starts over from the last page with products
		{"complete", 20, 21, nil, 21},
		// A product that failed is retried by starting from its page
		{"product fails on an earlier page", 20, 21, []int{20}, 20},
		{"page 0 failed", 0, 3, []int{0, 2}, 0},
		// The page that could not be fetched is reached again from the
		// last page with products
		{"catalog fetch fails midway", 20, 20, []int{21}, 20},
		{"first catalog fetch fails", 20, -1, []int{20}, 20},
		{"past the end of the catalog", 99, -1, nil, 99},
	}

	for _, tt := range tests {
		var report crawlReport
		for _, page := range tt.failedPages {
			report.fail(page, "boom")
		}
		if page := report.resumePage(tt.start, tt.lastPage); page != tt.resumePage {
			t.Errorf("%s: resumePage() = %d, want %d", tt.name, page, tt.resumePage)
		}
	}
}

func TestCrawlReportFail(t *testing.T) {
	var report crawlReport
	report.fail(21, "Secret Lair x Lofi Girl: Beats to Cast To (link), page 21: giving up\nafter 5 attempt(s)")
	want := "Secret Lair x Lofi Girl: Beats to Cast To (link), page 21: giving up after 5 attempt(s)"
	if len(report.failures) != 1 || report.failures[0] != want {
		t.Errorf("failures = %q, want [%q]", report.failures, want)
	}
}

// A clock where sleeping only moves time forward
type fakeClock struct{ now time.Time }

func (c *fakeClock) Now() time.Time        { return c.now }
func (c *fakeClock) Sleep(d time.Duration) { c.now = c.now.Add(d) }

// Scryfall allows 2 searches per second, even right after a long idle
func TestScryfallLimiterNoBurst(t *testing.T) {
	clock := &fakeClock{now: time.Unix(0, 0)}
	limiter := newScryfallLimiter(ratelimit.WithClock(clock))

	limiter.Take()
	clock.Sleep(time.Minute)
	prev := limiter.Take()
	for i := range 10 {
		next := limiter.Take()
		if gap := next.Sub(prev); gap < 500*time.Millisecond {
			t.Fatalf("request %d after idle came %v after the previous one", i+2, gap)
		}
		prev = next
	}
}

func TestCrawlReportFailed(t *testing.T) {
	tests := []struct {
		name   string
		report crawlReport
		failed bool
	}{
		{"decklists written", crawlReport{products: 46, written: 36}, false},
		{"partial failure", crawlReport{products: 46, written: 35, failures: []string{"x"}}, false},
		// A new page that so far holds only a bundle is not a broken run
		{"every product skipped", crawlReport{products: 1}, false},
		{"every product failed", crawlReport{products: 2, failures: []string{"x", "y"}}, true},
		{"skipped and failed, nothing written", crawlReport{products: 2, failures: []string{"x"}}, true},
		{"first catalog fetch failed", crawlReport{failures: []string{"catalog page 21: boom"}}, true},
		{"past the end of the catalog", crawlReport{}, true},
	}

	for _, tt := range tests {
		if failed := tt.report.failed(); failed != tt.failed {
			t.Errorf("%s: failed() = %v, want %v", tt.name, failed, tt.failed)
		}
	}
}

func TestIsScryfallError(t *testing.T) {
	notFound := &scryfall.Error{Status: http.StatusNotFound, Code: "not_found"}
	rateLimited := &scryfall.Error{Status: http.StatusTooManyRequests, Code: "rate_limited"}
	network := &url.Error{Op: "Get", URL: "https://api.scryfall.com/cards/search", Err: errors.New("connection reset by peer")}

	tests := []struct {
		name string
		err  error
		code string
		want bool
	}{
		{"no error", nil, "not_found", false},
		{"not found", notFound, "not_found", true},
		{"wrapped not found", fmt.Errorf("scryfall search: %w", notFound), "not_found", true},
		{"rate limited is not not found", rateLimited, "not_found", false},
		{"rate limited", rateLimited, "rate_limited", true},
		{"network error", network, "not_found", false},
		{"code only in the text", errors.New("not_found: no such card"), "not_found", false},
	}
	for _, tt := range tests {
		if got := isScryfallError(tt.err, tt.code); got != tt.want {
			t.Errorf("%s: isScryfallError(%v, %q) = %v, want %v", tt.name, tt.err, tt.code, got, tt.want)
		}
	}
}

type scryfallReply struct {
	status int
	body   string
}

// Bodies as api.scryfall.com sends them, details shortened
var (
	replyCard        = scryfallReply{http.StatusOK, `{"object":"list","total_cards":1,"has_more":false,"data":[{"object":"card","name":"Sol Ring","collector_number":"2822","type_line":"Artifact"}]}`}
	replyNotFound    = scryfallReply{http.StatusNotFound, `{"object":"error","code":"not_found","status":404,"details":"Your query didn’t match any cards."}`}
	replyRateLimited = scryfallReply{http.StatusTooManyRequests, `{"object":"error","code":"rate_limited","status":429,"details":"You are being rate-limited, try again after 60 seconds."}`}
	replyBadRequest  = scryfallReply{http.StatusBadRequest, `{"object":"error","code":"bad_request","status":400,"warnings":null,"details":"Your search contains unclosed parentheses."}`}
	replyOutage      = scryfallReply{http.StatusBadGateway, `<html><body>502 Bad Gateway</body></html>`}
)

// A Scryfall client whose server answers each request with the next reply
func scriptedScryfall(t *testing.T, replies ...scryfallReply) (*scryfall.Client, *atomic.Int32) {
	t.Helper()
	var requests atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n := int(requests.Add(1))
		if r.URL.Path != "/cards/search" {
			t.Errorf("request %d went to %s, want /cards/search", n, r.URL.Path)
		}
		if n > len(replies) {
			t.Errorf("unexpected request %d, only %d scripted", n, len(replies))
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		w.WriteHeader(replies[n-1].status)
		fmt.Fprint(w, replies[n-1].body)
	}))
	t.Cleanup(server.Close)

	client, err := scryfall.NewClient(scryfall.WithBaseURL(server.URL + "/"))
	if err != nil {
		t.Fatal(err)
	}
	return client, &requests
}

func TestSearchWithClient(t *testing.T) {
	defer func(wait time.Duration) { scryfallRateLimitWait = wait }(scryfallRateLimitWait)
	scryfallRateLimitWait = time.Millisecond

	solRing := []CardData{{Name: "Sol Ring", Number: "2822"}}
	tests := []struct {
		name     string
		replies  []scryfallReply
		want     []CardData
		wantCode string // the Scryfall error code expected, "-" for any other error
	}{
		{"match", []scryfallReply{replyCard}, solRing, ""},
		{"no such card", []scryfallReply{replyNotFound}, nil, ""},
		{"rate limited, then served", []scryfallReply{replyRateLimited, replyCard}, solRing, ""},
		{"rate limited again after waiting", []scryfallReply{replyRateLimited, replyRateLimited}, nil, "rate_limited"},
		{"rate limited, then no such card", []scryfallReply{replyRateLimited, replyNotFound}, nil, ""},
		{"malformed query", []scryfallReply{replyBadRequest}, nil, "bad_request"},
		{"outage page", []scryfallReply{replyOutage}, nil, "-"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			client, requests := scriptedScryfall(t, tt.replies...)
			got, err := searchWithClient(context.Background(), client, "Sol Ring cn:2822")
			switch {
			case tt.wantCode == "" && err != nil:
				t.Fatalf("unexpected error: %v", err)
			case tt.wantCode == "-" && (err == nil || errors.As(err, new(*scryfall.Error))):
				t.Fatalf("expected a non-API error, got %v", err)
			case tt.wantCode != "" && tt.wantCode != "-" && !isScryfallError(err, tt.wantCode):
				t.Fatalf("expected a %s error, got %v", tt.wantCode, err)
			}
			if !slices.Equal(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
			if n := int(requests.Load()); n != len(tt.replies) {
				t.Errorf("made %d requests, want %d", n, len(tt.replies))
			}
		})
	}
}

func TestRejectedQueryErr(t *testing.T) {
	malformed := &scryfall.Error{Status: http.StatusBadRequest, Code: "bad_request"}
	otherMalformed := &scryfall.Error{Status: http.StatusBadRequest, Code: "bad_request", Details: "other"}

	tests := []struct {
		name     string
		cards    []CardData
		rejected []error
		want     error
	}{
		{"unnumbered, nothing rejected", []CardData{{Name: "Sol Ring"}}, []error{nil}, nil},
		{"rejected, numbered another way", []CardData{{Name: "Sol Ring", Number: "2822"}}, []error{malformed}, nil},
		{"rejected, never numbered", []CardData{{Name: "Sol Ring"}}, []error{malformed}, malformed},
		{
			"only the unnumbered card counts",
			[]CardData{{Name: "Sol Ring", Number: "2822"}, {Name: "Brainstorm"}, {Name: "Nyx Lotus"}},
			[]error{malformed, nil, otherMalformed},
			otherMalformed,
		},
	}
	for _, tt := range tests {
		if got := rejectedQueryErr(tt.cards, tt.rejected); got != tt.want {
			t.Errorf("%s: rejectedQueryErr() = %v, want %v", tt.name, got, tt.want)
		}
	}
}

func TestSearchWithClientNetworkError(t *testing.T) {
	server := httptest.NewServer(http.NotFoundHandler())
	server.Close()
	client, err := scryfall.NewClient(scryfall.WithBaseURL(server.URL + "/"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := searchWithClient(context.Background(), client, "Sol Ring"); err == nil {
		t.Error("expected an error from an unreachable server")
	}
}

func TestSearchWithClientCancelledWait(t *testing.T) {
	defer func(wait time.Duration) { scryfallRateLimitWait = wait }(scryfallRateLimitWait)
	scryfallRateLimitWait = time.Hour

	client, requests := scriptedScryfall(t, replyRateLimited)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Millisecond)
	defer cancel()
	if _, err := searchWithClient(ctx, client, "Sol Ring"); !errors.Is(err, context.DeadlineExceeded) {
		t.Errorf("expected the wait to end with the context, got %v", err)
	}
	if n := requests.Load(); n != 1 {
		t.Errorf("made %d requests, want 1", n)
	}
}

// A product page reduced to the parts the scraper reads
const testProductPage = `<html><body>
<h1 class="product-title">Secret Lair x Lofi Girl: Beats to Cast To</h1>
<div class="force-overflow"><ul>
<li>1x Felidar Guardian</li>
<li>2x Galaxy Foil Sol Ring</li>
<li>Includes the following</li>
</ul></div>
</body></html>`

func testDocument(t *testing.T, html string) *goquery.Document {
	t.Helper()
	doc, err := goquery.NewDocumentFromReader(strings.NewReader(html))
	if err != nil {
		t.Fatal(err)
	}
	return doc
}

func TestParseCardList(t *testing.T) {
	tests := []struct {
		name  string
		html  string
		cards []CardData
	}{
		{
			// A line that is not a card is skipped
			name: "bullet points",
			html: testProductPage,
			cards: []CardData{
				{Name: "Felidar Guardian", Count: 1},
				{Name: "Sol Ring", Count: 2, Foil: true},
			},
		},
		{
			name: "no card list at all",
			html: `<h1 class="product-title">Secret Lair x MSCHF: The Zeta Set</h1><p>3 common cards</p>`,
		},
	}

	for _, tt := range tests {
		cards := parseCardList(testDocument(t, tt.html), testNames)
		if !slices.Equal(cards, tt.cards) {
			t.Errorf("%s: parseCardList() = %+v, want %+v", tt.name, cards, tt.cards)
		}
	}
}

func TestGalleryFoldMode(t *testing.T) {
	tests := []struct {
		title     string
		cardCount int
		fold      bool
	}{
		// Front and back of every card
		{"Gallery (10)", 5, true},
		{"Gallery (10)", 10, false},
		{"Gallery", 5, false},
	}

	for _, tt := range tests {
		doc := testDocument(t, `<h2 class="pdp_title">`+tt.title+`</h2>`)
		if fold := galleryFoldMode(doc, tt.cardCount); fold != tt.fold {
			t.Errorf("galleryFoldMode(%q, %d) = %v, want %v", tt.title, tt.cardCount, fold, tt.fold)
		}
	}
}

// Canned Scryfall results by query, or an error for the queries in errs
type fakeSearch struct {
	results map[string][]CardData
	errs    map[string]error
	queries []string
}

func (f *fakeSearch) search(_ context.Context, query string) ([]CardData, error) {
	f.queries = append(f.queries, query)
	if err := f.errs[query]; err != nil {
		return nil, err
	}
	return slices.Clone(f.results[query]), nil
}

func TestMatchEdition(t *testing.T) {
	headers := []scryfallHeader{
		{Title: "Pixel Pals", URI: "https://scryfall.com/search?q=pixel"},
		{Title: "Lofi Girl: Beats to Cast To", URI: "https://scryfall.com/search?q=lofi"},
		{Title: "Lofi Girl: Beats to Cast To", URI: "https://scryfall.com/search?q=lofi2"},
	}
	edition := []CardData{
		{Name: "Felidar Guardian", Number: "2821"},
		{Name: "Sol Ring", Number: "2822"},
	}
	scraped := func() []CardData {
		return []CardData{{Name: "Sol Ring", Count: 1, Foil: true}, {Name: "Felidar Guardian", Count: 1, Foil: true}}
	}
	badRequest := &scryfall.Error{Status: http.StatusBadRequest, Code: "bad_request"}
	serverErr := errors.New("scryfall search: 500")

	tests := []struct {
		name     string
		title    string
		search   fakeSearch
		cards    []CardData
		matched  bool
		rejected error
		err      error
	}{
		{
			// The Foil Edition suffix is dropped for matching, so a
			// shortened title still fuzzy-matches the edition; the cards
			// are numbered by name
			name:    "same count, aligned by name",
			title:   "Lofi Girl Foil Edition",
			search:  fakeSearch{results: map[string][]CardData{"lofi": edition}},
			cards:   []CardData{{Name: "Sol Ring", Number: "2822", Count: 1, Foil: true}, {Name: "Felidar Guardian", Number: "2821", Count: 1, Foil: true}},
			matched: true,
		},
		{
			name:  "different count, Scryfall's list wins",
			title: "Lofi Girl: Beats to Cast To",
			search: fakeSearch{results: map[string][]CardData{"lofi": append(slices.Clone(edition),
				CardData{Name: "Careful Study", Number: "2823"})}},
			cards: []CardData{
				{Name: "Felidar Guardian", Number: "2821", Count: 1, Foil: true},
				{Name: "Sol Ring", Number: "2822", Count: 1, Foil: true},
				{Name: "Careful Study", Number: "2823", Count: 1, Foil: true},
			},
			matched: true,
		},
		{
			name:    "an empty search moves on to the next matching edition",
			title:   "Lofi Girl: Beats to Cast To",
			search:  fakeSearch{results: map[string][]CardData{"lofi2": edition}},
			cards:   []CardData{{Name: "Sol Ring", Number: "2822", Count: 1, Foil: true}, {Name: "Felidar Guardian", Number: "2821", Count: 1, Foil: true}},
			matched: true,
		},
		{
			name:     "a malformed search is kept, and the next edition is tried",
			title:    "Lofi Girl: Beats to Cast To",
			search:   fakeSearch{errs: map[string]error{"lofi": badRequest}},
			cards:    scraped(),
			rejected: badRequest,
		},
		{
			name:   "any other Scryfall error fails the product",
			title:  "Lofi Girl: Beats to Cast To",
			search: fakeSearch{errs: map[string]error{"lofi": serverErr}},
			cards:  scraped(),
			err:    serverErr,
		},
		{
			name:  "no edition matches",
			title: "Secret Lair x Something Else Entirely",
			cards: scraped(),
		},
	}

	for _, tt := range tests {
		match, err := matchEdition(context.Background(), tt.search.search, headers, tt.title, scraped())
		if !errors.Is(err, tt.err) {
			t.Errorf("%s: error %v, want %v", tt.name, err, tt.err)
			continue
		}
		if match.matched != tt.matched || match.rejected != tt.rejected || !slices.Equal(match.cards, tt.cards) {
			t.Errorf("%s: matchEdition() = %+v, want cards %+v, matched %v, rejected %v",
				tt.name, match, tt.cards, tt.matched, tt.rejected)
		}
	}
}

func TestBackfillNumbers(t *testing.T) {
	cards := []CardData{
		{Name: "Sisay, Weatherlight Captain", Number: "2778"},
		{Name: "Silence"},
		{Name: "Emiel the Blessed", Number: "2780"},
		{Name: "Hajar Loyal Bodyguard"},
		{Name: "Kutzil, Malamet Exemplar"},
		{Name: "Sol Ring"},
	}
	badRequest := &scryfall.Error{Status: http.StatusBadRequest, Code: "bad_request"}
	search := fakeSearch{
		results: map[string][]CardData{
			"Silence cn:2779": {{Name: "Silence", Number: "2779"}},
			// Scryfall's spelling is adopted along with the number
			"Hajar Loyal Bodyguard cn:2781": {{Name: "Hajar, Loyal Bodyguard", Number: "2781"}},
		},
		errs: map[string]error{"Sol Ring cn:2783": badRequest},
	}
	rejected := make([]error, len(cards))

	// Numbers run on from the longest one found, Sisay's, in card order;
	// Kutzil's guess is not validated and stays unnumbered
	if err := backfillNumbers(context.Background(), search.search, cards, rejected); err != nil {
		t.Fatal(err)
	}
	want := []CardData{
		{Name: "Sisay, Weatherlight Captain", Number: "2778"},
		{Name: "Silence", Number: "2779"},
		{Name: "Emiel the Blessed", Number: "2780"},
		{Name: "Hajar, Loyal Bodyguard", Number: "2781"},
		{Name: "Kutzil, Malamet Exemplar"},
		{Name: "Sol Ring"},
	}
	if !slices.Equal(cards, want) {
		t.Errorf("backfillNumbers() = %+v, want %+v", cards, want)
	}
	if rejected[5] != badRequest || slices.ContainsFunc(rejected[:5], func(err error) bool { return err != nil }) {
		t.Errorf("rejected = %v, want only Sol Ring's malformed query", rejected)
	}

	// Any other Scryfall error fails the product
	serverErr := errors.New("scryfall search: 500")
	cards = []CardData{{Name: "Silence", Number: "2779"}, {Name: "Sol Ring"}}
	search = fakeSearch{errs: map[string]error{"Sol Ring cn:2780": serverErr}}
	if err := backfillNumbers(context.Background(), search.search, cards, make([]error, 2)); !errors.Is(err, serverErr) {
		t.Errorf("expected the Scryfall error, got %v", err)
	}

	// With no number to anchor a guess on, nothing is searched
	cards = []CardData{{Name: "Silence"}, {Name: "Sol Ring"}}
	search = fakeSearch{}
	if err := backfillNumbers(context.Background(), search.search, cards, make([]error, 2)); err != nil || len(search.queries) != 0 {
		t.Errorf("expected no searches and no error, got %q, %v", search.queries, err)
	}
}

func TestScrapeProduct(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/us/product/1254382":
			fmt.Fprint(w, testProductPage)
		case "/redesigned":
			fmt.Fprint(w, `<h1 class="pdp-title">Lofi Girl</h1>`)
		default:
			fmt.Fprint(w, `<h1 class="product-title">Secret Lair x MSCHF: The Zeta Set</h1>`)
		}
	}))
	defer server.Close()

	// No editions and no gallery: nothing reaches Scryfall
	cardSet, err := scrapeProduct(context.Background(), nil, testNames, server.URL+"/us/product/1254382", false)
	if err != nil {
		t.Fatal(err)
	}
	want := CardSet{
		Title:    "Lofi Girl: Beats to Cast To",
		Filename: "Lofi Girl- Beats to Cast To",
		Cards: []CardData{
			{Name: "Felidar Guardian", Count: 1},
			{Name: "Sol Ring", Count: 2, Foil: true},
		},
	}
	if cardSet.Title != want.Title || cardSet.Filename != want.Filename || !slices.Equal(cardSet.Cards, want.Cards) {
		t.Errorf("scrapeProduct() = %+v, want %+v", *cardSet, want)
	}

	if _, err := scrapeProduct(context.Background(), nil, testNames, server.URL+"/us/product/1254424", false); err == nil || err.Error() != "no cards found" {
		t.Errorf("expected no cards found, got %v", err)
	}

	// A page without the title the scraper expects has changed its markup
	if _, err := scrapeProduct(context.Background(), nil, testNames, server.URL+"/redesigned", false); err == nil || err.Error() != "no product title found" {
		t.Errorf("expected no product title found, got %v", err)
	}
}

func TestHTTPGet(t *testing.T) {
	var userAgent atomic.Value
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		userAgent.Store(r.UserAgent())
		if r.URL.Path == "/gone" {
			http.NotFound(w, r)
			return
		}
		fmt.Fprint(w, "ok")
	}))
	defer server.Close()

	resp, err := httpGet(context.Background(), server.URL+"/page")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if ua := userAgent.Load(); ua != "sldownloader/1.0 (+https://github.com/mtgban/sldownloader)" {
		t.Errorf("User-Agent = %q", ua)
	}

	// A removed page is an error, not an empty page to parse
	if _, err := httpGet(context.Background(), server.URL+"/gone"); err == nil || !strings.Contains(err.Error(), "404 Not Found") {
		t.Errorf("expected a 404 error, got %v", err)
	}
}

func TestParseEditionHeaders(t *testing.T) {
	doc := testDocument(t, `<div class="card-grid-header-content">
<a href="https://scryfall.com/search?q=e%3Asld+cn%E2%89%A52821+cn%E2%89%A42825">Lofi Girl: Beats to Cast To</a>
• 5 cards</div>`)
	want := []scryfallHeader{{
		Title: "Lofi Girl: Beats to Cast To",
		URI:   "https://scryfall.com/search?q=e%3Asld+cn%E2%89%A52821+cn%E2%89%A42825",
	}}
	if headers := parseEditionHeaders(doc); !slices.Equal(headers, want) {
		t.Errorf("parseEditionHeaders() = %+v, want %+v", headers, want)
	}

	// A page whose markup no longer has the editions finds none, which
	// loadScryfallHeaders reports as an error
	if headers := parseEditionHeaders(testDocument(t, `<h1>Secret Lair Drop</h1>`)); len(headers) != 0 {
		t.Errorf("expected no editions, got %+v", headers)
	}
}

func TestSearchCache(t *testing.T) {
	client, requests := scriptedScryfall(t, replyCard, replyNotFound, replyOutage, replyCard)
	cache := &searchCache{results: map[string][]CardData{}}
	ctx := context.Background()
	solRing := []CardData{{Name: "Sol Ring", Number: "2822"}}

	// A repeated query is answered from the cache, and what a caller does
	// to its copy does not reach the cache (matchCardNumbers clears numbers)
	got, err := cache.search(ctx, client, "Sol Ring cn:2822")
	if err != nil || !slices.Equal(got, solRing) {
		t.Fatalf("got %+v, %v", got, err)
	}
	for range 2 {
		got[0].Number = ""
		got, err = cache.search(ctx, client, "Sol Ring cn:2822")
		if err != nil || !slices.Equal(got, solRing) || requests.Load() != 1 {
			t.Errorf("repeat: got %+v, %v after %d requests, want %+v after 1", got, err, requests.Load(), solRing)
		}
	}

	// "No such card" is an answer too
	for range 2 {
		if got, err := cache.search(ctx, client, "Fog cn:2822"); got != nil || err != nil {
			t.Errorf("no such card: got %+v, %v", got, err)
		}
	}
	if n := requests.Load(); n != 2 {
		t.Errorf("made %d requests, want 2", n)
	}

	// An error is not kept, so the next try asks again
	if _, err := cache.search(ctx, client, "Island cn:2823"); err == nil {
		t.Error("expected the outage to be an error")
	}
	if got, err := cache.search(ctx, client, "Island cn:2823"); err != nil || len(got) != 1 {
		t.Errorf("retry after an error: got %+v, %v", got, err)
	}
	if n := requests.Load(); n != 4 {
		t.Errorf("made %d requests, want 4", n)
	}
}

func TestCrawlInterrupted(t *testing.T) {
	// Cancelled before it starts, as by Ctrl-C: nothing is fetched, and
	// the next crawl resumes on the page this one stopped at
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	report := crawl(ctx, nil, nil, 20, false)
	want := []string{"interrupted: context canceled"}
	if !slices.Equal(report.failures, want) || report.written != 0 || report.nextPage != 20 {
		t.Errorf("crawl() = %+v, want failures %q, nothing written, next page 20", report, want)
	}
}

func TestFormatVersion(t *testing.T) {
	tests := []struct {
		info    debug.BuildInfo
		version string
	}{
		// A build from a checkout, whose version already names the commit
		{debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261001113826-4d78459cd2c3+dirty"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "4d78459cd2c3e1f0a9b8c7d6e5f4a3b2c1d0e9f8"},
			{Key: "vcs.modified", Value: "true"},
		}}, "v0.0.0-20261001113826-4d78459cd2c3+dirty"},
		// A build whose version does not, so the commit is added
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "4d78459ab12c34de56f7890123456789abcdef01"},
			{Key: "vcs.modified", Value: "false"},
		}}, "(devel) 4d78459ab12c"},
		{debug.BuildInfo{Main: debug.Module{Version: "(devel)"}, Settings: []debug.BuildSetting{
			{Key: "vcs.revision", Value: "4d78459ab12c34de56f7890123456789abcdef01"},
			{Key: "vcs.modified", Value: "true"},
		}}, "(devel) 4d78459ab12c+dirty"},
		// go install github.com/mtgban/sldownloader@<version>
		{debug.BuildInfo{Main: debug.Module{Version: "v0.0.0-20261001120000-4d78459ab12c"}}, "v0.0.0-20261001120000-4d78459ab12c"},
		{debug.BuildInfo{}, "(unknown)"},
	}

	for _, tt := range tests {
		if version := formatVersion(&tt.info); version != tt.version {
			t.Errorf("formatVersion(%+v) = %q, want %q", tt.info, version, tt.version)
		}
	}
}
