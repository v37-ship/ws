# Context.md — Wangga Spa system reference

> Snapshot of the codebase structure and data flows. Verify against code
> when in doubt, and update this file with structural changes (same
> discipline as AGENTS.md). For agent operating rules, see AGENTS.md.

## Boot + request lifecycle

`cmd/server/main.go`: `config.Load` → `db.Migrate` (Goose) → `db.Connect`
→ repos → services → `os.MkdirAll(UPLOAD_DIR)` → `EnsureAdmin` +
`SeedDemo`/`SeedTestimonials`/`SeedFood` (all seed-if-empty) →
`handlers.New` (parses every template with the production `FuncMap`) →
chi router (`RequestID`, `RealIP`, `Logger`, `Recoverer`, `Timeout(60s)`,
`LoadUser`) → `Routes()`. A 1-minute background ticker runs
`ExpireHolds` + `DeleteExpired` sessions.

Chi router (`internal/http/handlers/server.go:Routes`) → middleware
(`LoadUser` always; `RequireAuth` / `RequireStaff` / `RequireAdmin` /
per-route `RateLimit`: login/register 20/min, book/apply 30/min) → handler
(`handlers.go`, `guest.go`, `food.go`, `gallery.go`, `promos.go`, `testimonials.go`,
`site.go`, `careers.go`, `report.go`) → service (`internal/service`) →
repo (`internal/repo`, raw SQL via pgx) → Postgres 16.

`render()` injects `Lang` (resolved per request, `?lang=` persists the
cookie), `User`, `CSRF` (cookie-based, session-independent), `Request`,
`HasDrawer` (portal/admin shell), `Contact` map, and analytics config.
`renderPartial()` serves HTMX fragments (`Vary: HX-Request`);
`renderAuto()` picks fragment vs full page by `HX-Request`. Helpers:
`toast()` (`HX-Trigger`), `redirectHTMX()` (`HX-Redirect` vs 303),
`requireCSRF()`, `requirePortalAdmin()` (admin-only actions inside shared
portal routes), `ensureCSRF()` / session cookie setters.
Template funcs: `t` (i18n), `icon` (inline SVG set, `icons.go`),
`slotLabel`/`dateLong`, `formatIDR`/`formatTime`,
`servicePhoto`/`staffPhoto`/`tmPhoto`/`foodPhoto`/`heroPhoto`/`serviceImg`,
`stars`, `waLink`/`waURL`/`waInquire`, `navParts`, `eq`/`add`/`hasUUID`/
`dict`/`csrf`, `static` (fingerprinted `?v=` URLs).

Polling partials: pay status `GET /bookings/{id}/status` (`every 3s`,
token-aware `?t=` for guests), portal queue `GET /portal/queue`
(`every 30s`), portal bookings rows via `HX-Request` (debounced live search
+ `hx-push-url`, search input `name="q"`).

## Database (`internal/db/migrations`, 001–019, Goose)

| # | Area |
|---|------|
| 001 | users/sessions/roles, locations, services/staff/bookings/payments core |
| 002 | `cs` role (`customer`/`admin`/`cs` check) |
| 003 | ops core: users `is_active`/baby/mom/medical + phone index; booking statuses (8), staff no-overlap EXCLUDE gist, `source`, `booking_fee_idr`, created/updated-by; payments `method`/`purpose` + cash-never-booking-fee CHECK; `staff_time_off`; `booking_logs` audit trail |
| 004 | careers (jobs + applications + CV columns) |
| 005–006 | service hours + service photos, staff photos |
| 007, 010 | refunds + voiding (`voided_at` excludes from totals, row kept) |
| 008 | admin email rename (`admin@spa.local` → `admin@wangga.biz.id`, idempotent) |
| 009 | `Booking.Place` (onsite/homecare) + visit address |
| 011 | service `name_id`/`description_id` |
| 012 | testimonials + `users.photo_url/photo_path` |
| 013–014 | `food_items` (M/L prices, variants; snacks split out, pre-order removed) |
| 015 | `promo_codes` + booking `promo_code`/`discount_idr` snapshots |
| 016 | guest bookings (nullable `customer_id`, guest fields, `access_token`) |
| 017 | `hero_banners` |
| 018 | `gallery_items` (photo/video kinds, optional service link) |
| 019 | WhatsApp plumbing: `wa_outbox` (dedupe key + retry state), `wa_conversations`, `wa_messages`, `users.wa_marketing_opt_in`/`wa_opted_out_at` |

Seeds (seed-if-empty on boot): `EnsureAdmin` (from `ADMIN_EMAIL`/
`ADMIN_PASSWORD`), `SeedDemo` (branch "Wangga Kayu Putih" + services +
staff + hours), `SeedTestimonials` (10 approved reviews), `SeedFood`
(18-item Bu Nah menu in card order). Gallery starts empty (no seed —
like hero banners).

## Feature map

