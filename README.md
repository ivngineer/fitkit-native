# Fitkit

Fitkit turns Pinterest saves into a shopping list. Save an outfit on Pinterest, open Fitkit, tap the pin, and get each piece (jacket, boots, bag) with store listings and prices.

```
ios/      SwiftUI iPhone and iPad app (iOS 18+)
server/   Go API server: accounts, Pinterest import, analysis pipeline, provider keys
```

The app only talks to the Fitkit server. Scraping, third-party API keys and accounts all stay on the server.

## Running locally

### 1. Server

Requires Go 1.26+. Data goes in SQLite under `server/data/`.

```sh
cd server
cp .env.example .env              # fill in keys, or leave them empty and use demo mode
./run.sh                          # loads .env, listens on :8080
```

What works depends on which keys are set:

| Keys | Tapping a pin shows |
|---|---|
| none | "not set up" message (sign-up, onboarding and import still work) |
| `GEMINI_API_KEY` only | real detected items, each with Google Shopping, Amazon and Nordstrom search links (no prices) |
| Gemini + imgbb + any one of `SEARCHAPI_API_KEY`, `SCRAPINGDOG_API_KEY`, `SERPAPI_API_KEY` | real detected items with visually matched listings and prices |
| `FITKIT_DEMO_ANALYSIS=1` | placeholder items, labeled "Demo results" |

### 2. iOS app

Open `ios/Fitkit.xcodeproj` in Xcode 26 or later and run the **Fitkit** scheme on a simulator. Debug builds point to `http://localhost:8080`. You can change this in the Account screen under Developer, or through the `FITKIT_API_BASE_URL` build setting. Release builds use `https://api.fitkit.app`, which is a placeholder.

On a real device, `localhost` is the phone itself, so point the app at your Mac's LAN address instead. From the command line that's one build setting override:

```sh
xcodebuild build -project Fitkit.xcodeproj -scheme Fitkit \
  -destination 'platform=iOS,id=<device UDID>' -allowProvisioningUpdates \
  FITKIT_API_BASE_URL='http:/$()/192.168.1.135:8080'   # your Mac's LAN address

xcrun devicectl device install app --device <device UDID> \
  ~/Library/Developer/Xcode/DerivedData/Fitkit-*/Build/Products/Debug-iphoneos/Fitkit.app
xcrun devicectl device process launch --device <device UDID> app.fitkit.Fitkit
```

`ios/scripts/deploy-device.sh` does all three steps, filling in the Mac's LAN address and the paired iPhone (pass a UDID, or set `FITKIT_DEVICE`, when there's more than one). `xcrun devicectl list devices` prints the UDIDs of paired devices, network ones included. The `$()` in the URL keeps Xcode from reading `//` as a comment.

### Offline browsing

Once someone has signed in on a device, the app keeps a copy of their account, pins, cart, each pin's pieces and shopping links, and the images it shows, under `Library/Application Support/Offline` in the app's container (left out of backups). While the server is reachable, a background sync copies the whole library after the grid loads. When the server can't be reached, the app opens straight to that copy, shows an offline banner, and turns off anything that needs the server; it checks back with backoff and when the app returns to the foreground. **Account → Offline → Clear Local Data** deletes the copy without touching the account; signing out deletes all of it.

### Resetting the data

`server/reset-data.sh` deletes the SQLite database and the cached images under `FITKIT_DATA_DIR`, leaving a clean server for a fresh demo. It refuses to run while a server is listening on `FITKIT_ADDR`, asks before deleting, and takes `--yes` to skip the prompt and `--keep-media` to keep the image cache.

```sh
cd server && ./reset-data.sh
```

## Credits

