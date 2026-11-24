# AGENTS.md — Wangga Spa Baby, Mom, & Woman

Booking app for the spa. Go 1.26 + Chi + `html/template` (embedded) + vendored
HTMX + Alpine.js + hand-written CSS (no utility framework) + Postgres 16 +
Goose + Midtrans QRIS.

## Layout

- `cmd/server` — entrypoint: `config.Load` → Goose migrate → repos →
  services → `EnsureAdmin` + `SeedDemo`/`SeedTestimonials`/`SeedFood` →
  `handlers.New` (parses templates) → chi router + middleware → `Routes()`.
  A 1-minute ticker expires booking holds and deletes expired sessions.
- `internal/config` (env defaults: `SESSION_TTL=168h`, `BOOKING_HOLD_TTL=15m`),
  `internal/auth` (bcrypt, CSRF token), `internal/db/migrations` (Goose
  001–025, single source of truth), `internal/domain` (models + helpers
  `OwnedBy`/`TokenOK`/`IsGuest`/`Net`/`DisplayName`), `internal/repo` (raw
  SQL via pgx: user/session/catalog/booking/payment/promo/career/
  testimonial/food/hero/report/payroll), `internal/service` (Auth, Catalog,
  Availability, Booking, Report, Career, Testimonial, Food, Hero, Promo,
  Payroll + validation), `internal/i18n` (EN+ID maps, 1172 keys each),
  `internal/email` (SMTP sender), `internal/payment` (Midtrans QRIS client),
  `internal/whatsapp` (Evolution client, phone normalization, message copy),
  `internal/integration` (build-tagged execution suite against a disposable
  Postgres; harness + first-wave groups, see Commands).
- `internal/http/handlers` — `Routes()` + `render`/`renderPartial`/`renderAuto`
  + `templates/` (75 files: layouts 3, pages 15, partials 20, admin 13,
  portal 24) + `static/` (vendored `htmx.min.js`/`alpine.min.js`, `app.js`,
  `app.css`/`modern.css`, `svc-*.svg`).
- `internal/http/htmx` (`IsRequest`, `Trigger`/`Toast`, `Redirect`, `Refresh`,
  `Retarget`, `Reswap`), `internal/http/middleware` (`LoadUser`,
  `RequireAuth`/`RequireStaff`/`RequireAdmin`, `RateLimit`).
- `docker/` (Caddyfile, postgres-init), `scripts/`, `uploads/` (gitignored
  runtime data), `.github/workflows/ci.yml`.

## Commands

- Verify like CI before pushing: `./scripts/ci-local.sh`
  (gofmt check, `go vet`, `go test -race -count=1`, the integration suite
  against compose Postgres, `go build`, docker build, compose config).
  `make ci` runs the same script. `make test`/`vet`/`fmt` run the Go steps
  individually — the Go-only path when the Docker engine is down.
- Integration suite (real SQL, build tag `integration`, one package
  `internal/integration`): `TestMain` drops schema `public` and replays the
  embedded Goose chain from blank, then each DB test starts with `TruncateAll`.
  Run it with a disposable DB:
  `docker compose up -d postgres`,
  `docker compose exec postgres createdb -U spa spa_test` (once),
  `TEST_DATABASE_URL=postgres://spa:spa@localhost:5432/spa_test go vet -tags=integration ./...`
  then the same env plus
  `go test -tags=integration -race -count=1 -p 1 ./...`. **`-p 1` is
  mandatory** (one shared DB, so packages must not overlap). The guard is an
  allowlist: the database must be `spa_test` or end in `_test`, and the host
  must be loopback or `postgres`; anything else hard-fails with the URL
  redacted (so `spa`, `umami`, `evolution` can never be wiped). Unset
  `TEST_DATABASE_URL` skips the DB tests with a visible message, but fails
  when `CI=true` — CI runs the same two commands against a `postgres:16-alpine`
  service with `POSTGRES_DB=spa_test`.
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
- Rate limits per IP: login/register 20/min, book/apply 30/min, review form +
  submit 30/min → HTTP 429.
