# Decisions

## Chosen feature: unified hotel schema

A Go script that maps every source record (partner-feed-a, partner-feed-b, scrape-booking-sites) onto one schema:

- **Name**: taken from `hotel_name` or `name`.
- **Location**: a single nested object (`city`, `country_code`, `coordinates`, `address`) rather than separate root-level fields. `country_code` is ISO, parsed from `location` strings like "Rimini, Italien". `coordinates` are optional, and the script checks whether they fall near the stated city. `address` follows the [schema.org `PostalAddress`](https://schema.org/PostalAddress) shape (`street_address`, `address_locality`, `address_region`, `postal_code`, `address_country`) so it's a drop-in with existing address tooling and geocoders. None of the current sources give us a street address, so this script always leaves it `null` — see "Address extraction" below.
- **Stars**: an integer or null, whether the source says `4`, `"3 stars"` or nothing.
- **Price**: an amount plus currency, with an EUR value added (`89` → 89 EUR, `"180 USD"` → converted using a fixed rate).
- **Description**: plain text, with HTML tags stripped and whitespace collapsed.
- **Amenities**: a list of canonical names (`free WiFi` → `wifi`, `swimming pool` → `pool`), whether the source sends a CSV string, an array or null.
- **Reviews**: a list of objects (`text`, `author`, `rating`, `written_at`, `guest_count`, `room_type`) instead of bare strings. Only `text` comes from the current sample; the rest are `null` — see "Review extraction" below.

**Audience:** an AI travel agent that queries hotel data, plus whoever maintains the feed ingestion.
**Value:** the agent can filter and compare hotels on one set of fields ("4-star in Italy under €100 with a pool") without handling each source's quirks.

### Address extraction (future work, not in this prototype)

None of the three sources gives a street-level address today (we only get city, a free-text "location" string, and occasional lat/lng). A precise `PostalAddress` still matters for an AI travel agent — "book me the one on Via Roma" or handing an address to a taxi/maps integration needs more than a city name. Plan:

- **Where it comes from:** a separate LLM service, called after this normaliser, given each hotel's name + city + country + description (+ coordinates when present) and asked to return a structured `PostalAddress` (or explicitly abstain if it can't determine one confidently). This is deliberately decoupled from this script — it needs a network call, a prompt, and a confidence/abstention policy, none of which belong in a one-hour deterministic prototype.
- **Why an LLM and not a geocoder API:** a reverse-geocoder (e.g. Nominatim/Google) only works when we already have trustworthy coordinates, which two of five sample records lack and one of five has *wrong* (City Lodge Berlin's coordinates are ~504 km off). An LLM can also mine the address out of free text (e.g. Alpenhof's German description), which no reverse-geocoder can do. In production these two approaches are complementary: geocode when coordinates are present and plausible, fall back to LLM extraction from text, and cross-check one against the other when both are available.
- **How it plugs in:** the LLM service would write into the same `location.address` field this script already emits as `null`, so the schema doesn't change when address extraction ships — only this one field stops being empty.
- **Confidence:** the address service should return a confidence/abstain signal rather than a guessed address, and a guessed address should never be presented to a traveller as a fact — this is the same "don't state an unverified thing confidently" concern that motivated normalising `stars`/`price`/`amenities` in the first place.

### Review extraction (future work, not in this prototype)

The only review data we have is two bare strings on one record ("pool was closed for the whole of August", "great breakfast") — no author, date, rating, guest count, or room type. Those details matter for an AI travel agent: "great breakfast" from a solo business traveller last month reads differently from the same phrase from a family two years ago, and a pool-closure complaint tied to a specific month is far more actionable than an undated one. Plan:

- **Where it comes from:** a separate LLM service, run against the actual review pages on each source site (not just the snippet we're handed), that extracts `author` (or leaves it null if anonymous/not shown), `rating`, `written_at`, `guest_count`, and `room_type` alongside the review text. Kept out of this deterministic normaliser for the same reason as address extraction: it needs network access to the source, a prompt, and a policy for missing/ambiguous fields.
- **Why extraction and not just structured review APIs:** some sources (like our `scrape-booking-sites` source here) hand back pre-summarised snippets with the surrounding metadata stripped out already; getting the metadata back means either re-fetching the original page or asking the source for richer data. Where the source does expose structured reviews, extraction is unnecessary — the plumbing should prefer the source's own fields and only fall back to LLM extraction when they're missing.
- **How it plugs in:** the service would populate `author`/`rating`/`written_at`/`guest_count`/`room_type` on the same `Review` objects this script already emits with those fields `null`, so downstream consumers don't need a schema change when it ships.
- **Confidence:** as with addresses, a field the service can't confidently extract should stay `null` rather than being guessed — an AI agent citing a fabricated reviewer or rating is worse than citing an anonymous one.

## Alternatives considered

- **Duplicate detection / merging** (the two Rimini records). High value, but fuzzy matching is only reliable once records share a schema. It's a natural next step.
- **Translating and extracting facts from descriptions** (Alpenhof's German text). Needs an LLM or translation dependency, which is too much for one hour.
- **Freshness scoring by `last_seen`.** Only one record has a timestamp, so the sample can't demonstrate it.

## How we'll know it works

Running `go run .` on `hotels.json`:
1. All 5 records come out in the same shape, with no fields lost silently. Unparseable values show up as null plus a note.
2. Stars are `4`, `3`, `null`, `null`, `null`.
3. Prices are 89 EUR and 180 USD (with an EUR equivalent). Missing prices are null.
4. Each record has one `location` object with a city and a 2-letter country code; `location.address` is `null` for all 5. City Lodge Berlin gets a note that its coordinates don't match Berlin.
5. No description contains `<` or `>` tags.
6. The one record with review data (Mare Azzuro Hotel) has two `reviews` objects with `text` set and `author`/`rating`/`written_at`/`guest_count`/`room_type` all `null`.
