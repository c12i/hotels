// Command hotels normalises mixed-source hotel records into one schema.
package main

import (
	"encoding/json"
	"fmt"
	"html"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
)

type Coords struct {
	Lat float64 `json:"lat"`
	Lng float64 `json:"lng"`
}

// PostalAddress mirrors schema.org's PostalAddress fields. None of the
// current sources give us a street-level address, so this is always null
// for now; see DECISIONS.md for how it gets filled in later.
type PostalAddress struct {
	StreetAddress   string `json:"street_address"`
	AddressLocality string `json:"address_locality"`
	AddressRegion   string `json:"address_region,omitempty"`
	PostalCode      string `json:"postal_code"`
	AddressCountry  string `json:"address_country"`
}

type Location struct {
	City        string         `json:"city"`
	CountryCode string         `json:"country_code"`
	Coordinates *Coords        `json:"coordinates"`
	Address     *PostalAddress `json:"address"`
}

type Price struct {
	Amount   float64 `json:"amount"`
	Currency string  `json:"currency"`
	EUR      float64 `json:"eur"`
}

// Review is provisioned for richer review data than any current source
// gives us (just a snippet of text). The other fields are always null for
// now; see DECISIONS.md for how an LLM extraction service would fill them
// in from the underlying review pages.
type Review struct {
	Text       string  `json:"text"`
	Author     *string `json:"author"`
	Rating     *int    `json:"rating"`
	WrittenAt  *string `json:"written_at"`
	GuestCount *int    `json:"guest_count"`
	RoomType   *string `json:"room_type"`
}

type Hotel struct {
	Source      string   `json:"source"`
	Name        string   `json:"name"`
	Location    Location `json:"location"`
	Stars       *int     `json:"stars"`
	PriceFrom   *Price   `json:"price_from"`
	Description string   `json:"description"`
	Amenities   []string `json:"amenities"`
	Reviews     []Review `json:"reviews,omitempty"`
	LastSeen    string   `json:"last_seen,omitempty"`
	Notes       []string `json:"notes,omitempty"`
}

// Fixed rates for the prototype; a real pipeline would pull daily rates.
var toEUR = map[string]float64{"EUR": 1, "USD": 0.92}

var countryCodes = map[string]string{
	"it": "IT", "italy": "IT", "italien": "IT", "italia": "IT",
	"at": "AT", "austria": "AT", "österreich": "AT",
	"de": "DE", "germany": "DE", "deutschland": "DE",
	"mx": "MX", "mexico": "MX", "méxico": "MX",
}

// Approximate city centres, used to sanity-check coordinates.
var cityCentres = map[string]Coords{
	"rimini":           {44.0678, 12.5695},
	"innsbruck":        {47.2692, 11.4041},
	"berlin":           {52.5200, 13.4050},
	"playa del carmen": {20.6296, -87.0739},
}

var amenityAliases = map[string]string{
	"wifi": "wifi", "wi-fi": "wifi", "free wifi": "wifi",
	"pool": "pool", "swimming pool": "pool",
	"parking": "parking", "pets allowed": "pets_allowed",
	"spa": "spa", "swim-up bar": "swim_up_bar", "kids club": "kids_club",
}

var (
	tagRe   = regexp.MustCompile(`<[^>]*>`)
	spaceRe = regexp.MustCompile(`\s+`)
	numRe   = regexp.MustCompile(`\d+(\.\d+)?`)
	poolsRe = regexp.MustCompile(`^\d+\s+pools?$`)
	priceRe = regexp.MustCompile(`^\s*([\d.]+)\s*([A-Za-z]{3})\s*$`)
)

