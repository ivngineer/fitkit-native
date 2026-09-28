# Fitkit Server — Cloudflare Workers Port Specification

**Deliverable of implementation:** the iOS app, built with `FITKIT_API_BASE_URL` pointing at a Cloudflare-hosted backend, works end to end with no client code changes: signup/login, Pinterest import, pin grid, hide, cart, item analysis ("Find pieces"), listing options, Get-This-Look checkout plans, looks/orders tracking, addresses, sizes, media images. A demo run on the Cloudflare **free tier** costs $0 and stays inside every free-plan limit at demo scale.

**Source of truth:** the existing Go server in `server/`. This spec maps it; when this spec and the Go code disagree on behavior, the Go code wins. Implementations are changeable; functionality is not.

**Location of new code:** a new top-level directory `cloudflare/` in this repository. Do **not** modify, replace, or delete `server/` (the Go server stays the local-dev/reference implementation) and do not touch `ios/` (zero client changes are expected; see §9).

---

## 1. Current system (what exists today)

Go 1.26 HTTP server (`server/cmd/fitkitd`), SQLite via `modernc.org/sqlite` (pure Go, WAL, single connection), on-disk media under `data/media/{pins,crops}`, in-process background goroutines for imports and analyses, outbound HTTP to Pinterest (scrapes its private JSON resource endpoints), Google Gemini (object detection), imgbb (crop hosting), SerpApi/SearchApi/Scrapingdog (Google Lens visual search), Shopify stores (`/products/<handle>.js`, `/meta.json`). No CORS (native client only). Config from `.env` (see `server/.env.example`). Demo mode (`FITKIT_DEMO_ANALYSIS=1`) replaces paid providers with deterministic placeholders.

Packages: `api` (routing/handlers/validation), `auth` (bcrypt + opaque bearer tokens), `config`, `store` (SQLite), `importer` (background import jobs), `pinterest` (discovery client), `analyze` (detection → crop → upload → visual-search pipeline), `shop` (listing classification, Shopify data, checkout-plan builder, affiliate links, shipping routing, merchants table).

## 2. Target architecture

One Worker (TypeScript) + D1 (SQLite) + R2 (media) + Cron Triggers (background jobs) + Cloudflare Image Transformations (crops). All on free plans.

| Concern | Go server | Cloudflare port |
|---|---|---|
| HTTP API | `net/http` mux | Worker `fetch` handler; routing per §3 (Hono or a small hand-rolled router — either is fine; no heavyweight framework) |
| Database | SQLite file, WAL | **D1** (SQLite-compatible; same schema, §7) |
| Pin/crop images | local `data/media/` | **R2** bucket, served by the Worker at the same `/media/*` paths |
| Image cropping for analysis | in-process Go `image` package | **Cloudflare Image Transformations** via `/cdn-cgi/image/` URL (free plan: 5,000 unique transformations/month; works on R2-sourced images) — no pixel code in the Worker (§6.2) |
| Import jobs | goroutines + channel queue | D1-backed queue drained by a **Cron Trigger** (§6.1) |
| Analysis jobs | goroutines per pin | D1 queue drained by a **Cron Trigger** (§6.1) |
| Password hashing | bcrypt | **PBKDF2-SHA256** via WebCrypto, 100,000 iterations (§8) |
| Session tokens | 32-byte random, SHA-256 stored hash | identical (WebCrypto) |
| Rate limiting | in-process map | D1-backed fixed-window counters (§8.3) |
| Config | `.env` | wrangler vars + secrets (§10) |

**Prerequisite — custom domain:** the Worker must be attached to a Cloudflare zone (e.g. `api.<your-domain>`); free zone plan is fine. Image Transformations (`/cdn-cgi/image/`) are a zone feature and do not work on `workers.dev` hostnames. Without a custom domain the crop fallback ladder (§6.2) degrades analysis quality but everything else works.

## 3. API contract (must match exactly)

The iOS client (`ios/Fitkit/Networking/APIClient.swift`) calls these with `Authorization: Bearer <token>`, JSON bodies, 30 s request timeout. Error body shape everywhere: `{"error": {"code": "...", "message": "..."}}` (plus `itemId` on plan-input errors). Keep every route, method, status code, error code, and message string from `server/internal/api/api.go` and `checkout.go`. The full list, with the Go handler as reference:

