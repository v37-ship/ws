# AGENTS.md — Wangga Spa Baby, Mom, & Woman

Booking app for the spa. Go 1.26 + Chi + `html/template` (embedded) + vendored
HTMX + Alpine.js + Tailwind browser CDN + Postgres 16 + Goose + Midtrans QRIS.

## Layout

- `cmd/server` — entrypoint: `config.Load` → Goose migrate → repos →
  services → `EnsureAdmin` + `SeedDemo`/`SeedTestimonials`/`SeedFood` →
  `handlers.New` (parses templates) → chi router + middleware → `Routes()`.
  A 1-minute ticker expires booking holds and deletes expired sessions.
- `internal/config` (env defaults: `SESSION_TTL=168h`, `BOOKING_HOLD_TTL=15m`),
  `internal/auth` (bcrypt, CSRF token), `internal/db/migrations` (Goose
  001–018, single source of truth), `internal/domain` (models + helpers
  `OwnedBy`/`TokenOK`/`IsGuest`/`Net`/`DisplayName`), `internal/repo` (raw
  SQL via pgx: user/session/catalog/booking/payment/promo/career/
  testimonial/food/hero/report), `internal/service` (Auth, Catalog,
  Availability, Booking, Report, Career, Testimonial, Food, Hero, Promo +
  validation), `internal/i18n` (EN+ID maps, ~795 keys each), `internal/email`
  (SMTP sender), `internal/payment` (Midtrans QRIS client),
  `internal/whatsapp` (Evolution client, phone normalization, message copy).
- `internal/http/handlers` — `Routes()` + `render`/`renderPartial`/`renderAuto`
  + `templates/` (54 files: layouts 3, pages 11, partials 12, admin 9,
  portal 19) + `static/` (vendored `htmx.min.js`/`alpine.min.js`, `app.js`,
  `app.css`/`modern.css`, `svc-*.svg`).
- `internal/http/htmx` (`IsRequest`, `Trigger`/`Toast`, `Redirect`, `Refresh`,
  `Retarget`, `Reswap`), `internal/http/middleware` (`LoadUser`,
  `RequireAuth`/`RequireStaff`/`RequireAdmin`, `RateLimit`).
- `docker/` (Caddyfile, postgres-init), `scripts/`, `uploads/` (gitignored
  runtime data), `.github/workflows/ci.yml`.

## Commands

- Verify like CI before pushing: `./scripts/ci-local.sh`
  (gofmt check, `go vet`, `go test -race -count=1`, `go build`, docker build,
  compose config). `make ci` runs the same script. `make test`/`vet`/`fmt`
  run the Go steps individually.
- Ship: `./scripts/deploy-local.sh` (rebuilds, `mkdir uploads` + chown to
  `65534:65534` for the `nobody` app user, restarts app, waits ~3 min for
  health, smoke-tests). `APP_URL` overrides the target (default
  `http://localhost:8088`).
- Local Go run: `docker compose up -d postgres mailpit && make run`.
- Smoke: `make smoke` (healthz + readyz).
- Ports: app **8088**→8080, caddy proxy **8089**→80, mailpit UI **8025** /
  SMTP **1025**, umami **8030**, postgres **5432**.
- Env TTLs: `BOOKING_HOLD_TTL=15m` (unpaid hold), `SESSION_TTL=168h` (7 days).
  Compose bakes env at container creation — follow `.env` edits with
  `docker compose up -d app`.

## Auth, sessions, rate limits

- Roles `customer`/`cs`/`admin`; `IsStaff()` = cs/admin. Session cookie is
  HttpOnly SameSite=Lax with `SESSION_TTL`; CSRF cookie is separate (24h,
  non-HttpOnly, Secure only when `APP_ENV=production`). `requireCSRF` guards
  all mutating POSTs; guest POSTs work because CSRF is session-independent.
- `LoadUser` runs on every request. `RequireAuth` redirects to
  `/login?next=...`; `RequireStaff`/`RequireAdmin` return 403 for the wrong
  role. `requirePortalAdmin()` enforces admin-only corrections inside the
  shared `/portal` routes (all portal routes admit CS too).
- Rate limits per IP: login/register 20/min, book/apply 30/min → HTTP 429.
- Single app replica only (native Postgres sessions, no Redis).

## Payments & mail

