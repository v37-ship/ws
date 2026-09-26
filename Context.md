# Context.md — Wangga Spa system reference

> Snapshot of the codebase structure and data flows. Verify against code
> when in doubt, and update this file with structural changes (same
> discipline as AGENTS.md). For agent operating rules, see AGENTS.md.

## Request lifecycle

Chi router (`internal/http/handlers/server.go:Routes`) → middleware
(`LoadUser` always; `RequireAuth` / `RequireStaff` / `RequireAdmin` /
per-route `RateLimit`) → handler (`handlers.go`, `food.go`, `promos.go`,
`testimonials.go`, `site.go`, `careers.go`, `report.go`) → service
(`internal/service`) → repo (`internal/repo`, raw SQL via pgx) →
Postgres 16.

`render()` injects `Lang` (resolved per request), `User`, `CSRF`
(cookie-based, session-independent), `Contact` map, and analytics config.
Template funcs of note: `t` (i18n), `slotLabel`/`dateLong`,
`servicePhoto`/`tmPhoto`/`foodPhoto`/`heroPhoto`, `stars`, `waLink`,
`static` (fingerprinted `?v=` URLs).

## Database (`internal/db/migrations`, 001–017, Goose)

| # | Area |
|---|------|
| 001–003 | users/sessions/roles, locations, services/staff/bookings/payments core |
| 004 | careers (jobs + applications) |
| 005–006 | service hours, service/staff photos |
| 007, 010 | refunds + voiding |
| 008 | admin email rename |
| 009 | `Booking.Place` (onsite/homecare) + visit address |
| 011 | service `name_id`/`description_id` |
| 012 | testimonials + `users.photo_url/photo_path` |
| 013–014 | `food_items` (M/L prices, variants, pre-order removed in 014) |
| 015 | `promo_codes` + booking `promo_code`/`discount_idr` snapshots |
| 016 | guest bookings (nullable `customer_id`, guest fields, `access_token`) |
| 017 | `hero_banners` |

Seeds (seed-if-empty on boot): `SeedDemo` (branch + services + staff),
`SeedTestimonials` (10 approved reviews), `SeedFood` (18-item Bu Nah menu).

## Feature map

- **Booking wizard** — `GET /book` (+`location_id`/`place` params),
  `GET /book/services|staff|slots` (HTMX partials), `POST /book`,
  `templates/pages/book.html` + `partials/slots|slot_groups|service_cards|staff_cards`.
  Same-hour slots collapse when no therapist is picked; `?place=homecare`
  pre-checks Homecare; single-branch shops auto-select the location.
- **Guest checkout** — same wizard + URLs with `?t=` token (pay, status
  poller, cancel). `Booking.OwnedBy/TokenOK/IsGuest` centralize checks.
- **Payments** — Midtrans QRIS charge at creation over the **net** amount;
  `POST /webhooks/midtrans`; mock-pay when keys are empty (dev).
  Cash is services-only, never booking fees.
- **Promo codes** — `GET /book/promo` quote partial (always HTTP 200);
  validated again inside both creators; usage via atomic `TryRedeem`
  (rolled back if charging fails); manager at `/portal/promos`.
- **Food (Bu Nah)** — `GET /food` (food/beverage/snacks + order info);
  homepage promo card; manager at `/portal/food` (full CRUD).
- **Testimonials** — homepage filmstrip (approved only); authed submit at
  `/testimonials/new` (pending moderation); manager at
  `/admin/testimonials`.
- **Hero banners** — `Home()` loads active banners; 0 = fallback image,
  1 = static, 2+ = crossfade + dots; manager at `/portal/site`;
  fullscreen-on-scroll-up is gesture-driven (`head.html`).
- **Account** — `/account` (name + avatar upload/link).
- **Portal (CS + admin, `RequireStaff`)** — `/portal` dashboard/queue,
  bookings (incl. walk-in create), customers, team/schedule, services,
  food, site banners, promos, reports + CSV.
- **Admin only (`RequireAdmin`)** — `/admin` home, locations, CS staff,
  jobs + applications (+CV download), testimonials, customer password
  reset, own password change. Services/staff redirect to portal pages.
- **Careers** — public `/careers` list + apply (CV upload ≤5 MB);
  admin review under `/admin/jobs`.
- **Reports** — `/portal/reports` (+CSV): cash-basis Asia/Jakarta monthly
  revenue (gross/refunds/net, QRIS-vs-cash, by service/staff); refunds
  deduct in the recorded month.

## Auth model

Roles: `customer`, `cs`, `admin` (`IsStaff()` = cs/admin). Session cookie
+ CSRF cookie (independent of session, so guest POSTs work). Guest
bookings carry `access_token` (nullable unique, revocable); history
(`/bookings`, `ListMine`) stays account-only. Passwords are bcrypt
(`internal/auth`).

## Assets, uploads, integrations

- Templates + `static/` are embedded (`//go:embed`); `?v=<hash>` covers
  all static assets — rebuild to ship text/markup/CSS changes.
- Uploads (gitignored, `./uploads` → `/app/uploads` volume): `services/`,
  `staff/`, `testimonials/`, `avatars/`, `food/`, `hero/`, plus career CVs
  at the root; served by traversal-safe handlers mirroring
  `ServeServicePhoto`.
- Midtrans QRIS (`MIDTRANS_*`; empty = mock mode) with notification URL
  pointed at `/webhooks/midtrans` from the dashboard.
- Outbound mail via Gmail relay (`SMTP_*`; empty = Mailpit catcher);
  inbound via Cloudflare Email Routing to Gmail.
- Umami analytics (`ANALYTICS_*`; empty = tracker off) reusing Postgres.
- Cloudflare Tunnel serves this machine's `:8088` (= public site);
  Caddy (`:8089`) is a local reverse-proxy sidecar only.