- **Booking wizard** — `GET /book` (+`location_id`/`place` params),
  `GET /book/services|staff|slots` (HTMX partials), `GET /book/promo`
  (quote partial, always HTTP 200), `POST /book` (rate-limited),
  `templates/pages/book.html` + `partials/slots|slot_groups|service_cards|staff_cards`.
  Same-hour slots collapse when no therapist is picked; `?place=homecare`
  pre-checks Homecare; single-branch shops auto-select the location.
  Availability = service-hours ∩ staff-hours, 10-min prep buffer, 15-min
  grid, minus time-off blockouts.
- **Guest checkout** — same wizard + URLs with `?t=` token (pay, status
  poller, cancel). `Booking.OwnedBy/TokenOK/IsGuest` centralize checks;
  `bookingAccess` gates pay/status/cancel/mock-pay. History (`/bookings`,
  `ListMine`) stays account-only.
- **Payments** — Midtrans QRIS charge at creation over the **net** amount;
  `POST /webhooks/midtrans` (sha512 verify, mock-mode bypass);
  `POST /bookings/{id}/mock-pay` (blocked when keys are set).
  Cash is services-only, never booking fees (service + DB CHECK).
  `payment_method` (`qris`/`cash`), `payment_purpose`
  (`booking_fee`/`service`/`full`).
- **Promo codes** — `GET /book/promo` quote partial (always HTTP 200);
  validated again inside both creators; usage via atomic `TryRedeem`
  (rolled back via `Unredeem` if charging fails); manager at
  `/portal/promos` (create/toggle/delete, immutable — never edit).
  Rules: uppercase, percent 1–100 or fixed IDR, min spend, optional
  validity window + use cap.
- **Food (Bu Nah)** — `GET /food` (food/beverage/snack + order info);
  homepage promo card; manager at `/portal/food` (full CRUD + photo +
  toggle). M/L prices nullable (at least one), variants list, names as-is.
- **Gallery** — `GET /gallery` (photo/video cards, kind + service filter
  chips, Alpine lightbox, click-to-play video facade); videos are Drive/
  YouTube/Vimeo links normalized to embed URLs (`VideoEmbedURL` allowlist);
  manager at `/portal/gallery` (full CRUD + photo/thumb upload, sort,
  toggle).
- **Testimonials** — homepage filmstrip (approved only); authed submit at
  `/testimonials/new` (pending moderation, ≤2 MB photo); manager at
  `/admin/testimonials` (create/status/delete).
- **Hero banners** — `Home()` loads active banners; 0 = fallback image,
  1 = static, 2+ = crossfade + dots; manager at `/portal/site`
  (upload/link, sort, show/hide); fullscreen-on-scroll-up is
  gesture-driven (`head.html`).
- **Account** — `/account` (name + avatar upload/link, ≤2 MB).
- **Portal (CS + admin, `RequireStaff`)** — `/portal` dashboard/queue,
  bookings (list/search/detail, walk-in create, status, reschedule, admin
  correct, refunds + void), customers (phone-first CRUD, baby/mom/medical,
  toggle-active, guarded delete), team/schedule (staff CRUD, weekly hours,
  time-off, toggle, guarded delete), services (catalog + hours + photo +
  toggle/delete), food, site banners, promos, reports + CSV.
- **Admin only (`RequireAdmin`)** — `/admin` home, locations, CS staff
  (create, toggle-active revoking sessions, password reset), jobs +
  applications (+CV download), testimonials, customer password reset, own
  password change. `/admin/services` → `/portal/services` and
  `/admin/staff` → `/portal/schedule` are redirects.
- **Careers** — public `/careers` list + apply (CV upload ≤5 MB
  PDF/DOC/JPG/PNG, `MaxCVBytes`); admin review under `/admin/jobs`.
- **Reports** — `/portal/reports` (+CSV): cash-basis Asia/Jakarta monthly
  revenue (gross/refunds/net, QRIS-vs-cash, by service/staff, month +
  location filters, print stylesheet). Paid payments group by `paid_at`
  month; refunds deduct in the recorded month; voided refunds excluded.
- **WhatsApp (phase 0, plumbing only)** — Evolution API v2.3 container
  (internal-only, `http://evolution:8080`); app-side outbox worker drains
  `wa_outbox` every 15s (`internal/service/wa.go`, claim with
  `FOR UPDATE SKIP LOCKED`), retries 5xx with backoff (1m/5m/15m/1h/6h) and
  fails 4xx permanently. `WA_ENABLED=false` = mock mode (render + log, mark
  sent). No booking hooks or inbound webhook yet — phase 1/2.

## Auth model

Roles: `customer`, `cs`, `admin` (`IsStaff()` = cs/admin). Native Postgres
sessions (no Redis): session cookie (HttpOnly, `SESSION_TTL` = 168h) +
CSRF cookie (independent of session, so guest POSTs work; Secure only in
production). Passwords are bcrypt (`internal/auth`); `EnsureAdmin`/
`EnsureCS` bootstrap staff. CS toggle-active revokes sessions; password
changes revoke the owner's sessions. Guest bookings carry `access_token`
(nullable unique, revocable, constant-time `TokenOK`); history
(`/bookings`, `ListMine`) stays account-only. Login/register/apply/book
are per-IP rate-limited (20/20/30/30 per minute → 429).