- `PAYMENT_MODE=qris_manual` (default): a static merchant QRIS is shown on the
  pay page, the booking is confirmed immediately with no hold, and staff
  verify the transfer receipt in the portal (`payments.customer_reported_at`
  → confirm QRIS/Tunai). Receipts are uploaded by the customer or staff
  (`uploads/receipts/<booking>/`, staff-only serving route); the QR is
  replaceable at `/admin/payments` (fallback: embedded
  `static/img/qris-wangga.jpeg`). Customer copy says pay now or at the counter.
- `PAYMENT_MODE=midtrans` keeps the dormant gateway checkout: Midtrans QRIS is
  charged at booking creation over the **net** amount
  (`PriceIDR - DiscountIDR`). Empty `MIDTRANS_SERVER_KEY` = mock mode
  (`MOCK-QRIS|order=...` + Simulate-payment button); `MockPay` is blocked
  when keys are set. Webhook `POST /webhooks/midtrans`; signature check is
  `sha512(order+status+gross+key)`, skipped in mock mode.
- Cash rule (service + DB CHECK constraint): cash is services-only at the
  counter, never for booking fees. Staff cash bookings confirm immediately.
- Mail via `email.Sender`: no `SMTP_USER` = plain unauthenticated SMTP
  (Mailpit `localhost:1025`); with user/pass = PLAIN over STARTTLS (Gmail
  `smtp.gmail.com:587` App Password). From `halo@wangga.biz.id`; inbound via
  Cloudflare Email Routing to Gmail.

## WhatsApp (Evolution API)

- Self-hosted `evolution` service (`evoapicloud/evolution-api:v2.3.7`,
  Baileys) on the **default compose network only**: no Traefik labels, no
  public port. The VPS loopback `127.0.0.1:8081` exists for QR pairing over an
  SSH tunnel. Own Postgres DB `evolution` (init script for fresh clusters +
  one-time `CREATE DATABASE` on an existing volume); Baileys auth state lives
  in the `evolution_instances` volume.
- App side: `internal/whatsapp` (client: `SendText`/`ConnectionState`/
  `SetWebhook`; `NormalizePhone` → `628…`; `Render` template registry),
  `internal/repo/wa.go` (outbox), `internal/service/wa.go` (15s worker,
  `FOR UPDATE SKIP LOCKED`, 4xx = permanent failure, 5xx/timeout backoff
  `1m/5m/15m/1h/6h`, ≤10 per batch, `WA_SEND_RATE` pacing). **Enqueue only** —
  never send inline from an HTTP handler or webhook path.
- Env: `WA_ENABLED` (false = mock: render + log + mark sent, so dev/CI and an
  unpaired box keep working), `WA_API_URL` (`http://evolution:8080`),
  `WA_API_KEY` (Evolution `AUTHENTICATION_API_KEY`), `WA_INSTANCE`,
  `WA_WEBHOOK_SECRET`, `WA_REMINDER_HOUR=18` (WIB), `WA_SEND_RATE=1s`,
  `GOOGLE_REVIEW_URL` (direct "write a review" link used by the post-visit
  message; `https://g.page/r/CTLPrFudX25eEBM/review`).
- Pairing: `ssh -L 8081:127.0.0.1:8081 vps`, then `POST
  /instance/create` (or `/instance/connect/wangga`), render the QR and scan it
  from the dedicated phone. The phone is a linked device: keep it online
  (WhatsApp Web rules) and warm the number up slowly.
- Phase 1 (manual-payment slice, done): `booking_confirm` (with pay link),
  `payment_receipt`, `staff_paid_claim` (group alert), `claim_rejected`, and
  free-text staff resends from the booking detail page. Automatic customer
  sends honour `WA_CUSTOMER_ENABLED` (default false); staff-triggered sends
  always enqueue. Remaining phase 1: cancel/reschedule/expire, H-1 reminder,
  review request. Phase 2: inbound webhook + `/portal/inbox`; phase 3
  automation/campaigns. Evolution is **unofficial** (ban risk): transactional
  only, opt-in for marketing, keep `wa.me` + email as fallbacks.
- Backups include the `evolution` DB dump and the `evolution_instances`
  volume; `deploy-vps.sh` prints the instance state but never fails on it.

## i18n — hard rules

- Every key must exist in **both** EN and ID maps (`TestDictParity` fails
  otherwise). Prefer value-only edits; keep keys **≤12 chars**.
- Default language is **ID**. Resolution: `?lang=` > `wangga-lang` cookie
  (1 year) > Accept-Language (EN only when ID is absent) > ID. `T()` falls
  back EN → key. `normalize` treats `id*`/`in*` as ID, `en*` as EN.