func str(r map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := r[k].(string); ok && strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func normalise(r map[string]any) Hotel {
	h := Hotel{Source: str(r, "source"), Name: str(r, "hotel_name", "name"), Amenities: []string{}}

	// Location: explicit city/country, or a "City, Country" string.
	// Address is always null here; see DECISIONS.md.
	h.Location.City = str(r, "city")
	country := str(r, "country")
	if loc := str(r, "location"); loc != "" {
		parts := strings.Split(loc, ",")
		if h.Location.City == "" {
			h.Location.City = strings.TrimSpace(parts[0])
		}
		if country == "" && len(parts) > 1 {
			country = strings.TrimSpace(parts[len(parts)-1])
		}
	}
	if code, ok := countryCodes[strings.ToLower(country)]; ok {
		h.Location.CountryCode = code
	} else if country != "" {
		h.Notes = append(h.Notes, fmt.Sprintf("unknown country %q", country))
	}
	if c, ok := r["coords"].(map[string]any); ok {
		lat, _ := c["lat"].(float64)
		lng, _ := c["lng"].(float64)
		h.Location.Coordinates = &Coords{lat, lng}
		if centre, ok := cityCentres[strings.ToLower(h.Location.City)]; ok {
			if d := haversineKm(*h.Location.Coordinates, centre); d > 50 {
				h.Notes = append(h.Notes, fmt.Sprintf("coords are %.0f km from %s centre", d, h.Location.City))
			}
		}
	}

	// Stars: number, "3 stars" string, or missing.
	switch v := r["stars"].(type) {
	case float64:
		n := int(v)
		h.Stars = &n
	}
	if h.Stars == nil {
		if m := numRe.FindString(str(r, "rating")); m != "" {
			n, _ := strconv.Atoi(m)
			h.Stars = &n
		}
	}

	// Price: price_from_eur number, or "180 USD" string.
	if v, ok := r["price_from_eur"].(float64); ok {
		h.PriceFrom = &Price{v, "EUR", v}
	} else if s := str(r, "price_from"); s != "" {
		if m := priceRe.FindStringSubmatch(s); m != nil {
			amt, _ := strconv.ParseFloat(m[1], 64)
			cur := strings.ToUpper(m[2])
			if rate, ok := toEUR[cur]; ok {
				h.PriceFrom = &Price{amt, cur, math.Round(amt*rate*100) / 100}
			} else {
				h.Notes = append(h.Notes, fmt.Sprintf("no EUR rate for %s", cur))
			}
		} else {
			h.Notes = append(h.Notes, fmt.Sprintf("unparsed price %q", s))
		}
	}

	// Description: strip HTML, unescape entities, collapse whitespace.
	desc := html.UnescapeString(tagRe.ReplaceAllString(str(r, "description"), " "))
	h.Description = strings.TrimSpace(spaceRe.ReplaceAllString(desc, " "))

	// Amenities: CSV string or array, mapped to canonical names.
	var raw []string
	switch v := r["amenities"].(type) {
	case string:
		raw = strings.Split(v, ",")
	}
	if f, ok := r["features"].([]any); ok {
		for _, x := range f {
			if s, ok := x.(string); ok {
				raw = append(raw, s)
			}
		}
	}
	seen := map[string]bool{}
	for _, a := range raw {
		key := strings.ToLower(strings.TrimSpace(a))
		if poolsRe.MatchString(key) {
			key = "pool"
		}
		canon, ok := amenityAliases[key]
		if !ok {
			canon = strings.ReplaceAll(key, " ", "_")
		}
		if canon != "" && !seen[canon] {
			seen[canon] = true
			h.Amenities = append(h.Amenities, canon)
		}
	}

	// Reviews: sources only give us free text today, so every field but
	// Text is left null (provisioned, not derivable yet — see DECISIONS.md).
	if rs, ok := r["review_snippets"].([]any); ok {
		for _, x := range rs {
			if s, ok := x.(string); ok {
				h.Reviews = append(h.Reviews, Review{Text: s})
			}
		}
	}
	h.LastSeen = str(r, "last_seen")
	return h
}

func haversineKm(a, b Coords) float64 {
	const R = 6371.0
	rad := math.Pi / 180
	dLat := (b.Lat - a.Lat) * rad
	dLng := (b.Lng - a.Lng) * rad
	x := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(a.Lat*rad)*math.Cos(b.Lat*rad)*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * R * math.Asin(math.Sqrt(x))
}

func main() {
	path := "hotels.json"
	if len(os.Args) > 1 {
		path = os.Args[1]
	}
	data, err := os.ReadFile(path)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	var records []map[string]any
	if err := json.Unmarshal(data, &records); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	hotels := make([]Hotel, 0, len(records))
	for _, r := range records {
		hotels = append(hotels, normalise(r))
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", "  ")
	if err := enc.Encode(hotels); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
