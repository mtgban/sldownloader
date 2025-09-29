package main

import (
	"context"
	"net/http"
	"net/url"
	"slices"
	"strings"

	"github.com/BlueMonday/go-scryfall"
	"github.com/PuerkitoBio/goquery"
	"github.com/hashicorp/go-cleanhttp"
)

const scryfallURL = "https://scryfall.com/sets/sld"
const titleClass = ".card-grid-header-content"

type scryfallHeader struct {
	Title string
	URI   string
}

func loadScryfallHeaders(ctx context.Context) ([]scryfallHeader, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, scryfallURL, http.NoBody)
	if err != nil {
		return nil, err
	}
	resp, err := cleanhttp.DefaultClient().Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	doc, err := goquery.NewDocumentFromReader(resp.Body)
	if err != nil {
		return nil, err
	}

	var headers []scryfallHeader
	doc.Find(titleClass).Each(func(i int, s *goquery.Selection) {
		title := s.Text()
		title = strings.Split(title, "•")[0]
		title = strings.TrimSpace(title)

		uri, _ := s.Find("a").Attr("href")
		headers = append(headers, scryfallHeader{
			Title: title,
			URI:   uri,
		})
	})

	return headers, nil
}

// Make a search call rebuilding the query used in the headers
func searchURI(ctx context.Context, uri string) ([]CardData, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	return search(ctx, u.Query().Get("q"))
}

func search(ctx context.Context, query string) ([]CardData, error) {
	client, err := scryfall.NewClient()
	if err != nil {
		return nil, err
	}

	so := scryfall.SearchCardsOptions{
		Unique:        scryfall.UniqueModePrints,
		Order:         scryfall.OrderSet,
		Dir:           scryfall.DirAsc, // Order by CNs
		IncludeExtras: true,
	}
	result, err := client.SearchCards(ctx, query, so)
	if err != nil {
		return nil, err
	}

	var out []CardData
	for _, card := range result.Cards {
		// Make sure to exclude bonus cards, they are tracked elsewhere
		if slices.Contains(card.PromoTypes, "sldbonus") {
			continue
		}
		// Skip (older) duplicated foil-only cards
		if strings.HasSuffix(card.CollectorNumber, "★") {
			continue
		}

		// Only preserve one chunk of the card
		name := strings.Split(card.Name, " // ")[0]

		number := card.CollectorNumber
		// Special case since upstream treates faces differently
		if len(card.CardFaces) > 0 {
			number += "a"
		}

		// In case we need it for later
		isToken := strings.Contains(card.TypeLine, "Token")

		out = append(out, CardData{
			Name:   name,
			Number: number,
			Token:  isToken,
		})
	}

	return out, nil
}