- `/testimonials/new` (GET+POST) is guest-open behind that 30/min review
  limiter; submissions always land `pending` — moderation is the spam gate, not
  auth.
- Waiting-room kiosk: `GET /tv` and `GET /tv/queue` (the 30s poll) are public
  but credential-gated in-handler. The shipped path is `?t=<tv_tokens.token>`
  (constant-time `domain.TVToken.TokenOK` after a revoke-filtered lookup), and
  the token is location-bound (`location_id`), so a screen can only ever show
  its branch. Unknown and revoked tokens both render `tv_invalid.html` (minimal,
  no data, noindex). Management is admin-only at `/admin/tv` (generate, display
  the full URL, revoke); a revoke kills the TV on its next poll. Handlers never
  log the token; the chi access log does contain the full URL, the same
  established exposure as guest booking links — revoke and re-mint to retire a
  leaked URL. The token is never an auth substitute anywhere else. The second
  credential is a paired device: a bare `/tv` with nothing to verify renders the
  pairing screen — a six-digit `crypto/rand` code (zero-padded, handled as a
  string) with a 5-minute TTL and single use via `tv_pairings.consumed_at`,
  plus a 15-minute HttpOnly `tv_pending` cookie that the 4s `/tv/pair-state`
  poll presents (pending → keep showing the code; paired → Set-Cookie
  `tv_device` + `HX-Redirect: /tv`; expired → notice + start-over, stale pending
  cookie cleared). CS pairs at `/portal/tv` — code + branch + optional label,
  10/min per user, an unnamed screen falling back to `TV · <branch> · <date>`
  (60-rune cap, matching the form) — where the code is consumed atomically with
  the branch, the device minted, and only then linked; wrong, expired and
  consumed codes answer the one `tv.pr.bad` message (the form is not a code
  oracle), and the success copy is `<label> paired` (no "TV " prefix, or the
  fallback label would read "TV TV · …"). The device secret IS the pairing's
  pending key, promoted: the portal stores only its sha256 hex
  (`tv_devices.secret_hash`, constant-time compare over the hash — a deliberate
  deviation from the plaintext `tv_tokens`, since this is a long-lived browser
  credential rather than a shareable link) and the TV hands the plaintext back
  as `tv_device`
  (`<id>.<secret>`, HttpOnly, SameSite=Lax, 30d), so no secret ever travels to
  the counter and a paired screen's poll is a bare `/tv/queue` (nothing secret
  in any URL). `tvCredential` resolves in one order: a live device cookie →
  the view (last-seen stamped at most once a minute, in the UPDATE's
  predicate); else a live `?t=` token → the view; a presented-but-dead cookie
  → cleared + pairing screen, so a revoked screen re-pairs itself without a
  counter visit; no credential → pairing screen; a presented-but-bad `?t=` →
  the invalid page (a deliberately minted URL must not silently heal into a
  pairing). A lookup error is an outage, not a revocation: the invalid page
  renders with the cookie untouched, the 30s poll answers 500 so the wall keeps
  its last good rows, and a missing/dead cookie on the poll asks htmx for
  `HX-Refresh` rather than swapping the pairing screen into the queue slot.
  `/portal/tv` lists the devices (label, branch, paired-at, last-seen; CS
  read-only), `/admin/tv` the same list with an admin-only revoke that kills the
  screen on its next poll. Pairing rows are written by anonymous GETs, so `/tv`
  is rate-limited 6/min per IP and the 1-minute ticker sweeps `tv_pairings` —
  expired at once, consumed after a 10-minute grace so the TV's final poll can
  still read its pairing — keeping that write surface bounded. The same ticker
  runs `tvDevices.SweepPhantoms`: a linked device whose `last_seen` is still
  NULL a full day after `paired_at` is a screen that never finished its final
  poll (its credential can never authenticate), so the row goes; the day-long
  grace is what keeps an actively-pairing screen safe. The 4s `/tv/pair-state`
  poll carries its own 30/min per-IP limiter — an anonymous read with a forged
  pending cookie must not get unbounded DB hits, and 30/min is twice what a
  live poll costs. Migration 024 is additive and `tv_tokens` is untouched. The
  queue view is built server-side from redacted view-models (`tvRow`: WIB time,
  first name + last initial, service, therapist first name, status) in
  `tv_queue.go` — templates never see a `domain.Booking`. `tvStatusKeys` is both the status whitelist
  (confirmed/checked_in/in_service/completed) and the label set, and
  `place='homecare'` never appears; a hostile-fixture render test pins the
  whole page. Kiosk styles live in `static/css/tv.css` (fullscreen layout,
  >=28px rows, pinned light). Rotate is the default shell: it gives every slide
  the whole panel (~15s, Alpine `tvDeck`) and puts the queue in the rotation as
  one slide among the set; `?layout=queue` is the market-free board — the bands
  and the day list at full width, no deck, no slides, no QR (the split layout is
  retired: at 40% width it collapsed Bu Nah's menu grid, and every legacy
  `?layout=split` bookmark now lands on rotate). Slides are catalog prices,
  approved testimonials, Bu Nah's menu and a review QR rendered client-side
  by vendored qrcodejs v1.0.0 from `wa_settings.review_url` (slide omitted
  when empty) — catalog/marketing data only, never a booking.
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
  `WA_WEBHOOK_SECRET`, `WA_REMINDER_HOUR=18` (WIB), `WA_DIGEST_HOUR=7` (WIB),
  `WA_GROUP_BOOKING=off|web|all` (default `web`), `WA_SEND_RATE=1s`,
  `GOOGLE_REVIEW_URL` (direct "write a review" link used by the post-visit
  message; `https://g.page/r/CTLPrFudX25eEBM/review`).
