# Fitkit

Fitkit turns Pinterest saves into a shopping list. Save an outfit on Pinterest, open Fitkit, tap the pin, and get each piece (jacket, boots, bag) with store listings and prices.

```
ios/              SwiftUI iPhone and iPad app (iOS 18+)
server/           Go API server: accounts, Pinterest import, analysis pipeline, provider keys,
                  the employee workspace API for the desktop app buyers use, and the admin API
agent-workspace/  React desktop app for Fitkit's buyers (served by the server or run with Vite)
admin-panel/      React admin panel: employee accounts and analytics (served at /admin)
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

### 2. Desktop app (buyers)

Requires Node 20+. Build it once and let the server serve it:

```sh
cd agent-workspace && npm install && npm run build
cd ../server
FITKIT_WEB_DIR=../agent-workspace/dist FITKIT_BOOTSTRAP_ADMIN_EMAIL=you@fitkit.space FITKIT_BOOTSTRAP_ADMIN_PASSWORD=change-me-please ./run.sh
```

Open http://localhost:8080 and sign in with the bootstrap account. `ALLOW_SEED=true go run ./cmd/fitkitd seed-workspace` loads demo requests (`anna@fitkit.space` / `fitkit-demo`); it wipes workspace data. While working on the front-end, run `npm run dev` in `agent-workspace/` instead (http://localhost:5173, proxying `/api` and `/media` to :8080).

### 3. Admin panel

Admins manage employee accounts and see per-employee analytics at `/admin`. There is no sign-up: the logins are fixed in `FITKIT_ADMINS` as `login:password` pairs separated by commas, and removing a login from it closes that admin's access on the next request.

```sh
cd admin-panel && npm install && npm run build
cd ../server
FITKIT_ADMINS='kostya:a-long-password,ivan:another-long-one' FITKIT_ADMIN_WEB_DIR=../admin-panel/dist ./run.sh
```

Open http://localhost:8080/admin. For front-end work run `npm run dev` in `admin-panel/` (http://localhost:5174/admin/, proxying `/admin/api` to :8080).

### 4. iOS app

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
`PUT /v1/cart/{id}` sets a saved pin aside to buy, `DELETE` takes it back out, and `GET /v1/cart` lists the cart newest first in the same shape as `GET /v1/pins`. Hiding a pin from the grid leaves it in the cart, since adding it was a buying decision.

In the app the cart lives between the sync and account buttons in the header, badged with its count, and opens as a half-height sheet that drags up to full screen. Tapping a row there opens the same breakdown sheet the grid opens, and removing a pin from Fitkit there also takes it out of the cart. "Shop This Look" carries a full-width Get This Look button at the bottom that starts checkout.

### Checkout (`internal/shop`)
Fitkit takes no payment and isn't the seller; see [mvp-plan.md](mvp-plan.md). The server turns the pieces a user picks into one checkout link per store, and the user pays each store directly.

- **Classification**: every listing URL is unwrapped from redirectors and classified as `shopify_cart` (confirmed by loading the store's public `/products/<handle>.js`), `amazon_cart`, `affiliate_link` or `product_page`. Shopify product and `/meta.json` lookups are cached in SQLite for 6 hours, and only go to public HTTPS addresses.
- **Plan** (`POST /v1/looks`): the request only names pieces and listings; each must exist in the pin's stored analysis, so links and prices always come from the analysis or the store, never the app. Shopify items need a variant (sizes must be in stock) and go into a cart permalink with the user's email and shipping address filled in. Amazon items go into an add-to-cart link when an Associates tag is set for that site and the URL already points at one size (`psc=1`) or the piece isn't sized. Everything else opens its product page, tagged when an affiliate program is configured.
- **Shipping**: addresses are `home`, `np_branch` (Nova Poshta, Ukraine) or `forwarder` (NP Shopping, Meest, Ukraine Express). For a Ukrainian address the server checks whether the store ships there (hand-kept table in `internal/shop/merchants.json`, then the Shopify store's own list) and otherwise routes the order to the user's forwarder warehouse: US stores to the US one, others to Poland. Plans warn about EU duty from non-EU stores and Ukraine's €150 threshold.
- **Orders**: the user marks each store ordered, shipped, delivered or skipped and can add an order and tracking number; known carriers get a tracking link.
- **Affiliate tags** come from `AMAZON_ASSOCIATE_TAG_<SITE>`, `ALIEXPRESS_AFFILIATE_KEY` and `SKIMLINKS_PUBLISHER_ID`; all optional. In demo mode, listings from `*.fitkit.example` act as a Shopify store without any network calls.

### Buying through Fitkit (`internal/api/orders.go`)
“Купити” in the app sends `POST /v1/orders`: the pin, the user's name or nickname, and the pieces (`itemId` + `listingUrl`, optional size, quantity and comment). As with looks, each piece must exist in the pin's stored analysis, so names, stores, links, photos (local crops, else the listing thumbnail) and prices come from the analysis, never the app. A missing size falls back to the user's saved size for that category. Prices in hryvnias are kept in kopiyky; other currencies are kept as the listing's price text (`listingPrice`) for the buyer, and the order total is set only when every price is in hryvnias. An optional `idempotencyKey` makes a retried tap return the same order.

The order is stored as a new request in the employee workspace (client `user:<id>`, the pin image as the order photo) and the response carries its number (`#1000`, `#1001`, …) and the status the client sees. `GET /v1/orders` lists the user's orders with that status only — buyers, notes and history stay internal. Payment and delivery aren't part of this yet.

