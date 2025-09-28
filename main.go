package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"log"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/PuerkitoBio/goquery"
	"github.com/hashicorp/go-retryablehttp"
	"github.com/lithammer/fuzzysearch/fuzzy"
	"github.com/otiai10/gosseract/v2"
)

func getImageBytes(link string) ([]byte, error) {
	retryClient := retryablehttp.NewClient()
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
	client.SetWhitelist("0123456789 ™ ©")

	data, err := getImageBytes(link)
	if err != nil {
		return "", err
	}

	client.SetImageFromBytes(data)

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
}

// Derive the card name, removing any special tag
func cleanLine(cardLine string) (string, int, error) {
	// Unicode characters
	cardLine = strings.Replace(cardLine, " ", " ", -1)
	cardLine = strings.Replace(cardLine, "’", "'", -1)
	cardLine = strings.Replace(cardLine, "”", "\"", -1)
	cardLine = strings.Replace(cardLine, "“", "\"", -1)
	cardLine = strings.TrimSpace(cardLine)

	fields := strings.Split(cardLine, "x ")
	if len(fields) != 2 {
		return "", 0, errors.New("unexpected line format")
	}

	num, err := strconv.Atoi(fields[0])
	if err != nil {
		return "", 0, errors.New("invalid number in line")
	}
	cardLine = strings.TrimSpace(fields[1])

	// Remove anything appearing after a parenthesis
	if strings.Contains(cardLine, "(") {
		cardLine = strings.Split(cardLine, "(")[0]
	}

	// Remove everything before "Foil" to catch variants like Galaxy Textured etc,
	// as long as they are before the card name
	if strings.Contains(cardLine, "Foil") &&
		!strings.HasSuffix(cardLine, "Foil Edition") && !strings.HasSuffix(cardLine, "Foil Etched") {
		fields := strings.Split(cardLine, "Foil")
		cardLine = fields[1]
	}

	// Remove this tag except for the cards with Phyrexian in them
	if !strings.Contains(cardLine, "Tower") &&
		!strings.Contains(cardLine, "Crusader") &&
		!strings.Contains(cardLine, "Unlife") {
		cardLine = strings.Replace(cardLine, "Phyrexian", "", -1)
	}

	// Remove random prefixes from card names
	for _, tag := range []string{
		"Full-Text", "Full-Art", "Full-art", "Alt-Art",
		"Reversible", "Old Frame", "Retro Frame",
		"Poster", "Stained Glass",
		"Foil-etched", "Etched", "Foil", "Tokens", "Token",
		"Different", "Hand-Drawn", "Borderless",
		"Showcase", "Left-Handed", "Edition", "cards", "Japanese",
		"Regular Human Guy", "Ichor-E", "DFC", "Italian-language", "*",
	} {
		cardLine = strings.Replace(cardLine, tag, "", -1)
		cardLine = strings.Replace(cardLine, strings.ToLower(tag), "", -1)
	}

	// Remove flavor names
	if strings.Contains(cardLine, " as ") {
		cardLine = strings.Split(cardLine, " as ")[0]
	}

	// There is no card with " by " yet...
	if strings.Contains(cardLine, " by ") {
		cardLine = strings.Split(cardLine, " by ")[0]
	}

	// Bob Ross Drop
	if strings.Contains(cardLine, " with art") {
		cardLine = strings.Split(cardLine, " with art")[0]
	}

	// Standardize DFC
	if strings.Contains(cardLine, "//") && !strings.Contains(cardLine, " // ") {
		cardLine = strings.Replace(cardLine, "//", " // ", -1)
	}

	// Only keep one face of the card
	cardLine = strings.Split(cardLine, " // ")[0]

	// Use upstream sheet name
	cardLine = strings.Replace(cardLine, "Sticker Sheets", "Sticker sheet", -1)

	// Typo
	cardLine = strings.Replace(cardLine, "Xenegos", "Xenagos", -1)

	return strings.TrimSpace(cardLine), num, nil
}

var replacerStrings = []string{
	// Unicode characters
	" ", " ",
	"’", "'",
	"‘", "'",
	"®", "",
	"™", "",
	// Windows special characters
	"<", "",
	">", "",
	"/", "",
	"\\", "",
	"*", "",
	" - ", " ",
	// Compatibility layer
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
	if strings.Contains(title, "|") {
		ogTitle := title
		title = strings.Split(title, " | ")[0]
		if strings.HasSuffix(ogTitle, "Foil Edition") {
			title += " Foil Edition"
		}
	}

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

	title = strings.Replace(title, ":", "-", -1)
	filename := strings.TrimSpace(title)

	return filename, originalName
}