- Admin config (`/admin/whatsapp`, admin-only, DB-backed like
  `payment_settings`): migration `022_wa_settings.sql` (`wa_settings` singleton
  + `wa_template_overrides`), `repo/wa_settings.go`,
  `service/wa_settings.go` (`WaConfig`, `Effective()` = DB over env, 15s cache
  + `Invalidate()`), `handlers/admin_whatsapp.go`. **Precedence: a NULL/empty
  DB column means "use the env default"**, so the row starts empty and a deploy
  changes nothing. Live-editable: reminder on/off + hour, customer messages,
  review on/off + URL, group mode + JID, digest on/off + hour, and
  `outbound_enabled` (pause-all). `WaService.Drain` skips claiming while
  paused, so the outbox keeps filling — **pauses delay, never drop**. Services
  read `Effective()` at call time (`BookingService.waCfg`, `DigestService.cfg`,
  `WaService.paused`), falling back to the static `BookingOptions` when no
  settings service is wired (tests). Secrets/transport (`WA_ENABLED`,
  `WA_API_KEY`, `WA_API_URL`, `WA_INSTANCE`, `WA_WEBHOOK_SECRET`,
  `WA_SEND_RATE`) stay env-only.
- Editable copy: `whatsapp.RenderOverride`/`ValidateOverride`/`Fields`/
  `SamplePayload`/`EditableTemplates` — customer templates only, a `{name}`
  placeholder allowlist, preview, reset-to-default. Copy is resolved at
  **delivery** time (`WaService.deliver`), so an edit also fixes already-queued
  rows. Staff group pings keep their code copy.
- Pairing: `ssh -L 8081:127.0.0.1:8081 vps`, then `POST
  /instance/create` (or `/instance/connect/wangga`), render the QR and scan it
  from the dedicated phone. The phone is a linked device: keep it online
  (WhatsApp Web rules) and warm the number up slowly.