| Route | Status | Handler (Go) |
|---|---|---|
| `GET /healthz` | 200 `{"ok":true}` | api.go |
| `POST /v1/auth/signup` · `login` | 201/200 `{token,user}` · limited 20/min/IP | signup/login |
| `POST /v1/auth/logout` | 204 | logout |
| `GET /v1/me` · `DELETE /v1/me` | 200 · 204 | me/deleteAccount |
| `PUT /v1/me/referral-source` · `PUT /v1/me/pinterest` | 200 user | setReferral/setPinterest |
| `POST /v1/imports` | 202 import job | startImport |
| `GET /v1/imports/latest` · `GET /v1/imports/{id}` | 200 · 404 | latestImport/getImport |
| `GET /v1/pins?limit&cursor` | 200 `{pins,nextCursor}` | listPins |
| `PUT/DELETE /v1/pins/{id}/hidden` | 204 | hidePin |
| `GET /v1/cart` · `PUT/DELETE /v1/cart/{id}` | 200 · 204 · 204 | listCart/add/remove |
| `GET /v1/pins/{id}/analysis` · `POST /v1/pins/{id}/analysis?force=true` | 200 · 202 | get/startAnalysis |
| `GET/POST /v1/addresses` · `PUT/DELETE /v1/addresses/{id}` | 200/201/204 | checkout.go |
| `GET/PUT /v1/sizes` | 200 `{sizes}` | getSizes/putSizes |
| `POST /v1/listings/options` | 200 `{options}` · user-limited | listingOptions |
| `POST /v1/looks` · `GET /v1/looks` · `GET /v1/looks/{id}` | 201/200 | createLook/list/get |
| `PUT /v1/looks/{id}/stores/{merchant}` | 200 lookStore | updateLookStore |
| `GET /media/pins/{file}` · `GET /media/crops/{file}` | 200 image bytes | §6.3 |

Behavioral details the client depends on (all in the Go code — port them literally):

- **JSON field names and nullability** of `userJSON`, `importJSON` (+`jobError`), `pinJSON` (`imageUrl` is the relative `/media/pins/<file>`; `pinterestUrl` composed server-side), analysis body (`{pinId,status,result,error}`), `addressJSON` (fields object), sizes map, `ListingOptions` (+ embedded `Product` with `options`/`variants`), `lookJSON`/`lookStoreJSON` (`trackingUrl` composed server-side, carrier table in checkout.go:403).
- Pagination: `nextCursor` is the last pin's `savedAt` in **unix milliseconds**, exclusive `before` cursor; `limit` clamps to (0,100] default 50.
- Auth failures: 401 `unauthorized` with the exact two message variants (no/expired session). Ownership mismatches return 404, never 403 (see `getImport`, `savedPin`, `getLook`).
- Validation: email normalization rules, password 8–72 bytes, referral source enum, Pinterest handle normalization (`pinterest.NormalizeHandle`), address validation incl. UA/Nova Poshta/forwarder rules and all user-facing messages, sizes category enum + limits (checkout.go constants), listing options 1–30 URLs and the "known listing" check against the stored analysis, look-store status enum and tracking-number pattern `^[A-Za-z0-9-]{4,40}$`.
- Request bodies are capped at 1 MB (Go `MaxBytesReader`) — enforce the same cap before parsing.
- `POST /v1/pins/{id}/analysis` returns the **current** status when a job is already running/done (dedupe), `force=true` re-runs, `none` status when nothing exists.
- Import `POST` returns the existing active job rather than creating a second one; 422 `missing_pinterest_handle` without one.

## 4. Platform limits that shape the design (Workers Free plan, verified 2026-09)