- Helpers: `WeekdayShort` (`p.dw.0-6`), `MonthShort` (`r.ms.01-12`),
  `SlotLabel`, `DateLong` (`p.day.*`, `r.m.*`). All render in WIB.
- Brand name stays identical in both languages. Error strings (`.Error`)
  stay English. User-facing copy is translated.

## Templates — embedded, rebuild to verify

- Templates and static assets are compiled into the binary. Text/markup
  changes are invisible until you rebuild + `deploy-local.sh`.
- `TestTemplatesBothLanguages` renders every page in both languages: copy
  changes can break its assertions — update them (e.g. the slogan change
  updated the `home.html` hero assertions).
- `staticVersion` is a sha256 of **all** embedded static assets (12 hex
  chars); `static` appends `?v=<hash>` and versioned URLs serve
  `Cache-Control: public, max-age=31536000, immutable`. Compare hashes to
  check what a deployment actually serves.
- UI icons are inline SVG via `{{icon "name"}}` (`icons.go`, Lucide-style
  stroke, `currentColor`); never emoji in UI chrome — `TestNoEmojiIcons`
  fails otherwise (it scans every page/partial in both languages, ignoring
  scripts and link targets). Exempt: text glyphs (★☆✓✕☰), the JS theme map,
  and WhatsApp message content. Icon markup lives in templates, never in
  dict values.

## Uploads

- `UPLOAD_DIR` is `./uploads` locally → `/app/uploads` volume. Served by
  traversal-safe `serveUploadDir` (`path.Base`) at
  `/uploads/services|staff|testimonials|avatars|food|hero/{file}`; career
  CVs sit at the uploads root, downloadable by admin only.
- Photos ≤2 MB JPG/PNG/WebP (`ValidatePhotoName`, uuid storage name);
  picture links must be `http(s)` image URLs (`ValidateImageURL`). CVs ≤5 MB
  PDF/DOC/JPG/PNG (`ValidateCVName`, `MaxCVBytes`). Multipart forms parse
  10 MB (`ParseMultipartForm(10 << 20)`).

## HTMX + handler gotchas (learned the hard way)

- HTMX preview/quote endpoints must **always answer 200 with inline errors**:
  the app's HTMX config (`[45].. → swap:false`) silently swallows 4xx/5xx,
  making buttons look dead (precedent: promo Apply).
- `hx-include` submits elements under their **`name` attribute**: the input
  name must equal the query param the endpoint reads (`promo_try` vs `code`
  cost a full debug cycle; customer picker `name="q"` is asserted by
  `TestTemplatesExecute`). Accept both names server-side when two forms
  share an endpoint.
- `renderAuto` serves a fragment for `HX-Request` and a full page otherwise
  (filter/search endpoints). Toasts go over `HX-Trigger: {"toast": ...}`
  (consumed by `app.js`); redirects use `HX-Redirect`, refreshes
  `HX-Refresh` — never a full form post from HTMX buttons.
- Honest status codes on fetch handlers: `ErrNotFound` → 404, anything else
  → log + 500. Blanket `err → NotFound` turned transient blips into ghost
  "missing booking" reports (precedent: `PayPage`, `PortalBookingDetail`).

## Booking lifecycle

- Statuses: `pending_payment`/`confirmed`/`checked_in`/`in_service`/
  `completed`/`cancelled`/`expired`/`no_show`. Staff no-double-book is a
  Postgres EXCLUDE gist over the four active statuses. Unpaid holds expire
  after `BOOKING_HOLD_TTL` via the 1-minute ticker.
- Cancel: unpaid cancels free; paid shows a WhatsApp refund button with the
  reference prefilled (reason logged for CS). Reschedule rejects past times
  and overlaps. `CorrectDetails` is admin-only, logs old→new, flags
  `priceBelowPaid`.
- Refunds are validated (never more than paid-minus-refunded) and recorded
  from booking detail; `VoidRefund` is an admin correction that keeps the
  row (`voided_at` excludes it from totals). See cash-basis rule below.
- Slots respect service-hours ∩ therapist-hours with a 10-min prep buffer
  on a 15-min grid, minus `staff_time_off` blockouts. `Source` is
  `web`/`walkin`/`whatsapp`/`phone`; `booking_fee_idr` is stored per booking.

## Deploy topology

