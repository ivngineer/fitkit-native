# Fitkit — Employee Workspace (front-end)

React 19 + TypeScript + Vite + React Router, UI on **shadcn/ui** (Radix + Tailwind CSS v4, components in `src/components/ui`), icons from lucide-react, toasts from sonner. Grey theme, dark by default with a light option. Interface in Ukrainian. Data comes from the Fitkit server in [`../server`](../server), which also serves the iOS app; orders placed with “Купити” in the app arrive here as new requests.

## Run

```bash
npm install
npm run build                     # dist/, served by the Fitkit server
cd ../server && FITKIT_WEB_DIR=../agent-workspace/dist FITKIT_BOOTSTRAP_ADMIN_EMAIL=you@fitkit.space FITKIT_BOOTSTRAP_ADMIN_PASSWORD=change-me-please ./run.sh
```

Open http://localhost:8080. For development keep the server running and use `npm run dev` here (http://localhost:5173, `/api` and `/media` proxied to :8080). Demo data: `ALLOW_SEED=true go run ./cmd/fitkitd seed-workspace` in `../server` (`anna@fitkit.space` / `fitkit-demo`).

## How it talks to the API

- `src/api/client.ts` — typed `fetch` wrapper; the session is an HttpOnly cookie, so requests are same-origin through the Vite proxy (in production serve the front-end and the API under one domain). Amounts: the API uses kopiyky, the UI hryvnias — converted in this file.
- `src/store/AppStore.tsx` — loads `/api/bootstrap` after sign-in; every action is one API call and the server re-checks the rules (server messages are shown as toasts). Live updates come over Server-Sent Events (`/api/events`): when someone else changes an order, a parcel or a ticket, only that object is refetched; after a reconnect everything is reloaded.
- Screens: sign-in, “server unavailable” with retry, and the five workspace pages. Sign out is in Settings.

## Screens

| Route | Screen | What works |
| --- | --- | --- |
| `/orders` | Current orders (default page) | Board *В роботі · Чекає підтвердження · Викуплено · Відправлено*, drag-and-drop with the same guards as the card, *Мої / Усі* |
| `/requests` | Requests — hidden unless **Settings → Вкладка «Заявки»** is on | Queue of new requests (no client column), *All / New / Overdue*, live timers, **Take next request** (oldest first) |
| `/orders/:id` | Request card | Left (narrow): items with checks, store price and rules (≤5% covered, >5% confirmation, sold out → refund); delivery block with city, Nova Poshta branch, recipient, phone and items in large bold text, each with a copy button, plus *Copy all*. Right (wide): owner, due, what the client sees, money, next-step button (blocked until every item is resolved), **Problem**, **Team notes**, cancel, history |
| `/clients` | Clients & support | Ticket list, thread, quick replies, reply as Fitkit Support, close ticket, client details with copy buttons |
| `/tracking` | Tracking | Both legs, tabs, problems first, timeline, add tracking number, **Прийнято на складі** (warehouse receipt), **Create shipment & ship** (once every parcel is at the warehouse — the board enforces the same rule) |

**Settings** (user block at the bottom of the sidebar): theme light/dark, show the Requests tab (stored in the browser), sign out.

The top search finds an order number, a tracking number, a client name or phone. The bell lists what needs attention (overdue requests, problems, open tickets, stuck parcels) and links to each.

New shadcn/ui components can be added with `npx shadcn@latest add <name>` (config in `components.json`).

## Structure

```
src/
  domain/      types.ts (entities), rules.ts (price/sold-out rules, client-facing status, money), format.ts
  api/         client.ts — API calls and kopiyky ⇄ hryvnias conversion
  data/        quickReplies.ts — support quick replies
  store/       AppStore.tsx — server state, actions, live updates, sign-in; Settings.tsx — theme, Requests tab
  components/  ui/ (shadcn/ui: button, badge, card, input, textarea, tabs, table, dialog, alert-dialog, dropdown-menu, tooltip, …),
               shared.tsx (Photo, Thumbs, Avatar, StatusDot, FilterTabs, CopyButton), Layout.tsx (sidebar, search, notifications)
  pages/       LoginPage, RequestsPage, OrderPage, BoardPage, ClientsPage, TrackingPage
  index.css    Tailwind + shadcn theme tokens (neutral, dark)
public/assets/ logo, fonts, garment photos
```

## API

Endpoints, webhooks and auth are documented in [`../docs/onboard.md`](../docs/onboard.md) (API section).