| Limit | Free value | Consequence |
|---|---|---|
| CPU per HTTP request / per cron invocation | **10 ms** | No bcrypt, no image decode/encode in the Worker. JSON handling + WebCrypto PBKDF2 (native) fit. Network waits don't count toward CPU. |
| Subrequests per invocation (fetches **and** binding ops: each D1 query, R2 call) | **50** | Chunked cron processing; batch D1 writes; see ledger §6.4. |
| Simultaneous open connections | 6 | Parallelize at most 6 outbound fetches (Go used 4 import workers / 4 shop fetch workers — keep ≤6). |
| Wall time | HTTP: unlimited while client connected; `waitUntil`: 30 s after response; **cron: 15 min** | Chunking is driven by the subrequest cap, not wall time. |
| Requests | 100,000/day | Irrelevant at demo scale. |
| D1 | 10 DBs/account, **500 MB/DB**, 5 GB/account, **50 queries per Worker invocation**, ~5 M rows read / 100 k rows written per day; `batch()` counts as one API call but per-statement limits apply | Schema unchanged; write-heavy progress updates batched. |
| R2 | 10 GB-month, 1 M Class A / 10 M Class B ops per month, zero egress | Pin images ~0.1–0.3 MB each; demo scale trivial. |
| Image Transformations (Images Free) | 5,000 **unique** transformations/month; works on R2-hosted sources; over-limit returns error 9422, no charge | Crops are deterministic URLs → cached; ~8 unique crops per analysis. Demo scale fine. |
| Cron Triggers | 5 per account (free), 15 min wall | One Worker, one cron expression `* * * * *` draining both queues. |
| Worker size | 3 MB gzipped (free) | Keep dependencies minimal (Hono or none). |
| Memory | 128 MB | Streams, don't buffer responses > a few MB; pin images ≤ 25 MB cap (Go `maxImageBytes`) — buffer per-image is fine, never whole batches. |

## 5. Project layout (new code)

```
cloudflare/
  wrangler.jsonc          # bindings: DB (d1), MEDIA (r2), vars, observability, cron
  package.json            # wrangler, typescript, vitest, @cloudflare/vitest-pool-workers (+hono if used)
  tsconfig.json
  .dev.vars.example       # local secrets mirror of server/.env.example
  migrations/
    0001_init.sql         # full schema (Go store.go schema + hidden_at already present)
  src/
    index.ts              # fetch + scheduled entrypoints, router wiring
    routes/               # one file per resource area (auth, me, imports, pins, cart, analysis, addresses, sizes, options, looks, media)
    db/
      client.ts           # D1 helpers: query/batch wrappers, error→NotFound/Conflict mapping, now(), newID()
      repo/               # users.ts, sessions.ts, pins.ts, jobs.ts, analyses.ts, addresses.ts, looks.ts, cache.ts — direct ports of server/internal/store/*.go queries
    auth.ts               # PBKDF2, tokens, email/handle normalization (port of internal/auth)
    services/
      importer.ts         # chunked import worker (port of internal/importer)
      analysis.ts         # chunked analysis worker (port of internal/analyze)
      pinterest.ts        # discovery client (port of internal/pinterest — same endpoints/headers/retries)
      lens.ts             # Gemini detector + SerpApi/SearchApi/Scrapingdog searchers (port of analyze/providers.go)
      shop/               # classify.ts, shopify.ts, plan.ts, links.ts, shipping.ts, options.ts, merchants.json (copy verbatim)
    http.ts               # JSON/error helpers, body cap, authed(), limited()
    env.ts                # typed Env (generated by `wrangler types`)
  test/                   # vitest specs (see §11)
  README.md               # setup/deploy/runbook
```

TypeScript strict mode. No `any` on `Env`. Generate `Env` with `wrangler types`.

## 6. Design changes that need explanation

### 6.1 Background jobs: D1 queues + one cron trigger

Both Go background loops become rows in D1 drained by a single `scheduled()` handler on `* * * * *` (runs every minute; worst-case job start latency ≈ 1 minute — the client already polls job status, and `POST /v1/imports` / `POST .../analysis` still return immediately with a `queued` job).

**Import.** `POST /v1/imports` = Go behavior (create-or-reuse job row; 202). The cron tick:

1. Claim: `UPDATE import_jobs SET status='running', updated_at=? WHERE id=(SELECT id FROM import_jobs WHERE status='queued' ORDER BY created_at LIMIT 1) RETURNING *` — atomic; re-running ticks can't double-claim.
2. If the claimed job has no stored discovery yet: run Pinterest discovery (port of `pinterest.Discover` — profile → saved feed → boards fallback, bookmarks pagination, same resource options/headers/retry-with-backoff/error codes `profile_not_found|private_profile|no_saves|rate_limited|upstream_error`). Persist the discovered pin list to a new table `import_pins (job_id, position, pin_json, status DEFAULT 'pending', saved_at)` and set `discovered`. This bounds re-work: discovery happens once per job.
3. Each tick downloads up to **10 pending** `import_pins` (≤ 6 in flight): fetch image (25 MB cap, `i.pinimg.com`), derive `width`/`height` from the **feed metadata** (`images.orig.width/height` — no decode needed; if missing, parse the JPEG SOF header bytes for dimensions, which is cheap), `PUT` to R2 `pins/<pinID>.<ext>`, insert pin row + `saved_pins` row. Then `batch()` one progress update.
4. Every few ticks (and at completion) update `imported/reused/failed` counters. `reused` = pin already existed by `pinterest_id` (then just `INSERT OR IGNORE saved_pins`). Conflict on concurrent insert: same resolution as Go (use the winner's pin row).
   `saved_at` ordering: Go assigned `base - i*ms` to preserve Pinterest's newest-first order — reproduce exactly from `position`.
5. All pending done → `FinishImportJob` (`done`, or `failed`/`ingest_failed` with the same retryable semantics).
   Ticks that find nothing to do must stay cheap: one `SELECT` for queued jobs + one for pending analyses.

**Analysis.** `POST /v1/pins/{id}/analysis` → create/refresh `pin_analyses` row `status='queued'`, return 202 (client polls `GET`). Cron tick:

1. Reset stale: analyses `queued/running` with `updated_at` older than 5 min → `failed`/`interrupted` (port of `ResetStaleAnalyses`, which Go ran at boot).
2. Claim one queued analysis (atomic `UPDATE ... WHERE status='queued' RETURNING`), set `running`.
3. Run the pipeline (§6.2): fetch image bytes from R2, Gemini detect (inline base64, same prompt/schema/model chain incl. fallbacks `gemini-3.6-flash → 3.5 → 2.5`), then per item (≤ 8, dedupe by category/label exactly as `dedupeDetections`) build the crop URL and run Lens search (parallel ≤ 6), rank listings (`rankListings`: drop empty/dup/pinterest hosts, priced first, cap 12).
4. `PUT` the final `Result` JSON (same shape: `items` with `id item-N`, `label`, `category`, `description`, `box {x,y,width,height}`, `cropUrl`, `listings[]`, `error`; `provider`; `demo`; `generatedAt`) and `done`, or `failed` with codes `analysis_failed` / `no_items` and the Go user-facing messages.
5. One analysis per tick is enough (≥ 8 Lens calls + 1 R2 + 1 Gemini ≈ 11 subrequests, well under 50; CPU is JSON-only). If the queue is deep, drain up to 2/tick.

Not-configured handling: same three-state behavior as `analyze.NewService` — no keys at all → immediate `failed`/`not_configured` on Start (no queue); Gemini only → `SearchLinks` mode (store search links, no prices); demo mode → `DemoDetector`/`DemoSearcher` ports (fixed boxes, demo listings incl. the fake Shopify store under `*.fitkit.example`).

### 6.2 Crops without pixel code

Go cropped with `image` package, uploaded to imgbb (24 h TTL) for Lens to fetch, and kept a local copy — hence the `dropExpiredCrops` hack. **Replace both with permanent Cloudflare Image Transformations URLs:**

- Crop URL = `https://<worker-domain>/cdn-cgi/image/url=/media/pins/<file>.jpg,fit=crop,crop=<cx>,<cy>,<cw>,<ch>,w=<w>,h=<h>,f=auto,q=90` where the pixel rect is computed from the normalized box **plus the same 0.06 padding and clamping** as `analyze.Crop`, and `minCropSidePixels=48` is checked arithmetically (dimensions are known — no decode). `crop=` takes absolute pixels (compute from stored pin width/height). Under 48 px → item `error: "Couldn't crop this item."` (same as Go).
- This URL is public: Lens providers fetch it directly (**imgbb dependency is removed entirely** — no `IMGBB_API_KEY`, no 24 h expiry, no `dropExpiredCrops`), and the app displays it as `cropUrl`.
- Transformations are counted per unique (source, params) and edge-cached; re-analyses with `force` reuse the cache unless the box moved.
- Keep `LocalUploader`-equivalent behavior for **demo mode**: write a static placeholder crop per item to R2 (`crops/<pinID>-item-N.jpg`, tiny generated JPEG committed in the repo or a 1×1 constant is fine — the demo app only needs a loadable URL) so demo crop URLs resolve.

**Fallback ladder if `/cdn-cgi/image/` is unavailable at implementation time** (e.g. no custom domain, or transformations reject zone-relative URLs): (1) absolute-`url=` form `.../cdn-cgi/image/url=https%3A%2F%2F<domain>%2Fmedia%2F...`; (2) pass the **full pin image URL** to Lens without a crop — listings still return, relevance drops; mark `cropUrl: ""`; (3) last resort: WASM JPEG crop — do not attempt by default, 10 ms CPU makes it unreliable. Verify (1) with a real Lens call before settling.

### 6.3 Media serving

`GET /media/pins/{file}` and `/media/crops/{file}`: look up nothing in D1; `MEDIA.get('pins/'+file)` → stream body with the stored/derived `Content-Type` (by extension), `Cache-Control: public, max-age=31536000, immutable` (same as Go), `ETag` from the R2 `httpETag`, 404 on missing object. No directory listing (paths with empty/trailing segment → 404), mirroring `noDirListing`. These are unauthenticated, exactly as in Go.

### 6.4 Subrequest ledger (per invocation, free cap 50)

| Invocation | D1 queries (batched where noted) | fetches | Total |
|---|---|---|---|
| Typical authed CRUD endpoint | 1–3 (1–2 D1 subrequests) | 0 | ≤ 5 |
| `POST /v1/listings/options` (30 URLs) | ~3 (cache lookups batched) + ~30 cache writes batched = ~4 | ≤ 30 Shopify | ≤ 35 |
| `POST /v1/looks` (8 items) | ~6 | ≤ 8 products + ≤ 8 storefronts | ≤ 23 |
| Import cron tick (10 pins) | ~14 (claims/progress, batched) | 10 images | ≤ 25 |
| Analysis cron tick (1 pin) | ~4 | 1 R2 + 1 Gemini + ≤ 8 Lens | ≤ 14 |
| `/media/*` | 0 | 1 R2 | 1 |

Keep a comment-block ledger like this in `src/index.ts` and re-verify when adding endpoints.

## 7. Data model (D1)

Use the **exact schema from `server/internal/store/store.go`** (tables `users, sessions, pins, saved_pins, cart_items, import_jobs, pin_analyses, addresses, size_profiles, looks, look_stores, shop_cache`, all indexes, `COLLATE NOCASE` email, integer-ms timestamps, `fields_json` JSON-in-text) with these deltas:

- `saved_pins.hidden_at` present from the start (Go added it via runtime `addColumn`; in D1 use wrangler migrations — `migrations/0001_init.sql` only, no runtime schema mutation).
- **Drop `embedding_jobs`** — written by `InsertPin`, read by nothing but a test helper. Note the removal in the README.
- **Add** `import_pins (job_id TEXT REFERENCES import_jobs(id) ON DELETE CASCADE, position INTEGER, pin_json TEXT, saved_at INTEGER, status TEXT DEFAULT 'pending', PRIMARY KEY (job_id, position))` for chunked imports.
- **Add** `rate_limits (key TEXT PRIMARY KEY, window_start INTEGER, count INTEGER)` (§8.3).
- Seed the synthetic `pinterest` author user in the migration (same `INSERT OR IGNORE`).
- Migrations run via `wrangler d1 migrations apply` — both locally and remote; never at runtime.

D1 notes: SQLite dialect is the same — `INSERT OR IGNORE`, `ON CONFLICT ... DO UPDATE`, `COALESCE` patches, subquery ownership checks (`look_id IN (SELECT id FROM looks WHERE user_id = ?)`) all port verbatim. Foreign keys are enforced (matches the Go `foreign_keys(1)` pragma; no WAL/busy_timeout pragmas needed). Map D1 errors: message containing `UNIQUE constraint failed` → conflict (Go checked `"UNIQUE"`), empty result → not-found. `db.batch([...])` for multi-statement writes (addresses default shuffling, `SetSizes` replace, look creation, import progress) — D1 batches are transactional.

Data migration from an existing Go `data/fitkit.db`: out of scope (greenfield demo deploy); note the path (sqlite → `wrangler d1 export/import` + R2 sync of `data/media/`) in the README as a follow-up.

## 8. Auth & security port

- **PBKDF2-SHA256, 100 000 iterations, 16-byte salt**, output stored as `pbkdf2$100000$<b64salt>$<b64hash>`. `crypto.subtle.deriveBits` (native — fits CPU budget; measure in tests, drop to 60 000 if a free-plan CPU overrun ever shows in logs). Verify with a constant-time comparison. Same password rules (8–72 bytes) and messages.
- **Timing equalization** (Go `BurnPasswordCheck`): on login with unknown email, run the same PBKDF2 verify against a module-level dummy hash before returning 401.
- **Tokens:** 32 random bytes → base64url token; store `SHA-256` hex; TTL default 60 days (`SESSION_TTL_DAYS` var). Same header parsing (`Bearer ` prefix), same 401 variants.
- **IDs:** `crypto.getRandomValues` 16-byte hex — same format as `store.NewID`.
- Outbound fetch policy (port of `shop.safeClient` intent): Shopify/external fetches are HTTPS-only, ≤ 3 redirects, 8 s timeout, 2 MB body cap. Workers can't reach loopback/private ranges from a deployed Worker, so the Go dial-guard is structurally satisfied; keep the HTTPS + caps checks.
- Secrets never in wrangler.jsonc plaintext — `wrangler secret put` for keys, `.dev.vars` locally (gitignored).
- `crypto.randomUUID()`/`getRandomValues` only — no `Math.random()` for IDs/tokens.

### 8.3 Rate limiting (D1-backed)

Go used an in-process 20/min fixed window keyed by IP+path (signup/login) and user+path (`listingOptions`, `createLook`). Isolates are shared/ephemeral, so port to `rate_limits`: on a limited endpoint run `INSERT INTO rate_limits(key, window_start, count) VALUES(?,?,1) ON CONFLICT(key) DO UPDATE SET count = CASE WHEN window_start = ? THEN count + 1 ELSE 1 END, window_start = CASE WHEN window_start = ? THEN window_start ELSE ? END RETURNING count` (single statement); reject when `count > 20` with 429 `rate_limited` and the Go messages. Opportunistic cleanup: delete expired rows inside the cron tick. Get the client IP from the `CF-Connecting-IP` header. (D1 writes: ~2/limited-request — well under 100 k/day at demo scale.)

## 9. iOS client — verification only, no changes

`ios/scripts/deploy-device.sh` bakes `FITKIT_API_BASE_URL` at build time; `APIClient.resolve()` resolves server-relative `/media/...` against it, and absolute URLs pass through. So: deploy with `FITKIT_API_BASE_URL=https://api.<your-domain>` (note the script's `://`-escaping quirk already handles https). ATS is satisfied by https. **Expected client-visible differences — none.** New job-start latency (≤ 1 min for imports/analyses) is inside the existing polling UX (the app polls job status; verify the polling doesn't time out — check the client's poll interval in `AppModel.swift` during implementation and confirm it tolerates a 1-minute queue delay; if any spinner has a hard 30 s budget, flag it and extend server-side priority rather than changing the client contract).

## 10. Configuration

Vars (wrangler.jsonc, non-secret): `SESSION_TTL_DAYS=60`, `IMPORT_PIN_LIMIT=250`, `DEMO_ANALYSIS` (bool), `LENS_PROVIDER=searchapi`, `GEMINI_MODEL`/`GEMINI_FALLBACK_MODELS`, `SHOPIFY_SHOP_PAY=true`, `AMAZON_ASSOCIATE_TAG_<SITE>` per site (same 11-site map), `ALIEXPRESS_AFFILIATE_KEY`*, `SKIMLINKS_PUBLISHER_ID`* (*if secret, use secrets — affiliate keys are low-sensitivity, vars are fine). Secrets: `GEMINI_API_KEY`, `SERPAPI_API_KEY`, `SCRAPINGDOG_API_KEY`, `SEARCHAPI_API_KEY`. `PinterestBaseURL` stays the default public site. `Addr`/`DataDir`/`ImportWorkers` are meaningless on Workers — drop. Enable `observability.enabled = true` (Workers Logs) from day one; structured JSON log lines per request like Go's `logRequests` (skip `/media/`).

## 11. Testing & verification plan

- **Unit (vitest + `@cloudflare/vitest-pool-workers`, real D1/R2 sim):** port the intents of the Go suites — `api_test.go` (route/status/error-code contract, §3 table as the checklist), `checkout_test.go` (address validation matrix, looks lifecycle, plan building), `shop_test.go` (URL classification, Amazon ASIN extraction, Shopify product parsing incl. legacy option shapes, variant matching, cart-URL prefill fields, shipping routing/customs warnings), `pinterest_test.go` (envelope parsing, feed fallback logic, error mapping), `importer_test.go` (chunking, reuse, conflict resolution, progress accounting), `analyze_test.go` (detection parsing/dedupe, crop rect math, listing ranking).
- **Contract smoke:** a script hitting a deployed preview with the §3 table (signup → login → me → import (demo Pinterest fixture or real handle) → pins → analysis (demo mode) → options → look → look update) and asserting bodies/status codes byte-comparable to the Go server's responses on the same inputs (run both side by side locally).
- **Free-tier drill:** verify each cron tick stays under 50 subrequests and ~10 ms CPU via Workers Logs invocation data; confirm Images transformations counter only grows by unique crops.
- **End-to-end:** deploy to the custom domain, run `ios/scripts/deploy-device.sh` with `FITKIT_API_BASE_URL=https://api.<domain>`, exercise the full app on device: onboarding → import real pins → grid/cart → find pieces → Get This Look → checkout link opens store → mark ordered w/ tracking.

## 12. Implementation order (suggested)

1. Scaffold `cloudflare/` (wrangler, tsconfig, migrations, env typing, http helpers) + healthz.
2. DB repos + auth (signup/login/me/logout) — §3 auth block green in tests.
3. Pins/cart/hidden/listing endpoints (pure D1).
4. Media serving from R2 + importer service + cron chunking (use demo Pinterest fixtures in tests; real handle in smoke).
5. Analysis pipeline (demo mode first — no keys), crop URL builder, Lens providers.
6. Shop: classify → shopify → options → plan → looks (+ affiliate links, merchants.json, shipping warnings).
7. Rate limits, logging polish, README runbook, deploy + device E2E.

## 13. Risks / open items

- **Pinterest scraping from Cloudflare IPs** may hit different bot defenses than a residential Mac. Mitigations: keep the exact browser headers/UA and backoff the Go client uses; if blocked, route discovery through the existing fallback chain (boards) and surface `rate_limited` (already a retryable, user-visible state). Worst case: demo mode uses a fixtures table — decide only if it actually blocks.
- **Images transformations details** (zone-relative vs absolute `url=`, `crop=` param exact form, `f=auto` on JPEG source) — validate with one real Lens fetch early (step 5); the §6.2 ladder covers failure.
- **Cron minute granularity** delays job start by up to ~60 s. Client polling tolerates it (§9); if unacceptable later, add the `waitUntil` fast path (start analysis in the request when the queue is empty — 30 s cap covers Gemini+Lens typically).
- **D1 single-region write latency** (~few ms/stmt): fine for this workload.
- **workers.dev-only deploys** (no custom domain): everything works except transformations — analysis falls back to ladder step (2). Get the domain early; it's the one external prerequisite.

## 14. Out of scope

Modifying `server/` or `ios/`. Migrating existing local data. Queues/Workflows/Durable Objects (unnecessary — D1 + cron suffices, and Queues/DO-with-storage have paid-plan or complexity costs). Any client-side cropping. Multi-region/read replicas. Real Pinterest API partnership.