- Phase 1 (manual-payment slice, done): `booking_confirm` (with pay link),
  `payment_receipt`, `staff_paid_claim` (group alert), `claim_rejected`,
  free-text staff resends from the booking detail page, staff-triggered
  reminder resends (kind `remind`), automatic customer lifecycle messages
  (`booking_remind` H-1 at `WA_REMINDER_HOUR` WIB, `booking_cancelled`,
  `booking_rescheduled`, `booking_expired`, `review_request` +2h after a
  completed visit); scheduling uses `UpsertScheduled`/`CancelPending`
  (never a bare `Enqueue` for a key that must move), plus the group pings
  `staff_booking` (new online booking) and `staff_cancelled`/
  `staff_rescheduled` (freed or moved slot) and the 07:00 WIB `staff_digest`
  agenda (`WA_DIGEST_HOUR`, once a day via the `digest:{YYYY-MM-DD}` key; the
  dashboard *Send agenda* button uses `digest:manual:{date}:{nano}`).
  Automatic customer sends honour `WA_CUSTOMER_ENABLED` (default false);
  staff-triggered sends always enqueue. Group pings follow `WA_GROUP_BOOKING`
  (`off`/`web` default/`all`): new-booking pings are online-only unless `all`,
  slot-freed pings fire unless `off`. Phase 2: inbound webhook + `/portal/inbox`;
  phase 3: automation/campaigns. Evolution is **unofficial** (ban risk):
  transactional only, opt-in for marketing, keep `wa.me` + email as fallbacks.
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
- The canonical dead-button remedy is `staffFail` (`handlers.go`): HTMX calls
  get the real status code + the `HX-Trigger` toast app.js consumes +
  `Reswap: none` (the last good DOM survives; a 30s poll keeps its old rows
  instead of blanking). Non-HTMX requests render `portal_error.html`
  **deliberately at 200** for 403/500 — it softens status-based monitoring on
  purpose, so future agents must not "fix" it back to the real code. Coverage
  is every staff POST, portal **and** admin (a stale CSRF token or expired
  session on an admin form is the same dead button; the shared shell's way-back
  link still points at `/portal`). Public/customer POSTs are deliberately
  outside the contract: they answer inline errors or redirect with `?error=`.
- `hx-include` submits elements under their **`name` attribute**: the input
  name must equal the query param the endpoint reads (`promo_try` vs `code`
  cost a full debug cycle; customer picker `name="q"` is asserted by
  `TestTemplatesExecute`). Accept both names server-side when two forms
  share an endpoint.
- Alpine `@htmx:*` hooks **never fire**: HTML attribute names are
  case-insensitive, so `@htmx:afterSwap.window` reaches the DOM as
  `@htmx:afterswap.window` and listens for an event htmx does not dispatch
  (`htmx:afterSwap`). Register the listener in JS (Alpine `init()`) or use
  htmx's kebab `hx-on::after-swap`. Precedent: the booking wizard's promo
  quote never updated the total.
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
- One-tap undo: `POST /portal/bookings/{id}/status/undo` restores the
  previous status derived from the audit log (`previousStatusFromLogs` — the
  newest "Status changed to X" entry must equal current, else the chain moved
  under you and undo refuses with a toast, never a silent overwrite). Every
  creation path (online, guest, CS walk-in) also writes that status row for the
  booking's initial status (`logCreationStatus` → `domain.StatusLogAction`), so
  the first manual move is undoable too; the manual-payment confirm gets its
  row from `UpdateStatus`. Undo is
  **quiet**: it writes through `applyStatus(fanOut=false)`, so it never
  messages the customer or pings the staff group; un-finishing cancels the
  pending review request (`cancelReview`), and un-cancelling tells staff the
  customer was already notified (the confirmation dedupe key is permanent —
  resend from the detail page).

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
- `/robots.txt`, `/sitemap.xml`, and `/llms.txt` are app-served (routes in
  `internal/http/handlers/server.go`, bodies in `seo.go`); Cloudflare's managed
  robots.txt must be disabled by the owner in the dashboard or it shadows the
  app's copy.