func scrapeProduct(headers []scryfallHeader, link string, doOCR bool) (*CardSet, error) {
	resp, err := retryablehttp.Get(link)
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
		var card CardData

		line := s.Text()
		cardLine, num, err := cleanLine(line)
		if err != nil {
			log.Printf("%s - %s", cardLine, err.Error())
			return
		}

		card.Foil = strings.Contains(strings.ToLower(line), "foil")
		card.Etched = strings.Contains(strings.ToLower(line), "etched")
		card.Token = strings.Contains(strings.ToLower(line), "token")
		card.Name = cardLine
		for i := 0; i < num; i++ {
			log.Printf("'%s'", card.Name)
			cards = append(cards, card)

			// Special hack for this set
			if strings.Contains(title, "Astrology Lands") {
				break
			}
		}
	})

	if len(cards) == 0 {
		// Fallback if there were no bullet points
		productInfo, _ := doc.Find(`div[id="collapse2"] div[class="force-overflow"] p[class="product-information"]`).Html()
		for _, line := range strings.Split(productInfo, "<br/>") {
			var card CardData

			cardLine, num, err := cleanLine(line)
			if err != nil {
				continue
			}

			card.Foil = strings.Contains(strings.ToLower(line), "foil")
			card.Etched = strings.Contains(strings.ToLower(line), "etched")
			card.Token = strings.Contains(strings.ToLower(line), "token")
			card.Name = cardLine
			for i := 0; i < num; i++ {
				log.Printf("'%s'", card.Name)
				cards = append(cards, card)
				if strings.Contains(title, "Astrology Lands") {
					break
				}
			}
		}
		if len(cards) == 0 {
			return nil, errors.New("no cards found")
		}
	}

	cardSet.Cards = cards

	foundMatch := false
	cleanTitle := cardSet.Title
	cleanTitle = strings.ReplaceAll(cleanTitle, " Foil Edition", "")
	cleanTitle = strings.ReplaceAll(cleanTitle, " Raised", "")
	cleanTitle = strings.ReplaceAll(cleanTitle, " Galaxy", "")

	for _, header := range headers {
		a := strings.ToLower(cleanTitle)
		b := strings.ToLower(header.Title)
		if !(fuzzy.Match(a, b) || strings.Contains(a, b) || strings.Contains(b, a)) {
			continue
		}

		results, err := search(context.TODO(), header.URI)
		if err != nil {
			log.Println(err.Error())
			continue
		}

		log.Printf("Found these possible card numbers: %+q", results)
		for i, card := range cards {
			cards[i].Number = results[card.Name]
		}
		foundMatch = true
		break
	}
	if !foundMatch {
		log.Println(cleanTitle, "was not found, no numbers available!")
	}

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
					cards[j].Number = fmt.Sprint(cn + j - pos)
				}
			}
		} else {
			log.Println("...worth a shot")
		}
	}

	return &cardSet, nil
}

func dumpCards(cardSet *CardSet, link, releaseDate string) error {
	fileName := cardSet.Filename + ".txt"
	file, err := os.Create(fileName)
	if err != nil {
		return err
	}
	defer file.Close()

	fmt.Fprintf(file, "// NAME: %s\n", cardSet.Title)
	fmt.Fprintf(file, "// SOURCE: %s\n", link)
	if releaseDate != "" {
		fmt.Fprintf(file, "// DATE: %s\n", releaseDate)
	}
	for _, card := range cardSet.Cards {
		if card.Number != "" {
			card.Number = ":" + card.Number
		}
		fmt.Fprintf(file, "1 [SLD%s] %s", card.Number, card.Name)
		if card.Foil {
			fmt.Fprintf(file, " [foil]")
		}
		if card.Etched {
			fmt.Fprintf(file, " [etched]")
		}
		if card.Token {
			fmt.Fprintf(file, " [token]")
		}

		fmt.Fprintf(file, "\n")
	}

	log.Printf("Created '%s' (%s)", fileName, releaseDate)

	return nil
}

func run() int {
	pageOpt := flag.Int("page", 0, "Which page to start from")
	doOCROpt := flag.Bool("ocr", false, "Enable OCR to derive collector numbers")
	flag.Parse()

	headers, err := loadScryfallHeaders(context.Background())
	if err != nil {
		log.Println("Unable to query scryfall")
		return 1
	}

	for i, arg := range flag.Args() {
		cardSet, err := scrapeProduct(headers, arg, *doOCROpt)
		if err != nil {
			log.Println("page", i, "-", err)
			return 1
		}

		err = dumpCards(cardSet, arg, "")
		if err != nil {
			log.Println(err)
			return 1
		}
		return 0
	}

	if *pageOpt == 0 {
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
			shouldSkip := false
			for _, desc := range product.Descriptions {
				// Skip any bundle and special releases
				if strings.Contains(desc.Title, "Bundle") ||
					strings.Contains(desc.Title, "BUNDLE") ||
					strings.Contains(desc.Title, "Festival in a Box") ||
					strings.Contains(desc.Title, "Transformers TCG") ||
					strings.Contains(desc.Title, "DRAGON’S ENDGAME") ||
					strings.Contains(desc.Title, "Secret Lair Commander Deck") ||
					strings.Contains(desc.Title, "They're Just Like Us but") ||
					strings.Contains(desc.Title, "Heads I Win, Tails") ||
					strings.Contains(desc.Title, "Deluxe Collection") ||
					strings.Contains(desc.Title, "Heroes of the Borderlands") ||
					strings.Contains(desc.Title, "Welcome to the Hellfire Club") ||
					strings.Contains(desc.Title, "D&D Sapphire Anniversary") ||
					strings.Contains(desc.Title, "30th Anniversary Edition") ||
					strings.Contains(desc.Title, "Japanese") ||
					strings.Contains(desc.Title, " JP") ||
					strings.Contains(desc.Title, " SP") ||
					strings.Contains(desc.Title, "Countdown Kit") {
					shouldSkip = true
				}
			}
			if shouldSkip {
				continue
			}

			releaseDate := product.ReleaseDate.Format("2006-01-02")

			link := "https://secretlair.wizards.com/us/product/" + product.ProductID
			cardSet, err := scrapeProduct(headers, link, *doOCROpt)
			if err != nil {
				log.Println("page", i-1, "-", err)
				continue
			}

			err = dumpCards(cardSet, link, releaseDate)
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
	retryClient := retryablehttp.NewClient()
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
