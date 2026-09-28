# Checkout and shipping plan

Goal: a user taps "Get this look" on a pin, picks sizes, pays once with Apple Pay or a card, and the pieces arrive at their door in the US, the EU or Ukraine. This document covers the research behind the design, the recommended architecture, and a phased implementation plan. Nothing here is built yet.

Research date: 18 September 2026. Several findings (customs rules, provider coverage) change often; the "Verify before building" section lists what to re-check.

## 1. What the research found

### 1.1 No retailer offers a buy API to third parties, with one exception

| Retailer | Programmatic ordering | Ships directly to |
|---|---|---|
| Amazon | No official buyer API. Possible through Rye or Zinc, both of which drive Amazon's checkout for you. | US; limited "International Shopping" selection to EU and Ukraine. Clothing often blocked for Ukraine. |
| Shopify stores (a large share of indie and mid-size fashion brands) | Yes, through Shopify's agent checkout (UCP / Checkout MCP). Approval requirement dropped in Spring 2026, but only "trusted agents" can complete checkout without handing the buyer to a web page. Also covered by Rye. | Whatever each store ships to. |
| AliExpress | Yes. Official Dropshipping API on the AliExpress Open Platform (`aliexpress.ds.order.create`, tracking endpoints). Needs an approved developer app (1–2 business days). | US, EU, Ukraine directly, including Nova Poshta delivery. |
| DHgate | Dropship order flow exists through partner integrations; no clearly documented public ordering API. Treat as manual until confirmed. | US, EU, Ukraine. |
| Shein | No API, no dropshipping program. Only an affiliate program. Automated checkout is against its terms. | US, EU; Ukraine direct delivery exists for some items (7–12 days), otherwise via forwarders. |
| Etsy | Open API v3 is seller-only; no buyer purchase endpoint. Etsy backs UCP, so agent checkout may arrive later. | Depends on each seller. |
| H&M, Zara, most fast-fashion brands | No buyer API. Only product data (official or scraped). | Their own country storefronts only (US store ships US, EU stores ship within the EU). No Ukraine shipping from US; H&M/Zara operate locally in Ukraine only partly. |

Conclusion: automation will always be partial. The architecture has to treat "a human places this order" as a first-class path, not an error.

### 1.2 Universal checkout services

- **Rye** (rye.com). Takes a product URL, buyer details and a Stripe payment token, returns a placed order. Works on Amazon, Shopify and "any merchant" through browser-level automation. Pricing: $149/month developer plan, $0.05 per order, $0.02 per product fetch. **US shipping addresses only**; international addresses fail. Docs say there are no webhooks and tracking emails go to the buyer email (the marketing page claims webhooks; needs checking).
- **Zinc** (zinc.com). $1 per successful order plus item cost, wallet funded by card/ACH/wire. Strongest on Amazon, Walmart, Target. Also US-centric.
- **Henry Labs, CartAI, Induced AI**: smaller agent-checkout players, either embedded one-click UIs or browser agents. None solves international shipping.
- **Protocols (ACP from OpenAI + Stripe, UCP from Google + Shopify, AP2, Visa Intelligent Commerce, Mastercard Agent Pay)**: all require the merchant to opt in. Good for Shopify stores now, useless for Shein/H&M.

Key observation: every universal checkout provider is US-address-only. **If the ship-to address is always a warehouse we control in the US, that restriction stops mattering.**

### 1.3 Ukraine logistics

