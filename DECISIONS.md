# Decisions

## Chosen feature: unified hotel schema

A Go script that maps every source record (partner-feed-a, partner-feed-b, scrape-booking-sites) onto one schema:

- **Name**: taken from `hotel_name` or `name`.
- **Location**: `city` plus an ISO `country_code` (parsed from `location` strings like "Rimini, Italien"). Coordinates are optional, and the script checks whether they fall near the stated city.
- **Stars**: an integer or null, whether the source says `4`, `"3 stars"` or nothing.
- **Price**: an amount plus currency, with an EUR value added (`89` → 89 EUR, `"180 USD"` → converted using a fixed rate).
- **Description**: plain text, with HTML tags stripped and whitespace collapsed.
- **Amenities**: a list of canonical names (`free WiFi` → `wifi`, `swimming pool` → `pool`), whether the source sends a CSV string, an array or null.

**Audience:** an AI travel agent that queries hotel data, plus whoever maintains the feed ingestion.
**Value:** the agent can filter and compare hotels on one set of fields ("4-star in Italy under €100 with a pool") without handling each source's quirks.

## Alternatives considered

- **Duplicate detection / merging** (the two Rimini records). High value, but fuzzy matching is only reliable once records share a schema. It's a natural next step.
- **Translating and extracting facts from descriptions** (Alpenhof's German text). Needs an LLM or translation dependency, which is too much for one hour.
- **Freshness scoring by `last_seen`.** Only one record has a timestamp, so the sample can't demonstrate it.

## How we'll know it works

Running `go run .` on `hotels.json`:
1. All 5 records come out in the same shape, with no fields lost silently. Unparseable values show up as null plus a note.
2. Stars are `4`, `3`, `null`, `null`, `null`.
3. Prices are 89 EUR and 180 USD (with an EUR equivalent). Missing prices are null.
4. Each record has a city and a 2-letter country code. City Lodge Berlin gets a note that its coordinates don't match Berlin.
5. No description contains `<` or `>` tags.