In the app, `POST /v1/orders/{id}/complaints` reports a problem with an order. The complaint is filed against the employee who owns the order and only feeds the admin analytics for now.

### Employee workspace (`internal/workspace`)
The desktop app buyers use (React, `agent-workspace/`) talks to the same server under `/api`, with its own employee accounts and cookie sessions, and the server can serve its build (`FITKIT_WEB_DIR`). Tables live in the same SQLite file.

- **Orders**: queue of new requests (`take`, `take-next`; atomic), item checks, store price, rules (a rise up to 5% is covered, above that the client confirms, sold out is refunded; unanswered confirmations are refunded after 24 h by a background job), purchase guard, cancel with refund, problem flag, team notes, history.
- **Parcels**: one per store after purchase, tracking numbers (manual or from the aggregator webhook), warehouse receipt, the Nova Poshta shipment once every parcel is at the warehouse; stalled parcels get flagged.
- **Support tickets** answered as Fitkit Support.
- **Live updates** over Server-Sent Events (`/api/events`).
- **Webhooks** (`/webhooks/client-app/*`, `/webhooks/tracking`) are HMAC-signed (`X-Fitkit-Signature: sha256=<hex>`).
- Nova Poshta, the tracking aggregator, refunds and client notices are interfaces with logging fakes for now (`internal/workspace/integrations`).

### Admin panel (`admin-panel/`, `internal/workspace/httpapi/admin.go`)
- Admin sign-in checks `FITKIT_ADMINS`; sessions are stored hashed in `admin_sessions` (12 h, cookie `fitkit_admin` on `/admin`, or Bearer), with 10 attempts per 15 minutes.
- Employees: add, edit name / email (the sign-in login) / role, toggle team lead, set a new password (signs them out everywhere), delete. Deleting is soft: access closes, their active orders go back to the queue, history and stats stay, and the email can be reused.
- Analytics: a completed order is one in `delivered`, counted for its owner. The page shows totals, the top performer, a leaderboard by completed orders and the employees with the most complaints.

Roles are `buyer`, `warehouse` and `support`; any of them can also be a **team lead**, which for now only adds the right to hand an order over to another employee (`POST /api/orders/{id}/assign`). Only the order's owner edits it. Employees are added in the admin panel; the first one can also come from `FITKIT_BOOTSTRAP_ADMIN_EMAIL` / `FITKIT_BOOTSTRAP_ADMIN_PASSWORD` while there are none, or from `go run ./cmd/fitkitd create-employee -name … -email … -role buyer|warehouse|support [-team-lead] -password …`. Older databases with the `lead` role are migrated to buyer + team lead. `ALLOW_SEED=true go run ./cmd/fitkitd seed-workspace` wipes the workspace tables (not app accounts or pins) and loads demo data (`anna@fitkit.space` / `fitkit-demo`).

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
| GET / POST | `/v1/addresses` | address book; the first address becomes the default |
| PUT / DELETE | `/v1/addresses/{id}` | edit or remove an address |
| GET / PUT | `/v1/sizes` | `{sizes: {category: size}}`, replaced as a whole |
| POST | `/v1/listings/options` | `{pinId, urls}` → checkout method and Shopify variants per listing of that pin |
| POST | `/v1/looks` | `{pinId, addressId?, items: [{itemId, listingUrl, variantId?, size?, quantity}]}` → look with its checkout plan |
| GET | `/v1/looks`, `/v1/looks/{id}` | orders, newest first |
| PUT | `/v1/looks/{id}/stores/{merchant}` | `{status, orderRef?, trackingNo?, carrier?}` |
| POST | `/v1/orders` | `{pinId, name, idempotencyKey?, items: [{itemId, listingUrl, size?, quantity?, comment?}]}` → order with its number |
| GET | `/v1/orders`, `/v1/orders/{id}` | the user's orders with the status they see |
| POST | `/v1/orders/{id}/complaints` | `{text}` (1–1000 chars) → `{id}`; a problem with the user's own order |
| GET | `/media/...` | pin images and demo crops |

Authenticated routes take `Authorization: Bearer <token>`. Errors come back as `{"error": {"code", "message"}}`.

The employee workspace API (`/api/...`: sign-in, orders, parcels, tickets, events; `/webhooks/...`) uses employee accounts, an HttpOnly cookie or Bearer token, and `{"error": "<message>"}` errors; amounts are in kopiyky. Amounts are in kopiyky (1 ₴ = 100); errors are `{"error": "<message in Ukrainian>"}`.