- **NP Shopping** (Nova Poshta's forwarding, npshopping.com / nova.global). Each person registers and gets a personal virtual address (suite ID) at warehouses in 16 countries: USA, Poland, UK, Germany, France, Spain, Italy, Czech Republic, China, Canada, Slovakia, Lithuania, Latvia, Hungary, Romania, Estonia. Delivery from 12 days, from $3 per 0.5 kg, delivery to any NP branch, locker or door, fee paid on pickup or in the NP app. Free storage up to 24 days. **No returns to foreign stores.** No public API for the consumer service.
- **Nova Global business services** ("Global Sellers", Direct E-commerce, "single global API platform") handle cross-border B2C shipments into Ukraine with brokerage and customs clearance. Direct E-commerce already powers AliExpress/iHerb/Joom deliveries to NP branches. API access is by business agreement.
- **Meest** (Meest Shopping, Meest America, Meest World Logistics API). Warehouses in the US and Poland, a documented partner API (keys via b2c.ecommerce@meest.com), a "buy for me" service, and last-mile through Meest or Nova Poshta.
- **Ukraine Express, Bayshop, ColisExpat, Planet Express**: consumer forwarders, same model as NP Shopping.

**Does NP Shopping fit?** As a consumer product, only partly. It is cheap and reaches every NP branch, but it assumes each end user has their own account, pays delivery separately at pickup, and gets no returns. That breaks "one checkout". It works as an interim option (the user pastes their NP Shopping suite address into Fitkit and we ship to it), but the real fit is a **business agreement with Nova Global (or Meest)** where Fitkit sends pre-declared parcels in each customer's name through their API and prepays delivery in our own checkout.

**Customs into Ukraine (as of Sept 2026):** duty-free up to €150 per parcel per recipient; above that, 10% duty plus 20% VAT on the excess. Bills to charge 20% VAT from the first euro failed on 26 May and 1 Sept 2026, but two replacement bills passed first reading in Sept 2026; effective date discussed as July 2027. Plan for VAT from the first euro.

Parcels must be addressed to the real individual to use the personal allowance. A single Fitkit-named bulk import would be commercial import, fully taxed. So consolidation into Ukraine must be per recipient.

### 1.4 EU and US customs

- **EU**: from 1 July 2026 the €150 duty exemption is gone. A flat €3 duty per item (per tariff line) applies to low-value parcels from IOSS-registered sellers until July 2028. VAT is due from the first euro. As the seller of record shipping from outside the EU, Fitkit would need IOSS registration (through an intermediary) to collect VAT at checkout and give customers a no-surprise delivery.
- **US**: the universal $800 de minimis no longer applies; duty on imported parcels depends on origin and product. For US users we avoid imports entirely by buying from US merchants and shipping domestically.

### 1.5 Payments

- Stripe Payment Element / PaymentSheet on iOS with Apple Pay keeps card data off our servers (PCI SAQ A). One PaymentIntent per Fitkit order, **manual capture**: authorize the quoted total at checkout, capture after supplier orders are confirmed, capture less if an item fails. Card authorizations last about 7 days, which is enough because we place supplier orders within minutes to hours.
- Stripe is not available to Ukrainian-registered companies. Fitkit needs a US entity (Stripe Atlas LLC) or an EU entity (Poland or Estonia). Customers anywhere, including Ukraine, can pay with Visa/Mastercard and Apple Pay.
- Paying suppliers: Rye accepts a Stripe token, Zinc takes a prefunded wallet, AliExpress DS orders are paid from the linked account, manual orders need a company card. Single-use virtual cards per order (Stripe Issuing, Lithic, or a business bank like Mercury/Ramp) cap each purchase at the confirmed amount. Stripe's terms forbid facilitating payments "on behalf of another undisclosed merchant" and restrict Issuing to business spend, so the business model (we sell goods to the customer as merchant of record and buy them from retailers for resale) must be disclosed in the Stripe application and approved in writing before launch.

## 2. Recommended architecture: Fitkit as merchant of record, hub and spoke

Fitkit sells the outfit to the customer and becomes the merchant of record: one checkout, one charge, one support contact. Behind that, the server splits the order into supplier orders and routes each through the most automated path available. Retailers always ship **domestically to a hub we control**, so every retailer and every checkout provider works regardless of the customer's country. Hubs forward to the customer.

```
iOS app
  "Get this look" -> sizes -> address -> Apple Pay (Stripe, one charge, auth only)
        |
Fitkit server
  Quote (items + shipping + duties/VAT + service fee)
  Order -> supplier orders, one per merchant
        |
  Fulfillment adapters (pick first that works)
    1. Direct API        AliExpress DS API, Shopify UCP (trusted agent)
    2. Universal checkout Rye (fallback Zinc for Amazon/Walmart)
    3. Concierge queue   human ops places the order (Shein, H&M, Zara, Etsy, DHgate)
        |
  Ship-to address chosen by route:
    US customer, US merchant   -> customer's door directly
    EU customer, EU merchant   -> customer's door directly (EU storefronts)
    EU customer, US merchant   -> US hub -> forwarder -> EU door (VAT via IOSS, €3/item duty)
    UA customer, any merchant  -> US hub or Poland hub -> Meest / Nova Global B2B -> NP branch/locker/door
    UA customer, AliExpress    -> direct to Ukraine via AliExpress's Nova Poshta option
        |
  Tracking aggregator (merchant tracking + hub inbound + outbound + last mile) -> push notifications
```

Why this is the most predictable and geographically accessible option:

- **One contract per country pair, not per retailer.** Ukraine and EU coverage depend on our forwarding partners, which we can negotiate with, not on whether Shein or H&M decide to ship to Kyiv.
- **US-only tools become global.** Rye and Zinc only ship to US addresses; the US hub is a US address.
- **Poland hub is the Ukraine and EU workhorse.** EU storefronts (Shein EU, H&M, Zara, Amazon.de/.pl) ship to Poland domestically in a few days. Intra-EU forwarding has no customs, and Poland to Ukraine is the shortest, cheapest lane for both Meest and Nova Poshta.
- **Consolidation per customer** means one delivery for the whole outfit, one customs declaration per recipient (keeps the €150 Ukraine allowance), and one tracking number for the user.
- **Cost is higher** (hub handling, a second shipping leg, per-order provider fees, ops labor). The user said fee hikes are acceptable; they are shown as a transparent "delivery and handling" line.

Recommended partners to start with:

| Role | First choice | Fallback |
|---|---|---|
| Customer payments | Stripe (PaymentSheet, Apple Pay, manual capture, Stripe Tax) | none needed for v1 |
| Universal checkout (US addresses) | Rye | Zinc for Amazon/Walmart |
| AliExpress | AliExpress Open Platform DS API | Concierge |
| Shopify stores | Shopify UCP / Checkout MCP once trusted-agent status is granted | Rye |
| Everything else | Concierge ops queue | Browser automation later, only where terms allow |
| US hub and UA/EU forwarding | Meest (US + Poland warehouses, partner API, NP or Meest last mile) | Nova Global business (Global Sellers) |
| Tracking | AfterShip or 17TRACK API | Poll carriers directly |
| Supplier payment cards | Single-use virtual cards (Stripe Issuing if approved for this use, else Lithic/Mercury) | Company card for concierge |

NP Shopping's role: offer "Ship to my NP Shopping address" as an interim Ukraine option in phase 2 (user enters their personal suite ID, pays NP's delivery in the NP app), then replace it with the Nova Global or Meest B2B route in phase 3 so the user pays everything in one checkout.

