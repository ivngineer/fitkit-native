# Company-less checkout MVP plan

Goal: get as close to "tap the outfit, it arrives at my door" as possible **without a registered company**. Fitkit does not take payments and is not the seller. The user pays each store directly with their own card, Apple Pay or Shop Pay. Fitkit's job is to remove every step it can: pick the right product and size, build a prefilled cart per store, prefill the shipping address, and track what was ordered.

This is the bridge to the full plan in [plan.md](plan.md). The app flow built here ("Get this look" to checkout to order tracking) stays the same when Fitkit later becomes the merchant of record; only the step behind the "Checkout" button changes.

Status: not implemented. Research date 18 September 2026.

## 1. Constraints and ground rules

- **No money through Fitkit.** No Stripe or PayPal account taking payments for goods, and no personal or borrowed account used for that purpose. Those accounts get frozen with the money held, and it breaks the payment providers' terms. Revenue comes from affiliate commissions, which individuals can earn.
- **No card data on our servers or in our app.** Payment happens on the store's own checkout (in an in-app Safari sheet, so Apple Pay, Shop Pay and iCloud Keychain autofill work).
- **No automated ordering on stores whose terms forbid it** and no bot-detection workarounds. The user always presses "Pay" themselves.
- **Shipping to the US, EU and Ukraine** relies on each store's own shipping, plus the user's personal NP Shopping (or Meest / Ukraine Express) forwarding address for stores that do not ship to Ukraine.

## 2. User experience

1. On Pin Detail, the user taps **Get this look**.
2. **Pick pieces**: every detected item is listed with its best buyable listing. Items can be unticked or switched to another listing ("Other options").
3. **Pick sizes**: sizes and colors come from the store where we can read them (Shopify, some others). Where we cannot, the row says "Choose size at checkout". Sizes the user picked before are remembered per category (tops, bottoms, shoes).
4. **Deliver to**: pick a saved address. For Ukraine, the address sheet explains the two options: store ships to Ukraine directly, or ship to your NP Shopping address.
5. **Checkout plan**: items grouped by store, with a subtotal per store, a total, and one row per store:
   - "Checkout at Everlane (2 items)" opens a prefilled Shopify checkout with Shop Pay.
   - "Checkout at Amazon (1 item)" opens an Amazon cart with the item already added.
   - "Open at Shein (1 item, size M)" opens the product page; the user adds it to the cart.
6. After each store sheet closes, the app asks "Did you place the order?" with **Yes, ordered** / **Not yet**. On yes, the user can optionally paste the order number or tracking number (or share the confirmation email later).
7. **Orders** screen: every "look" the user checked out, per-store status (not ordered, ordered, shipped, delivered), and tracking once a tracking number is known.

Most outfits are 1–3 stores, so this is 1–3 checkouts, each one or two taps with Apple Pay or Shop Pay.

## 3. How each store is handled

Each listing URL is classified into a **checkout method**, computed on the server and stored with the listing.

