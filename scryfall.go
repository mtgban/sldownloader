package main

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/BlueMonday/go-scryfall"
	"github.com/PuerkitoBio/goquery"
	"github.com/hashicorp/go-cleanhttp"
	"go.uber.org/ratelimit"
)

const scryfallURL = "https://scryfall.com/sets/sld"
const titleClass = ".card-grid-header-content"

// Scryfall allows 2 requests per second on /cards/search, which every search
// uses; the only other call is the card-name catalog, once per run
const scryfallReqPerSecond = 2

// A rate_limited error asks to try again after 60 seconds, and every request
// in that window is refused; wait a little longer so the retry lands after it
var scryfallRateLimitWait = 65 * time.Second

// The rate limiter lives on the client, so a single shared client is needed
// for it to actually pace requests across calls
var (
	scryfallClientOnce sync.Once
	scryfallClient     *scryfall.Client
	scryfallClientErr  error
)

func getScryfallClient() (*scryfall.Client, error) {
	scryfallClientOnce.Do(func() {
		scryfallClient, scryfallClientErr = scryfall.NewClient(
			scryfall.WithUserAgent("sldownloader/1.0"),
			scryfall.WithLimiter(newScryfallLimiter()),
		)
	})
	return scryfallClient, scryfallClientErr
}

// Without slack, so that idle time (OCR, Wizards page fetches) never banks
// a burst of back-to-back requests
func newScryfallLimiter(opts ...ratelimit.Option) ratelimit.Limiter {
	return ratelimit.New(scryfallReqPerSecond, append(opts, ratelimit.WithoutSlack)...)
}

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

// Load every real card name, which cleanLine must never cut into
func loadCardNames(ctx context.Context) (*cardNames, error) {
	client, err := getScryfallClient()
	if err != nil {
		return nil, err
	}
	catalog, err := client.GetCardNamesCatalog(ctx)
	if err != nil {
		return nil, err
	}
	if len(catalog.Data) == 0 {
		return nil, errors.New("empty card name catalog")
	}
	return newCardNames(catalog.Data), nil
}

// Make a search call rebuilding the query used in the headers
func searchURI(ctx context.Context, uri string) ([]CardData, error) {
	u, err := url.Parse(uri)
	if err != nil {
		return nil, err
	}
	return search(ctx, u.Query().Get("q"))
}

// Whether err is a Scryfall API error with this code, such as not_found for
// a search that matches no card
func isScryfallError(err error, code string) bool {
	var scryfallErr *scryfall.Error
	return errors.As(err, &scryfallErr) && scryfallErr.Code == code
}

func search(ctx context.Context, query string) ([]CardData, error) {
	client, err := getScryfallClient()
	if err != nil {
		return nil, err
	}
	return searchWithClient(ctx, client, query)
}

// A query that matches no card returns no cards and no error. Anything else
// Scryfall refuses (rate limiting that outlasts one wait, a server or network
// error, a malformed query) is an error, never mistaken for no such card.
func searchWithClient(ctx context.Context, client *scryfall.Client, query string) ([]CardData, error) {
	so := scryfall.SearchCardsOptions{
		Unique:        scryfall.UniqueModePrints,
		Order:         scryfall.OrderSet,
		Dir:           scryfall.DirAsc, // Order by CNs
		IncludeExtras: true,
	}
	result, err := client.SearchCards(ctx, query, so)
	if isScryfallError(err, "rate_limited") {
		log.Printf("Rate limited by Scryfall, retrying in %s", scryfallRateLimitWait)
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(scryfallRateLimitWait):
		}
		result, err = client.SearchCards(ctx, query, so)
	}
	if isScryfallError(err, "not_found") {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("scryfall search %q: %w", query, err)
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