## 3. Implementation plan

### Phase 0: business prerequisites (blocks everything; start now)

1. Entity: US LLC via Stripe Atlas, or a Polish/Estonian company if the EU is the bigger market. Needed for Stripe, Rye, card issuing and forwarder contracts.
2. Stripe account with the reseller/personal shopping model disclosed; get written confirmation it is acceptable, including for Issuing if we use it.
3. Forwarder agreements: Meest World Logistics API keys (b2c.ecommerce@meest.com) with US and Poland inbound; parallel conversation with Nova Global business. Ask each for: per-recipient declarations via API, DDP/prepaid delivery, consolidation, inbound matching by tracking number, returns handling, rates per kg.
4. Rye developer account (30-day trial) and Zinc account; confirm webhook/tracking behavior and whether the buyer email can be our per-order alias.
5. AliExpress Open Platform developer app with DS API permissions.
6. Shopify agent profile registration; apply for trusted-agent checkout.
7. EU VAT: IOSS intermediary. US: Stripe Tax for sales tax. Terms of sale, returns policy, privacy policy covering shipping data shared with forwarders.

### Phase 1: purchasable items and quotes (server + iOS, no money yet)

Server:
- Today the cart stores pins (`cart_items`), and `Listing` has title, merchant, URL, price. Add a `products` resolution step: listing URL to merchant, variants (size, color), availability, price in source currency. Use Rye's product fetch for US merchants, the AliExpress DS product API, Shopify Catalog API; for others, scrape and mark as concierge.
- New tables: `addresses` (country, region, NP branch/locker ref for Ukraine), `size_profiles`, `quotes`, `quote_lines`.
- Router: `(merchant, destination country) -> route` (adapter + ship-to hub or direct) as data, not code, so ops can change it.
- Landed-cost calculator: item price + domestic shipping + hub handling + international leg (forwarder rate by weight estimate per category) + duties/VAT per destination rules above + Fitkit fee. Show each line.
- Endpoints: `POST /v1/quotes` (items with chosen variants + address id), `GET /v1/quotes/{id}`; quotes expire in 30 minutes.