| Method | Stores | What the button opens | Prefills |
|---|---|---|---|
| `shopify_cart` | Any Shopify store (detected by fetching `/products/<handle>.js`) | `https://<shop>/cart/<variant>:<qty>,<variant>:<qty>?checkout[email]=…&checkout[shipping_address][…]=…&payment=shop_pay` (goes straight to checkout) | Items, sizes, quantities, email, shipping address |
| `amazon_cart` | amazon.com and EU Amazon sites | `https://www.amazon.<tld>/gp/aws/cart/add.html?ASIN.1=…&Quantity.1=1&…&AssociateTag=…` | Items (only when the URL's ASIN is already the right size/color), affiliate tag |
| `affiliate_link` | AliExpress, Shein, H&M, Zara, Etsy, DHgate and anything with an affiliate program | Product page through the affiliate network's deep link | Nothing; size shown in the app so the user picks the right one |
| `product_page` | Everything else | Product page as is | Nothing |

Notes:
- Shopify cart permalinks accept several variants at once, go straight to checkout by default, accept `checkout[email]` and `checkout[shipping_address][first_name|last_name|address1|address2|city|province|country|zip]`, and `payment=shop_pay` sends the buyer to Shop Pay. This is the closest to one-tap checkout available without a company.
- Amazon's add-to-cart form takes multiple `ASIN.n` / `Quantity.n` pairs and requires an Associates tag. Amazon clothing listings usually use a parent ASIN with size children; if the URL is a parent ASIN, fall back to the product page, because picking the child ASIN needs the Product Advertising API, which Amazon only opens after an account has qualifying sales.
- Shipping address prefill is only possible for Shopify. For the others the user's saved address is shown on the checkout plan with a Copy button.

## 4. Shipping by region

| User's country | Store ships there | Store does not ship there |
|---|---|---|
| US | Normal store shipping | Rare for US-facing listings; hide those listings |
| EU | Prefer EU storefronts (Amazon.de/.fr/.it/.es/.pl, Shein EU, H&M/Zara EU, EU Shopify stores). Visual search should favor listings in the user's region. | Show a warning with the expected €3 per item EU duty plus VAT for non-EU stores |
| Ukraine | AliExpress (direct, with Nova Poshta delivery), Shopify stores that list Ukraine, some Shein items | Ship to the user's **NP Shopping** personal address in the US or Poland (or Meest / Ukraine Express). The user pays forwarding in the NP app on pickup. |

For Ukraine, the address book supports two address kinds:
1. **Home or NP branch in Ukraine** (for stores that ship directly).
2. **Forwarder address**: the user's personal NP Shopping warehouse address (country, street, suite ID). The app has a short guide: register at npshopping.com, copy your US and Poland addresses into Fitkit. We do not create NP accounts for users.

For each store the app picks the right address automatically: direct address if the store ships to Ukraine, otherwise the forwarder address in the warehouse country that matches the store (US store to the US warehouse, EU store to the Poland warehouse). The checkout plan shows a line such as "Ships to your NP Shopping address in Poland, then about 5–10 days to your branch. Nova Poshta delivery fee paid on pickup, from $3 per 0.5 kg." Parcels over €150 are flagged: 10% duty plus 20% VAT on the excess.

Whether a store ships to a country is not always knowable in advance. Rules, in order: known per-merchant table (config file, maintained by hand for the top 30 merchants in our listings), Shopify stores' shipping countries (from the storefront's `/meta.json` or checkout once opened), otherwise "unknown" and default to the forwarder address for Ukraine.

## 5. Revenue without a company

- **Amazon Associates** (US, and the EU programs): individuals can join. The tag goes into cart and product links.
- **AliExpress Portals** affiliate program.
- **A sub-affiliate network** (Skimlinks or Sovrn Commerce) that rewrites any merchant URL into an affiliate link and covers thousands of stores (Shein, H&M, many Shopify brands) with one account. Individuals can join; payouts go to a personal bank account or PayPal. They typically keep a share of the commission.
- Affiliate income is personal income for tax purposes until the company exists. Keep a record; this is not a reason to delay.

Application approval may need the app to be public (TestFlight link or landing page). Apply early; links work without tags until approved.

Disclosure: the App Store and affiliate programs require telling users that links may earn Fitkit a commission. Add a one-line note on the checkout plan screen and in Settings.

## 6. Implementation

### 6.1 Server (Go)

New package `server/internal/shop`:

- `classify.go`: given a listing URL, returns merchant key, checkout method, and normalized product URL. Unwraps Google/redirect URLs first (Lens results sometimes point at redirectors).
- `shopify.go`: detects Shopify and loads variants by fetching `https://<host>/products/<handle>.js` (public JSON: variants, option names like Size/Color, availability, price). Cached per URL for 6 hours in SQLite. Builds cart permalinks with prefilled email and address.
- `amazon.go`: extracts ASIN from `/dp/<ASIN>` and `/gp/product/<ASIN>`; builds add-to-cart and tagged product URLs for the right Amazon domain.
- `affiliate.go`: wraps URLs for AliExpress and the sub-affiliate network. Tags come from config; with no tag set, links go out untouched.
- `shipping.go`: per-merchant shipping table (embedded JSON: merchant key to countries shipped, warehouse country for forwarding) and the address-choice rules from section 4.
- `plan.go`: builds a checkout plan from the chosen items: groups by merchant, picks method and address, computes subtotals per currency, warnings (customs, unknown shipping, missing size).

Config (`server/internal/config`, `.env.example`): `AMAZON_ASSOCIATE_TAG_US`, `AMAZON_ASSOCIATE_TAG_DE` (and other EU sites as approved), `ALIEXPRESS_AFFILIATE_KEY`, `SKIMLINKS_PUBLISHER_ID` (or Sovrn). All optional.

Schema additions (`server/internal/store/store.go`):

```sql
CREATE TABLE IF NOT EXISTS addresses (
	id          TEXT PRIMARY KEY,
	user_id     TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	kind        TEXT NOT NULL,            -- 'home' | 'np_branch' | 'forwarder'
	label       TEXT NOT NULL DEFAULT '',
	country     TEXT NOT NULL,            -- ISO 3166-1 alpha-2 of the delivery point
	forwarder   TEXT NOT NULL DEFAULT '', -- 'np_shopping' | 'meest' | 'ukraine_express'
	final_country TEXT NOT NULL,          -- where the user lives, e.g. UA for a forwarder address in PL
	fields_json TEXT NOT NULL,            -- name, lines, city, region, zip, phone, suite id, NP branch ref
	is_default  INTEGER NOT NULL DEFAULT 0,
	created_at  INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS size_profiles (
	user_id  TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	category TEXT NOT NULL,               -- matches DetectedItem.category
	size     TEXT NOT NULL,
	PRIMARY KEY (user_id, category)
);

CREATE TABLE IF NOT EXISTS looks (           -- one "Get this look" checkout
	id         TEXT PRIMARY KEY,
	user_id    TEXT NOT NULL REFERENCES users(id) ON DELETE CASCADE,
	pin_id     TEXT NOT NULL REFERENCES pins(id) ON DELETE CASCADE,
	plan_json  TEXT NOT NULL,             -- the checkout plan as shown
	created_at INTEGER NOT NULL
);

CREATE TABLE IF NOT EXISTS look_stores (     -- one row per store in a look
	look_id       TEXT NOT NULL REFERENCES looks(id) ON DELETE CASCADE,
	merchant      TEXT NOT NULL,
	status        TEXT NOT NULL,          -- 'pending' | 'ordered' | 'shipped' | 'delivered' | 'skipped'
	order_ref     TEXT NOT NULL DEFAULT '',
	tracking_no   TEXT NOT NULL DEFAULT '',
	carrier       TEXT NOT NULL DEFAULT '',
	updated_at    INTEGER NOT NULL,
	PRIMARY KEY (look_id, merchant)
);
```

Endpoints (`server/internal/api/api.go`):

| Method and path | Purpose |
|---|---|
| `GET/POST /v1/addresses`, `PUT/DELETE /v1/addresses/{id}` | Address book |
| `GET/PUT /v1/sizes` | Size profile |
| `POST /v1/listings/options` | Body: listing URLs. Returns checkout method and, for Shopify, variants and options. |
| `POST /v1/looks` | Body: pin id, chosen items (listing URL, variant id or size text, quantity), address id. Builds and saves a checkout plan; returns it with one ready-to-open URL per store. |
| `GET /v1/looks`, `GET /v1/looks/{id}` | Orders screen |
| `PUT /v1/looks/{id}/stores/{merchant}` | Mark ordered or skipped, save order number and tracking number |