- Production lives on a **VPS (`31.97.48.59`, AlmaLinux, `gelatto`)**:
  Cloudflare proxied A record → Traefik edge (80/443; ` ~/docker/portainer-traefik`
  compose with resolver `wangga` = Cloudflare DNS-01, token in its `.env`,
  CF-IP `forwardedHeaders` trust) → app (no published ports; loopback
  `127.0.0.1:8088` only for admin/debug). This machine is dev-only.
- Ship: `./scripts/deploy-vps.sh` (git pull + `docker compose -f
  docker-compose.vps.yml up -d --build app` + health wait). Prod runtime
  file is `docker-compose.vps.yml` (no Mailpit/Caddy, no published
  Postgres/Umami ports; real `${SMTP_*}`, `ANALYTICS_URL=https://wangga.biz.id/_u`
  — Traefik `umami-apex` router maps `/_u/*` on the apex to Umami, so the
  tracker is first-party and survives ad blockers).
- Verify with `curl https://wangga.biz.id/` (compare `app.css?v=` or
  content markers), plus `docker logs traefik --since 10m` on the VPS.
- Backups: `scripts/backup-vps.sh` via VPS crontab 03:15 (pg_dump spa+umami,
  tar uploads) into `~/backups`, 14 days kept — restore = drop schema +
  `psql` the dump + `git pull`.
- Single stack, single DB, single app replica. Throwaway SQL ops still stay
  uncommitted. Rollback path: this box's cloudflared connector is still
  installed (idle) — re-pointing the A record back via the CF API/dashboard
  restores the old serving chain.
- Docker: multi-stage build, image `HEALTHCHECK` on `/healthz`, non-root
  `nobody`. The dev stack still runs Caddy (`:8089`) + Mailpit locally —
  localhost-only by design, never published on a server.
- `GET /healthz` (compose/docker healthcheck) + `GET /readyz`. All compose
  services use `restart: unless-stopped`. SSH on the VPS must be key-only
  for `gelatto` (`sshd_config.d` hardening; verify with
  `sudo sshd -T | grep -Ei 'passwordauthentication|permitrootlogin'`).

## Domain decisions (do not "simplify" these)

- Homecare is `Booking.Place` (`onsite`/`homecare`), never a second Location.
  `VisitAddress` is required for homecare. `?place=homecare` pre-checks the
  wizard's Homecare radio (homepage Homecare card links there). Key prefix:
  `hc.*`.
- Homecare transport fee: the customer opens a prebuilt Google Maps
  directions link (branch address → their address, no geocoding/route API)
  and picks a distance band; the server maps the band to a fee from
  `distance_zones` and never trusts a posted amount. Bands are one-way
  distance rounded up, fees cover the round trip (poster: 0–1 km Rp5k,
  2–3 Rp10k, 4–5 Rp20k, 6–7 Rp25k, 8–9 Rp30k, 10–12 Rp50k; seeded once by
  `SeedDistanceZones`, editable at `/portal/distance`). `PriceIDR` stays the
  treatment price (promos/promo min-spend still use it); the fee is a
  booking snapshot (`distance_fee_idr`, `distance_km` = band upper bound) and
  `Booking.Net()` = price + transport − discount. No active bands = feature
  off (no picker, fee 0). Beyond the last band is WhatsApp only; admins may
  override the band in the correction form with a logged reason. Key prefix:
  `p.dz.*`, `p.nav.dz`.
- Contact/social links follow the `CONTACT_*` pattern: env default in
  `config.go` → `Contact` map in `render()` → footer template; document in
  `.env.example`, `docker-compose.yml`, `README.md` (precedent:
  `CONTACT_INSTAGRAM`).
- `Service.Name` is canonical EN; `name_id`/`description_id` are optional
  (empty = same as canonical via `DisplayName`/`DisplayDescription`).
- Bookings snapshot `ServiceName`; reports group by it, history untouched.
- WA slips use the canonical Name.
- `BookPage` auto-selects the location radio when exactly one active branch
  exists; falls back to manual pick otherwise.
- Reports are cash-basis Asia/Jakarta: revenue from paid payments by
  `paid_at` month, refunds deduct in the recorded month, cancelled-but-paid
  counts until refunded.
- Testimonials are moderated: `pending`/`approved`/`hidden`, only approved
  renders (homepage Alpine filmstrip: drag-to-slide, dots, no arrows).
  Entries snapshot name/photo/message (account edits never rewrite reviews);
  messages display as-written in both languages (no `message_id`).
  Launch seeds live in `SeedTestimonials` (seed-if-empty, like `SeedDemo`).
  Key prefixes: `tm.*` (section/form), `acc.*` (account), `a.tm.*` (admin).