iOS:
- "Get this look" on Pin Detail: choose which detected items to buy (default all), one listing per item, size/color picker, remembered sizes.
- Address book with country-specific forms; for Ukraine, NP branch/locker picker (Nova Poshta public API for branch lists).
- Quote screen with a clear breakdown and delivery estimate per destination.

### Phase 2: payment and order placement (US first, then Ukraine via NP Shopping address)

Server:
- stripe-go: create PaymentIntent with `capture_method=manual` from a quote; handle `payment_intent.amount_capturable_updated`, `payment_intent.canceled`, `charge.refunded`, `charge.dispute.created` webhooks.
- `orders`, `supplier_orders`, `shipments` tables with a state machine: `authorized -> placing -> placed (all) -> captured -> in_transit -> at_hub -> forwarded -> delivered`, plus `partially_failed`, `refunded`.
- Fulfillment adapter interface in Go: `Quote`, `Place`, `Status`, `Cancel`. Implementations: `rye`, `aliexpress`, `concierge`. Idempotency key per supplier order so retries never double-buy.
- Per-order inbound email alias (e.g. `o-<id>@orders.fitkit.app`) used as buyer email with merchants; parse confirmations and tracking numbers.
- Capture rule: capture the sum of successfully placed items after placement; release the rest. Never capture more than the authorized amount; if a price rose above the quote tolerance, ask the user in-app before placing.
- Concierge admin page (simple server-rendered HTML behind staff auth): queue of supplier orders, product link, variant, ship-to address, one-time card details from the issuer, fields to paste merchant order number and tracking.

iOS:
- Stripe iOS SDK (`StripePaymentSheet` via SPM), Apple Pay merchant ID, PaymentSheet with the server's client secret.
- Orders tab: status per item, tracking, push notifications (APNs) on state changes.

Scope for launch of phase 2: US customers (Rye + concierge, direct shipping, no hub) and Ukraine customers who provide an NP Shopping suite address (we ship to their NP US/Poland warehouse; they pay NP delivery on pickup). This proves the whole money and ordering loop with the fewest partners.

### Phase 3: hubs and one-checkout international delivery

- Integrate Meest (or Nova Global) API: create inbound pre-alerts with merchant tracking numbers and recipient data, receive at-hub events, request consolidated outbound per customer order, get outbound tracking.
- Route UA and EU customers through the US or Poland hub; charge forwarding and estimated duties in the Fitkit checkout (DDP where the forwarder supports it).
- EU direct route for EU storefronts; IOSS VAT collection at checkout.
- Hub exceptions in the admin page: missing item after N days, damaged, wrong size, over-weight.
- Remove the NP Shopping interim option once B2B delivery is live, or keep it as a cheaper "I'll pay delivery myself" choice.

### Phase 4: more automation, less ops

