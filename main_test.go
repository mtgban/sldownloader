package main

import (
	"testing"
)

func TestCleanLine(t *testing.T) {
	tests := []struct {
		line  string
		name  string
		count int
	}{
		// Card names containing "x " survive the count separator split
		{"1x Nyx Lotus", "Nyx Lotus", 1},
		// Tags are only removed on word boundaries
		{"2x Borderless Expedition Map", "Expedition Map", 2},
		// Card names containing a tag word are preserved
		{"1x Foil Etched Champion", "Etched Champion", 1},
		{"1x Lavinia, Foil to Conspiracy", "Lavinia, Foil to Conspiracy", 1},
		// Regular tag stripping still works
		{"3x Showcase Brainstorm (Retro Frame)", "Brainstorm", 3},
		{"1x Galaxy Foil Sol Ring", "Sol Ring", 1},
		{"1x Foil Etched Sol Ring", "Sol Ring", 1},
		{"4x Borderless Werewolf token", "Werewolf", 4},
		{"1x Phyrexian Tower", "Phyrexian Tower", 1},
		{"1x Phyrexian Vorinclex, Voice of Hunger", "Vorinclex, Voice of Hunger", 1},
		{"1x Growing Rites of Itlimoc // Itlimoc, Cradle of the Sun", "Growing Rites of Itlimoc", 1},
		{"1x Triumph of Hordes", "Triumph of the Hordes", 1},
	}

	for _, tt := range tests {
		name, count, err := cleanLine(tt.line)
		if err != nil {
			t.Errorf("cleanLine(%q) returned error: %v", tt.line, err)
			continue
		}
		if name != tt.name || count != tt.count {
			t.Errorf("cleanLine(%q) = %q, %d - expected %q, %d", tt.line, name, count, tt.name, tt.count)
		}
	}
}

func TestCleanLineErrors(t *testing.T) {
	for _, line := range []string{
		"no count here",
		"Includes the following",
	} {
		_, _, err := cleanLine(line)
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