- Boot guard: `config.Validate` refuses to start when `APP_ENV=production` and
  `BASE_URL` is still the `http://localhost:8080` default (every canonical,
  og:url, sitemap, and llms.txt link would be wrong).
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
- Reports (`/portal/reports`, CS + admin) remain cash-basis Asia/Jakarta —
  revenue from paid payments by `paid_at` month, refunds deduct in the
  recorded month, cancelled-but-paid counts until refunded. **That rule is
  unchanged** by the tabbed overhaul. Four tabs via `?tab=`
  (`daily`/`revenue`/`ops`/`staff` whitelist, anything else → `revenue`), with
  the month's Gross/Refunds/Net + QRIS/cash strip pinned above the month tabs
  (`revenue`/`ops`/`staff`); the daily tab hides the month form, strip and
  delta because its panel is day-scoped and those controls would describe a
  different period, and each tab buys only its own extra read. **Daily**
  (`?date=`, WIB; malformed/future → today): completed/walk-in/no-show/cancelled
  counts (walk-ins are `source='walkin'` whatever the status), money collected
  by `paid_at` day split cash vs non-cash, and still-upcoming confirmed rows.
  **Revenue**: the by-service table, the refund ledger, and the transactions
  table paged `repo.PageSize` (25) rows — the summary and service rows always
  describe the whole month, page links keep month/tab/location, and CSV exports
  the month uncut. **Staff**: the by-staff revenue table plus utilization —
  scheduled minutes expand the weekly `staff_hours` windows over the month's
  real WIB days (month-edge partial weeks clamp) minus time-off overlap
  (overlapping time-off spans merge first, so one blockout is not subtracted
  twice); booked minutes are Σ `end_at - start_at` of `completed` visits;
  Rp/hour is Σ`price_idr` of those completed visits ÷ scheduled hours, while
  occupancy is booked minutes ÷ scheduled minutes — neither is the
  payment-based Gross column; no schedule renders "—". **Operations**: a demand block on **two date bases** — the
  funnel (bookings made/cancelled/no-shows) buckets `created_at` while the
  paid count and gross bucket `paid_at`, and the visit mix (source, homecare
  share, promo/transport) buckets `start_at`, all as WIB months; a booking can
  land in one month's funnel and another's mix. The funnel's no-show **rate**
  is visit-dated (numerator and denominator both from `start_at`) even though
  the no-show **count** stays created-dated. Rates are integer basis points
  ("—" for a zero denominator); gross paid is the summary's figure, never
  re-derived. **Trend** (current month only): month-to-date net vs the
  previous month cut at the same WIB day-of-month (a day past the previous
  month's length clamps to its last day, so on the 31st after a 30-day month
  the baseline is the whole previous month), or the "first days of the month"
  line when that baseline is ≤0; the revenue tab also draws a dependency-free
  six-month net sparkline (inline SVG, integer coordinates).
