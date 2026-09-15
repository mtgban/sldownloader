package main

import (
	"testing"
)

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
	}

	for _, tt := range tests {
		name, count, tags, err := cleanLine(tt.line)
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
		_, _, _, err := cleanLine(line)
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
		cards, err = processLine(cards, line)
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
		cards, err := processLine(nil, tt.line)
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
		{Name: "Fog"},
	}
	results := []CardData{
		{Name: "Azusa, Lost but Seeking", Number: "2403"},
		{Name: "Dosan the Falling Leaf", Number: "2404"},
		{Name: "Fog Bank", Number: "2405"},
	}

	matchCardNumbers(cards, results)

	if cards[0].Name != "Dosan the Falling Leaf" || cards[0].Number != "2404" {
		t.Errorf("expected the Scryfall spelling and number to be adopted, got %+v", cards[0])
	}
	if cards[1].Name != "Azusa, Lost but Seeking" || cards[1].Number != "2403" {
		t.Errorf("expected an exact match, got %+v", cards[1])
	}
	if cards[2].Name != "Fog" || cards[2].Number != "" {
		t.Errorf("expected no match for a different name, got %+v", cards[2])
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