The Fitkit wordmark is set in [Newsreader](https://fonts.google.com/specimen/Newsreader), bundled under the SIL Open Font License (`ios/Fitkit/Resources/OFL.txt`).

## Tests

```sh
cd server && go test -race ./...

cd ios && xcodebuild test -project Fitkit.xcodeproj -scheme Fitkit \
  -destination 'platform=iOS Simulator,name=iPhone 18 Pro'
```

`FitkitUITests` runs end to end: sign-up, onboarding, a real Pinterest import, pin analysis and removing a pin. It needs a local server, and skips itself if it can't reach one:

```sh
FITKIT_DEMO_ANALYSIS=1 FITKIT_IMPORT_PIN_LIMIT=24 go run ./cmd/fitkitd
```

Demo mode keeps the run free and predictable. Against real providers, import a profile whose pins actually contain clothes, or the analysis step finds nothing to shop. `xcodebuild` only forwards `TEST_RUNNER_`-prefixed variables to the test process:

```sh
TEST_RUNNER_FITKIT_UITEST_PINTEREST=nordstrom \
TEST_RUNNER_FITKIT_UITEST_SERVER=http://localhost:8081 xcodebuild test ...
```

`FITKIT_UITEST_SERVER` points both the health check and the app at another server, so a demo server on a spare port can run the suite while the main one keeps its live provider keys.

## How it works

### Onboarding
After sign-up there's a short optional wizard. It never blocks using the app, and each step saves as soon as it's answered:
1. **Referral source**: `PUT /v1/me/referral-source`
2. **Pinterest handle**: `PUT /v1/me/pinterest`. Accepts `jane`, `@jane` or any `pinterest.*/jane/...` profile URL, and rejects pin and search links.

### Saved pins import (`internal/importer`, `internal/pinterest`)
1. **Kickoff**: `POST /v1/imports` creates a job, or returns the one already running. The app polls `GET /v1/imports/{id}` for discovered, imported, reused and failed counts, and shows new pins as they arrive.
2. **Discovery**: walks the public saves feed through Pinterest's web resource endpoints. If that comes back empty, it walks each board instead. Results are deduped and capped at `FITKIT_IMPORT_PIN_LIMIT`. Rate limits and upstream errors are retried with backoff and marked `retryable`. Private profiles, profiles with no saves, and unknown handles are terminal.
3. **Per-pin ingestion**: pins are deduped by Pinterest pin ID, so a pin someone else already imported is reused without downloading it again. Otherwise the server downloads the image, records its dimensions, stores the pin under the synthetic `Pinterest` author (title comes from the pin, falling back to the board name), queues an `embedding_jobs` row for feed ranking, and marks the pin saved for the user.
4. **Completion**: the job is marked `done` or `failed` with final counts. Jobs interrupted by a restart pick up again on the next start.

### Pin analysis (`internal/analyze`)
`POST /v1/pins/{id}/analysis` starts a background run. Results are cached per pin, so everyone who saved that pin shares one analysis. The app polls `GET` on the same path.
1. **Detect**: Gemini returns items with `box_2d` boxes (structured JSON output). When the model is overloaded or missing from the key's catalogue, the run retries and then falls through `GEMINI_FALLBACK_MODELS` — free keys get 503s from the newest model regularly.
2. **Crop**: each box, padded, is cut out as its own JPEG.
3. **Host**: each crop is uploaded to imgbb for a public URL (expires after 24 hours).
4. **Search**: Google Lens-style visual search through SearchApi.io, Scrapingdog or SerpApi, normalized to one listing shape, with priced results first. Every provider that has a key is tried in turn (starting with `LENS_PROVIDER`), so one provider's outage or exhausted quota falls through to the next. Pinterest links are dropped — that's where the pin came from, not somewhere to buy it — so the next match takes the top spot.
5. **Assemble**: pin, then items, then listings, returned as a single JSON result.

Detections are collapsed to one per kind of garment before any crop is uploaded, so a look with a jacket and jeans lists one jacket and one pair of jeans even when the model boxes the same piece twice; the biggest box wins. Catch-all categories (accessory, jewelry, other) key on the label instead, so a belt and a watch keep their own rows. The app shows the best match for each piece, titled with the piece rather than the store's product name, and keeps the store, price and link.

One item failing doesn't sink the others; its error is reported on that item.

Analysis only ever runs when someone opens a pin, never in batch during an import: one run costs a Gemini call plus a visual search per detected item. When detection finds nothing wearable, the run ends as `no_items` and the app offers to remove the pin from the grid.

### Removing pins
`PUT /v1/pins/{id}/hidden` hides a pin from the owner's grid; `DELETE` puts it back. The pin, its image and its analysis all stay on the server, and the save keeps its `hidden_at` mark, so a later re-import doesn't resurrect it.

In the app it's a destructive quick action on every grid pin (long press), and the same action is offered inside a pin's popup — both in its menu and as the main button when detection finds nothing wearable. The first two removals show a dismissable note that the pin is still in the person's Pinterest account.

### Cart
`PUT /v1/cart/{id}` sets a saved pin aside to buy, `DELETE` takes it back out, and `GET /v1/cart` lists the cart newest first in the same shape as `GET /v1/pins`. Hiding a pin from the grid leaves it in the cart, since adding it was a buying decision. Checkout itself (one-tap buy and ship) isn't built yet; the cart is where it will hang off.

In the app the cart lives between the sync and account buttons in the header, badged with its count, and opens as a half-height sheet that drags up to full screen. Tapping a row there opens the same breakdown sheet the grid opens, and removing a pin from Fitkit there also takes it out of the cart. "Shop This Look" carries a full-width Buy Outfit button at the bottom; it's a placeholder until checkout exists.

## API

| Method | Path | |
|---|---|---|
| POST | `/v1/auth/signup`, `/v1/auth/login` | `{email, password}` → `{token, user}` |
| POST | `/v1/auth/logout` | |
| GET / DELETE | `/v1/me` | account; delete removes all user data |
| PUT | `/v1/me/referral-source` | `{source}` |
| PUT | `/v1/me/pinterest` | `{handle}` |
| POST | `/v1/imports` | start or reuse an import |
| GET | `/v1/imports/latest`, `/v1/imports/{id}` | progress |
| GET | `/v1/pins?cursor=&limit=` | saved pins, newest first |
| PUT / DELETE | `/v1/pins/{id}/hidden` | hide a pin from the grid, or restore it |
| GET | `/v1/cart` | pins set aside to buy, newest first |
| PUT / DELETE | `/v1/cart/{id}` | add a saved pin to the cart, or take it out |
| GET / POST | `/v1/pins/{id}/analysis` | status or start (`?force=true` re-runs) |
| GET | `/media/...` | pin images and demo crops |

Authenticated routes take `Authorization: Bearer <token>`. Errors come back as `{"error": {"code", "message"}}`.

## TODO

- **Merchant pages block our scraper.** Most sites we follow from search results answer with a bare HTML "Forbidden" page or a Cloudflare challenge wall instead of the product page, so prices, stock and images fall back to whatever the search provider returned. Needs a fix: realistic headers and a session per host, a headless or proxied fetch for the sites that challenge every request, and a per-host record of what works so we stop paying for fetches that never succeed.