## Assets, uploads, integrations

- Templates (54 files: layouts 3, pages 11, partials 12, admin 9, portal 19)
  + `static/` are embedded (`//go:embed`); `staticVersion` (sha256 of all
  static assets, 12 hex chars) appends `?v=` — versioned URLs serve
  immutable long-cache; unversioned serve normally. Rebuild to ship
  text/markup/CSS changes. Vendored `htmx.min.js` + `alpine.min.js`;
  brand `img/logo.png` (header/footer lockup) + `img/favicon.png` /
  `img/apple-touch-icon.png` (mom-baby crop).
- UI icons are inline SVG via the `icon` template func (`icons.go`,
  Lucide-style stroke, `currentColor`); never emoji in UI chrome
  (`TestNoEmojiIcons` scans every page/partial in both languages).
  `app.js` handles `HX-Trigger` toasts, `?toast=` flash fallback, and the
  non-Alpine slot-picker fallback. HTMX config: 204/`[23]..` swap, `[45]..`
  no-swap (hence 200-with-inline-errors on preview/quote endpoints).
- Uploads (gitignored, `./uploads` → `/app/uploads` volume, chown
  `nobody` via `deploy-local.sh`): `services/`, `staff/`, `testimonials/`,
  `avatars/`, `food/`, `hero/`, plus career CVs at the root; served by
  traversal-safe `serveUploadDir` handlers mirroring `ServeServicePhoto`.
  Photos ≤2 MB JPG/PNG/WebP; CVs ≤5 MB PDF/DOC/JPG/PNG.
- Midtrans QRIS (`MIDTRANS_*`; empty = mock mode) with notification URL
  pointed at `/webhooks/midtrans` from the dashboard.
- Outbound mail via Gmail relay (`SMTP_*`; empty = Mailpit catcher on
  `localhost:1025`); inbound via Cloudflare Email Routing to Gmail.
- Umami analytics (`ANALYTICS_*`; empty = tracker off) reusing Postgres
  (`docker/postgres-init` creates the `umami` DB; tracker renders only when
  URL + site ID are both set; hostname must match the Website domain).
- Production since Oct 2026: **VPS `31.97.48.59` (AlmaLinux, gelatto)** —
  Cloudflare proxied A record → Traefik edge (80/443, `~/docker/portainer-traefik`,
  cert resolver `wangga` = Cloudflare DNS-01 token in its `.env`) → app
  container (no published ports; loopback `127.0.0.1:8088` for admin/debug).
  Prod file `docker-compose.vps.yml` (no Mailpit/Caddy; no published
  Postgres/Umami app ports; real `${SMTP_*}` from `~/ws/.env`). Ship via
  `./scripts/deploy-vps.sh` (pull + rebuild + health wait); nightly backups
  via `scripts/backup-vps.sh` (cron 03:15, `~/backups`, 14 kept). Local box
  is dev-only (`deploy-local.sh`); its old cloudflared connector is the
  rollback path (tunnel CNAME was replaced by the A record). Mailpit
  unchanged on this box.
- Umami is public on the VPS (Oct 2026): `umami.wangga.biz.id` A record
  (proxied) → Traefik router `umami` with BasicAuth `${UMAMI_BA_USER}:
  ${UMAMI_BA_HASH}` (VPS `~/ws/.env`; `$` doubled in the hash) on **pages
  only**. A lower-priority `umami-tracker` router keeps `/script.js` and all
  of `/api/*` outside BasicAuth — the tracker must be anonymous, and the SPA
  sends `Authorization: Bearer <token>` on every API call, which cannot
  share the header with BasicAuth (BasicAuth-gating `/api` made the
  dashboard spin forever after login; `basicauth.removeheader=false` is not
  a fix — Traefik still has to *read* the Basic credential). API safety =
  Umami's own token auth + `LOGIN_MAX_FAILED_ATTEMPTS`.
  **The tracker itself is first-party**: `ANALYTICS_URL=https://wangga.biz.id/_u`
  and Traefik router `umami-apex` (`Host(wangga.biz.id) && PathPrefix('/_u/')`,
  priority 300, `stripPrefix /_u`, service `umami`) maps `/_u/script.js` and
  `/_u/api/send` onto Umami. Third-party analytics hosts are dropped by ad
  blockers (uBO) for a large share of phone visitors — scripts were never
  even requested — so the tag must stay on the main origin with a neutral
  path. `DISABLE_BOT_CHECK=1` because isbot false-positives silently discard
  real phone visits (HTTP 200 `{"beep":"boop"}`, nothing stored). Full
  history restored (source DB also froze 2026-09-21, so nothing was lost in
  the cutover).