| Method & path | Body | Does |
| --- | --- | --- |
| `GET /api/me` | | Current employee |
| `GET /api/bootstrap` | | Everything the UI shows: me, employees, clients, orders, shipments, tickets |
| `GET /api/events` | | SSE stream: `event: order\|shipment\|ticket`, `data: {"type","id"}` — refetch that object |
| `GET /api/orders?status=new,in_progress&owner=me&client=…` | | Orders with items, notes, history, `money`, `clientSees`, `canPurchase` |
| `GET /api/orders/{id}` | | One order (same shape) |
| `POST /api/orders/take-next` | | Assign the oldest new request to me |
| `POST /api/orders/{id}/take` | | Take a request (409 if someone was faster) |
| `POST /api/orders/{id}/status` | `{status}` | `purchased` (all items resolved; creates store parcels), `shipped`, `delivered`, `in_progress` |
| `PATCH /api/orders/{id}/items/{itemId}` | `{action, price?}` | `check`, `sold_out`, `store_price` (+`price`), `send_confirmation`, `client_confirmed`, `cancel` (refund) |
| `PUT /api/orders/{id}/problem` | `{problem: string\|null}` | Flag or clear a problem |
| `POST /api/orders/{id}/notes` | `{text}` | Team note |
| `POST /api/orders/{id}/cancel` | | Cancel before purchase, refund what is left |
| `POST /api/orders/{id}/ship` | | Create the Nova Poshta ТТН (all store parcels at the warehouse) |
| `POST /api/orders/{id}/assign` | `{employeeId}` | Team lead only: hand the order to another employee (a new request goes in progress) |
| `GET /api/clients`, `GET /api/clients/{id}` | | Clients |
| `GET /api/shipments?order=…` | | Parcels of both legs with tracking events |
| `POST /api/shipments/{id}/tracking` | `{tracking, carrier?}` | Add a store tracking number; registered with the aggregator |
| `POST /api/shipments/{id}/arrived` | | Warehouse received the parcel |
| `GET /api/tickets?status=open` | | Support tickets with messages |
| `POST /api/tickets/{id}/messages` | `{text}` | Reply as Fitkit Support (sent to the client app) |
| `POST /api/tickets/{id}/resolve` | | Close the ticket |

#### Admin API

Admin session from `POST /admin/api/auth/login`; errors are `{"error": "<message>"}`.

| Method & path | Body | Does |
| --- | --- | --- |
| `POST /admin/api/auth/login` | `{login, password}` | `{login, token}` and the `fitkit_admin` cookie; 503 when `FITKIT_ADMINS` is empty |
| `POST /admin/api/auth/logout` | | |
| `GET /admin/api/me` | | `{login}` |
| `GET /admin/api/analytics` | | `{totals, leaderboard, mostComplaints}` |
| `GET /admin/api/employees` | | Employees with `stats` (`completed`, `inProgress`, `onTheWay`, `cancelled`, `complaints`) |
| `POST /admin/api/employees` | `{name, email, role, isTeamLead, password}` | Add (password 10+ characters; 409 if the email is taken) |
| `GET /admin/api/employees/{id}` | | One employee with stats, recent orders and complaints |
| `PATCH /admin/api/employees/{id}` | `{name, email, role, isTeamLead}` | Edit |
| `PUT /admin/api/employees/{id}/password` | `{password}` | New password; signs them out |
| `DELETE /admin/api/employees/{id}` | | Close access; active orders go back to the queue |

#### Workspace webhooks (signed)

Header `X-Fitkit-Signature: sha256=<hex HMAC-SHA256 of the raw body>`; secrets `CLIENT_APP_WEBHOOK_SECRET` and `TRACKER_WEBHOOK_SECRET`.

| Path | From | Body |
| --- | --- | --- |
| `POST /webhooks/client-app/orders` | Fitkit app | `{externalId, paid, fee, client: {externalId, name, phone, telegram?, city, branch, sizes?, stores?}, items: [{name, store, url, size, qty, price?, photo?, comment?}]}` — idempotent by `externalId` |
| `POST /webhooks/client-app/confirmations` | Fitkit app | `{orderExternalId, itemId, accept}` — answer to a price confirmation |
| `POST /webhooks/client-app/messages` | Fitkit app | `{clientExternalId, orderExternalId?, text}` — opens or continues a support ticket |
| `POST /webhooks/tracking` | Tracking aggregator adapter | `{tracking, status: in_transit\|arrived_warehouse\|delivered\|exception, title, detail?, at?}` |

Each aggregator (17TRACK, AfterShip, …) has its own payload, so a small adapter maps it to `/webhooks/tracking`.

## TODO

- **Merchant pages block our scraper.** Most sites we follow from search results answer with a bare HTML "Forbidden" page or a Cloudflare challenge wall instead of the product page, so prices, stock and images fall back to whatever the search provider returned. Needs a fix: realistic headers and a session per host, a headless or proxied fetch for the sites that challenge every request, and a per-host record of what works so we stop paying for fetches that never succeed.