- `/account` is the self-service profile page (name + avatar upload/link
  under `uploads/avatars/`); header user chip links there. Review uploads go
  under `uploads/testimonials/`, served by traversal-safe handlers mirroring
  `ServeServicePhoto`.
- Bu Nah food: `food_items` in food/beverage/snack categories, nullable M/L
  prices (at least one), optional variants list, names display as-is (no
  `name_id`). Portal CRUD for CS + admin. Ordering is onsite / WhatsApp /
  free Kayu Putih delivery — never online payment. The real menu lives in
  `SeedFood`. Key prefixes: `fd.*` (page), `p.fd.*` (manager),
  `p.nav.food`.
- Gallery: `gallery_items` in photo/video kinds, optional `service_id` link
  (filter chips, NULL = standalone), names display as-is (no `name_id`).
  Photos are upload/link (≤2 MB, `uploads/gallery/`); videos are links
  only — server normalizes Drive/YouTube/Vimeo share URLs to embed URLs
  (`VideoEmbedURL`, host allowlist, unit-tested), motionless until the
  visitor presses play (click-to-load facade). Portal CRUD + sort for CS +
  admin; public page at `/gallery` (nav + footer links, no homepage
  section). Key prefixes: `gl.*` (page), `p.gl.*` (manager),
  `p.nav.gal`.
- Hero banners: `hero_banners` table; 0 rows = built-in fallback image,
  1 active = static, 2+ = crossfade slideshow with dots. Portal manager
  (upload/link, sort, show/hide). Default view is compact (54vh) with text
  overlaid on a brand-green grade (deep `#405030` + ochre `#f0a040` accent);
  decorative blobs render on the no-photo fallback only. Fullscreen moment
  expands only on an at-top upward gesture (`scrollY === 0`) or the
  View-photo toggle (`hero.view`/`hero.hide`) — scrolling never expands on
  its own. Parallax drift is clamped to the photo overscan (scale 1.25).
  Key prefixes: `p.nav.site`, `p.hb.*`.
- Booking wizard: first-available collapses identical hours to one button
  (free slot preferred); slot labels are time-only because the review row
  already carries the date. Homecare inserts its own **Visit** step (address +
  Maps check + band) between Schedule and Review, so homecare shows 4 steps
  and onsite 3; the review only appears once that step is continued.
- Promo codes: uppercase unique codes, percent/fixed, min spend, optional
  validity window and use cap. Bookings snapshot `promo_code` +
  `discount_idr` (history untouched, like `ServiceName`); payments and the
  Midtrans charge use the net amount. Usage is consumed atomically
  (`TryRedeem`, rolled back if charging fails). Codes are immutable —
  delete and recreate, never edit; never seed live codes. Manager lives in
  the portal (`/portal/promos`, CS + admin). Key prefixes: `promo.*`
  (wizard), `p.pr.*` (manager), `p.nav.promo`.
- Guest checkout: bookings may have NULL `customer_id` with identity in
  `guest_name/phone/email` + `access_token` (unique, revocable). Guests use
  the same URLs plus `?t=` (pay, status poller, cancel); anon submit shows
  guest fields in the wizard. `Booking.OwnedBy`/`TokenOK`/`IsGuest`
  centralize the checks; reports skip nil customers. History stays
  account-only (`ListMine` untouched); claim-account linking is a deferred
  phase 2. Key prefixes: `guest.*`, `book.guest`, `p.det.guest/walink`.
- Portal (`/portal`, CS + admin) owns the daily operation: dashboard/queue,
  bookings (incl. walk-in create), customers (phone-first, baby/mom/medical
  fields, toggle-active, guarded delete), team/schedule (staff CRUD, weekly
  hours, time-off blockouts, guarded removal when history exists), services
  (catalog + hours + photo + toggle), food, site banners, promos, reports +
  CSV. Admin (`/admin`) owns cross-cutting setup: locations, CS accounts
  (create, toggle-active revoking sessions, password reset), jobs +
  applications (+CV download), testimonials, customer password reset, own
  password change. `/admin/services` → `/portal/services` and
  `/admin/staff` → `/portal/schedule` are redirects, not pages.

## Git

- `feat:` / `fix:` / `chore:` prefixes on `main`. Inspect `git status`,
  `git diff`, recent log before committing; stage only intended files.
- Never commit `.env` or secrets. `uploads/` and local DB volumes are not
  transferable artifacts.