- Therapist payroll (`/admin/payroll`, admin-only, plus `/admin/payroll.csv`,
  `/admin/payroll/rates` and `POST /admin/payroll/pay`): accrual over
  **completed visits by WIB service date**, payment-blind — an unpaid completed
  visit still earns, so this is deliberately not the cash-basis revenue tab
  (which counts `paid_at`). Gross is the visit's frozen `price_idr` under base
  `list`, or `price_idr - discount_idr` under base `net` (floored at 0 per
  visit); the booking fee and transport fee never count. Refunds deduct on the
  date recorded and only for bookings still `completed` (a cancelled visit's
  refund is a logistics reversal), capped per booking per period at that
  booking's base under the resolved rate — the recorded refund line stays as
  written while the deduction stops at the cap, and the cap is not carried
  between periods, so a later refund-only period can be negative (net,
  commission and total are schema-signed). Rates live in the append-only
  `staff_comp` timeline: a raise or base change is a new row with a later
  `effective_from` (unique per `(staff_id, effective_from)`), resolved as the
  newest row with `effective_from <= period start`, so a future row queues a
  raise and rewrites no past period. Fixed pay follows its own cadence
  (`weekly`/`monthly`): weekly repeats for each contained WIB week on the month
  view and is paid once on the week view; monthly is paid once on the month
  view and is omitted with a note on the week view. Commission is
  `round(net × percent_bp / 10000)` to the nearest Rp 1,000, half away from
  zero; money is integer IDR. A therapist with work and no rate row is listed
  and flagged (zero commission, net-default base for the refund cap), never
  dropped. An **active** therapist whose resolved rate has `fixed_idr > 0` and
  whose cadence matches the view is listed even with no visits (flagged
  fixed-only) so the retainer is payable; inactive and percent-only staff are
  not invented. **Cadence is the settlement granularity**: weekly staff are paid
  only on week views and monthly staff only on month views; the other view
  shows the row with a settlement note (and, for a weekly cadence on the month
  view, how many contained weeks are already paid) and the pay handler refuses
  the mismatch. Marking a period paid recomputes it server-side (posted amounts
  are never trusted), refuses a future period and an empty row, and freezes an
  immutable `staff_payouts` snapshot; the partial unique key
  `(staff_id, period_kind, period_start) WHERE voided_at IS NULL` makes a second
  live mark "already paid", never a 500. A mistaken mark is **voided** with a
  required reason (`POST /admin/payroll/payout/void`); the voided snapshot stays
  in history, the period reads unpaid again, and it can be marked again as a
  new snapshot. Nothing is edited in place or deleted. Payroll is
  location-agnostic (a rate follows the person, not the branch) and never
  appears on `/portal`. Key prefix: `pr.*`.
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
  password change, and therapist payroll (rates, CSV, mark-paid).
  `/admin/services` → `/portal/services`,
  `/admin/staff` → `/portal/schedule` and `/admin/bookings` →
  `/portal/bookings` are redirects, not pages (the legacy admin bookings
  table was deleted; its route is a 303 under `RequireAdmin`).

## Git

- `feat:` / `fix:` / `chore:` prefixes on `main`. Inspect `git status`,
  `git diff`, recent log before committing; stage only intended files.
- Never commit `.env` or secrets. `uploads/` and local DB volumes are not
  transferable artifacts.

## Agent skills

### Issue tracker

Issues live in GitHub Issues on `v37-ship/ws` (`gh issue create`, `gh issue list`).
See `docs/agents/issue-tracker.md`.

### Triage labels

Canonical defaults: `needs-triage`, `needs-info`, `ready-for-agent`,
`ready-for-human`, `wontfix`. See `docs/agents/triage-labels.md`.

### Domain docs

Single-context: `GLOSSARY.md` at the root plus `docs/adr/`.
See `docs/agents/domain.md`.

### Subagent-driven development

Plans live in `/home/hone/.opencode/plan/<slug>.md`, one per work item, written
before any code. Execute one with `subagent-driven-development` from a worktree
off `main` (created with OpenCode's worktree tools, which place it outside the
repo under `~/.local/share/opencode/worktree/`) on branch `sdd/<slug>`, merged
back fast-forward — never on `main`, because `deploy-vps.sh` pulls `main`.

Model tiers (OpenCode Go) — **owner decision 2026-11-23: orchestrator and
implementer on deepseek-flash** (`opencode-go/deepseek-v4.1-flash`) for cost;
**task reviewers on `opencode-go/glm-5.3`** (the owner wants an independent
tier on every task diff). The whole-branch reviewer stays flash unless edited
here. Vision gates are exempt: `deepseek-flash` cannot accept images — dispatch
any screenshot/card check on a vision-capable model (e.g. `glm-5.3-flash`).
An implementer never reviews its own work. The per-plan ledger is
`.superpowers/sdd/<plan>/progress.md`
(git-ignored) — after a compaction, trust it and `git log`, not memory.