- Shopify UCP checkout adapter once trusted-agent status is granted (direct merchant checkout, international where the store ships).
- Zinc adapter as Rye fallback for Amazon/Walmart.
- Measure concierge volume per merchant; automate only where the retailer's terms allow it or a partner (Rye) takes that responsibility. Never bypass CAPTCHAs or bot detection.
- Returns: ship back to the hub, hub returns to merchant where possible; for Ukraine, returns end at our hub (NP forwarding has no returns), so offer store credit or resale there.

## 4. Risks and open questions

- **Stripe approval of the reseller model** is the single biggest blocker. If refused, alternatives are Adyen or Checkout.com (both support marketplaces and merchant-of-record models) or Paddle-like MoR providers, though physical goods limit those options.
- **Merchant terms**: Shein and others prohibit automated ordering and may cancel orders shipped to forwarder addresses. Human concierge with regular accounts reduces this; keep per-merchant cancellation rates.
- **Price and stock drift** between quote and placement. Tolerance setting plus in-app re-confirmation.
- **Size and fit returns** are the main cost driver in fashion; ask for measurements, show size charts, price return handling into the fee.
- **Ukraine customs change** (VAT from first euro, possibly July 2027). Keep duty rules in config.
- **Delivery times**: US to US 3–7 days; Poland to Ukraine about 5–10 days; US to Ukraine from 12 days plus merchant shipping. Show honest estimates per route.
- **Rye behavior**: docs say no webhooks and tracking emails to the buyer; confirm on the trial and rely on the email alias if so.
- **Wartime delivery disruptions in Ukraine**: allow branch/locker changes after dispatch (NP supports address change).

## 5. Verify before building

- Rye: international shipping still US-only? Webhooks? Which merchants actually succeed for fashion (H&M US, Zara US, Shein US)?
- Shopify: trusted-agent requirements and how payment is handed over in Checkout MCP.
- AliExpress DS API: Ukraine shipping methods exposed through the API, payment method for DS orders.
- Meest / Nova Global: B2B API capabilities, per-recipient customs declarations from US and Poland, DDP, rates.
- Stripe: written OK for the business model; Issuing eligibility.
- Current Ukraine and EU customs thresholds at the time of launch.

## Sources

- Rye: https://rye.com/products/universal-checkout-api, https://rye.com/pricing, https://rye.com/docs/api-v2/example-flows/simple-checkout, https://rye.com/blog/agentic-commerce-startups
- Zinc: https://www.zinc.com/pricing
- Shopify UCP: https://shopify.dev/docs/agents, https://www.shopify.com/news/spring-26-edition-dev
- AliExpress Open Platform: https://openservice.aliexpress.com/doc/api.htm
- Etsy Open API: https://developers.etsy.com/documentation/
- Shein (no dropshipping/API): https://cjdropshipping.com/blogs/dropshipping-knowledge/Shein-Dropshipping-Guide
- NP Shopping and Nova Global: https://nova.global/en-ua/for-you/shopping/, https://nova.global/en-ua/for-you/priamyi-e-commerse/, https://nova.global/en-ua/for-business/services/
- Meest: https://meest.shopping/, https://documenter.getpostman.com/view/12823986/TzCTam5v
- Stripe restrictions: https://stripe.com/legal/restricted-businesses
- Ukraine customs: https://globalpost.ua/en/blog/tamozhennye-limity-dlya-besposhlinnogo-vvoza-otpravlenij/, https://dev.ua/en/news/rada-provalyla-holosuvannia-pro-skasuvannia-limitu-na-bezmytni-posylky-z-za-kordonu-1779802509, https://en.interfax.com.ua/news/economic/1205195.html
- EU customs: https://www.consilium.europa.eu/en/press/press-releases/2025/12/12/customs-council-agrees-to-levy-customs-duty-on-small-parcels-as-of-1-july-2026/, https://taxation-customs.ec.europa.eu/news/guidance-and-legal-text-temporary-flat-fee-low-value-imports-which-will-apply-until-1-july-2028-2026-06-08_en