Tests alongside existing ones (`api_test.go`, new `shop/*_test.go`): URL classification table, Shopify variant parsing from a recorded `.js` fixture, permalink building with address encoding, Amazon ASIN extraction per domain, address choice per country and store. No live network calls in tests.

### 6.2 iOS (SwiftUI)

- `Features/Look/` (new):
  - `LookBuilderView` and `LookBuilderModel`: steps 2–4 from section 2 (pieces, sizes, address) as one scrolling screen, not a wizard.
  - `CheckoutPlanView`: stores, subtotals, warnings, "Checkout at …" buttons. Reuses the existing `SafariView` full-screen cover from `PinDetailView.swift`. On dismiss, shows the "Did you place the order?" prompt.
- `Features/Orders/`: `OrdersView` list and `LookOrderView` detail with per-store status, order number and tracking entry, tracking link (carrier page).
- `Features/Settings/`: Addresses and Sizes screens; the NP Shopping guide.
- `Features/PinDetail/PinDetailView.swift`: a prominent **Get this look** button once analysis is loaded.
- `Models/Models.swift`: `Address`, `SizeProfile`, `ListingOptions`, `CheckoutPlan`, `Look`, `LookStore`.
- `Networking/APIClient.swift`: methods for the endpoints above. The current `request` helper takes `[String: String]` bodies; add an `Encodable` body variant for nested payloads.
- The existing cart (pins set aside) stays as a wishlist. "Get this look" from the cart sheet works on several pins at once in a later iteration.

UI tests: extend `FitkitUITests` with a demo-mode flow (demo analysis returns listings with fake Shopify-style URLs; the server returns a plan without network calls when `FITKIT_DEMO_ANALYSIS=1`).

### 6.3 Visual search tweaks that help checkout

- Pass the user's country to the Lens provider (`gl`/`location` parameter) so listings come from stores that ship to them.
- Rank listings: in stock first, then checkout method (`shopify_cart` > `amazon_cart` > `affiliate_link` > `product_page`), then visual match order. Keep the provider's order within each tier.

## 7. Milestones

1. **Classification and options** (server): `shop` package, `/v1/listings/options`, tests. Check on 20–30 real listings from the test pins what share are Shopify, Amazon, others.
2. **Addresses and sizes** (server + iOS settings screens).
3. **Checkout plan** (server `/v1/looks`, iOS builder and plan screens), Shopify and Amazon cart links, product-page fallback. At this point the MVP is usable end to end.
4. **Orders tracking** (mark ordered, order and tracking numbers, Orders screen).
5. **Affiliate tags** once programs approve; disclosure copy.
6. **Region-aware search and ranking**.

Suggested order of test runs, keeping live provider calls sparse (free-tier keys): classification fixtures first, then a single live analysis per milestone on the test device.

## 8. Known limits (accepted for the MVP)

- One checkout per store, not one for the whole outfit.
- Address and size prefill only on Shopify; elsewhere the user picks the size and address in the store's checkout.
- Ukrainian users on the forwarder route pay Nova Poshta separately and have no returns to foreign stores.
- Order status depends on the user marking it; there is no automatic confirmation.
- Affiliate commission is not guaranteed (some programs exclude sale items, some stores are not in any program).

## 9. Upgrade path to the full plan

When a company exists (see [plan.md](plan.md)):
- Add Stripe payment to `CheckoutPlanView` as one "Pay for everything" button; stores the server can buy from automatically (Rye, AliExpress DS API, Shopify agent checkout) move behind it, others stay as per-store checkout until the concierge queue exists.
- `looks` and `look_stores` become `orders` and `supplier_orders`; addresses already carry the forwarder information needed for hub routing.
- Optional earlier step for US users only: Rye is currently itself the merchant of record, so a true single checkout for US addresses may be possible without a Fitkit company. Ask Rye whether they accept individual developers, and what the $149/month developer plan requires.
