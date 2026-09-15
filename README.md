# Wangga: Baby & Mom Spa

Go + HTMX booking app for **Wangga: Baby & Mom Spa** — PostgreSQL, Docker Compose, Midtrans QRIS, Frontdesk Staff Portal, and Role-Based Access (Customer, CS, Admin).

## Quick start

```bash
docker compose up --build
```

- App: http://localhost:8088
- Caddy proxy: http://localhost:8089
- Mailpit UI: http://localhost:8025 (localhost-only, never expose publicly)
- Umami analytics: http://localhost:8030 (localhost-only dashboard; public tracker via tunnel)
- Admin: `admin@spa.local` / `admin12345`

## Visitor analytics (Umami)

Open-source, cookie-less analytics. One `umami` container reuses the existing Postgres server (own `umami` database).

First run (existing `pgdata` volume needs the DB created once):

```bash
docker compose up -d postgres umami
docker exec ws-postgres-1 psql -U spa -d spa -c "CREATE DATABASE umami;"
docker compose up -d umami  # Umami creates its own tables on boot
```

Then: open http://localhost:8030, log in `admin` / `umami`, **change the password**, Add Website → copy the Website ID, and set:

```bash
ANALYTICS_URL=http://localhost:8030          # or https://umami.wangga.biz.id via tunnel
ANALYTICS_SITE_ID=<website-id>
```

(Fresh clones get the DB automatically via `docker/postgres-init/`.) The tracker snippet renders only when both are set.

Note: Umami only records visits whose hostname matches the Website domain — set the domain to `wangga.biz.id` and localhost visits are silently skipped. Add a second Website with domain `localhost` if you want local-visit stats too.

Public access via Cloudflare Tunnel (named tunnel on your server):

```yaml
ingress:
  - hostname: wangga.biz.id
    service: http://localhost:8088
  - hostname: umami.wangga.biz.id
    service: http://localhost:8030
  - service: http_status:404
```

plus one DNS CNAME per hostname to `<tunnel-id>.cfargotunnel.com`. Protect `umami.*` with Cloudflare Access (Zero Trust, email OTP — **pending**) and keep Mailpit off the tunnel entirely.

(Host port 8080 is often busy; Compose maps the app to **8088**.)

Without Midtrans keys in local dev, payment pages show a **Simulate successful payment** button.

## Deploy to another machine (fresh install)

Free ports required on the new machine: `5432, 8025, 1025, 8030, 8088, 8089`.

`.env` is gitignored and never transfers — every machine gets its own from `.env.example`. Always generate a fresh session secret per machine (sharing one lets machines forge each other's sessions):

```bash
openssl rand -hex 32   # → paste as SESSION_SECRET
```

First boot self-provisions: Goose migrations auto-run, the admin is auto-created from `ADMIN_EMAIL`/`ADMIN_PASSWORD`, and the demo catalog is seeded. Uploads start empty (old CVs/photos stay on the old machine — this guide moves no data).

### A. Same setup (Windows + WSL + Docker Desktop)

1. Install Docker Desktop with the WSL2 backend; enable **Start Docker Desktop when you log in**.
2. Clone the repo **inside WSL home** (not `/mnt/c` — bind mounts are slow there):
   ```bash
   git clone https://github.com/v37-ship/ws.git
   cd ws
   cp .env.example .env   # set SESSION_SECRET, ADMIN_PASSWORD, Midtrans keys
   ./scripts/deploy-local.sh
   ```
3. Tunnel: install `cloudflared` on Windows with the **same tunnel token** (Zero Trust dashboard → your tunnel → Install connector — the DNS records already point at the tunnel ID, so no DNS changes). Serve one machine at a time: stop the old connector when cutting over.
4. Umami: one-time `CREATE DATABASE umami` (see Visitor analytics), first login + new Website ID → set `ANALYTICS_URL`/`ANALYTICS_SITE_ID` in the new `.env` → `docker compose up -d app`.

### B. Linux server (native Docker Engine)

1. Install Docker Engine + the compose plugin; `sudo systemctl enable --now docker`.
2. Clone, configure, deploy:
   ```bash
   git clone https://github.com/v37-ship/ws.git
   cd ws
   cp .env.example .env
   ```
   Set: fresh `SESSION_SECRET`, `ADMIN_PASSWORD`, `APP_ENV=production` (Secure cookies), `BASE_URL=https://wangga.biz.id`, Midtrans production keys. Then `./scripts/deploy-local.sh` (works unchanged; `restart: unless-stopped` + enabled docker service = reboot survival).
3. Tunnel: install `cloudflared` (`cloudflared service install <token>` with the same tunnel token) — same single-server-at-a-time note as above.
4. Firewall: the tunnel only needs **outbound** HTTPS — keep everything else closed (`8088/8030/8025` must not be public; the Umami dashboard is already gated by Cloudflare Access).
5. Umami first-run as in step A.4 (new Website ID per install).

## Local Go run

```bash
docker compose up -d postgres mailpit
cp .env.example .env
make run
```

## User Roles & Capabilities

### 1. Customer (Mom / Parents)
- Self-registration & login.
- Book spa appointments (select location, service, therapist/bidan, date, and slot).
- Pay booking fees or full services via Midtrans QRIS.
- View booking history and status.
- Cancel with reconfirmation: unpaid bookings cancel free; paid bookings show a WhatsApp refund button with the booking reference prefilled (reason is logged for CS).

### 2. Customer Service (CS / Frontdesk) — `/portal`
- **Live Clinic Dashboard**: Real-time queue (`Confirmed`, `Checked-in`, `In-service`, `Completed`, `No-show`).
- **1-Click WhatsApp Slip**: Open pre-filled appointment confirmation directly to the mother's WhatsApp.
- **Manage Bookings**: View, filter, edit details, reschedule appointments, and execute clinic workflow transitions.
- **Register Customer**: Phone-first customer registration capturing baby age/DOB, maternal condition (pregnancy trimester/postpartum recovery), and medical/skin sensitivities.
- **Register Walk-in Bookings**: Create bookings on behalf of customers.
- **Payment Business Rules**:
  - **QRIS**: Can be used to pay booking fees and services.
  - **Cash**: Strictly accepted for services only (at clinic counter).
- **Bidan Schedules & Leave**: Add time-off blockouts for sick leave, doctor visits, or holidays to dynamically prevent booking conflicts.
- **Service Availability**: Instantly toggle services active/inactive.
- **Service Catalog**: Add/edit services with price, duration, per-day opening hours, and photo (upload or internet link — gallery, wizard, and homepage follow automatically). Booking slots respect the intersection of service hours and therapist hours.
- **Team**: Add/edit therapists (profile, bio, service capabilities, profile photo upload or link, weekly hours), pause/activate, guarded removal (blocked with guidance when booking history exists), and time-off blockouts — all from the portal Team page.
- **Reports** (`/portal/reports`): Monthly revenue (gross / refunds / net, QRIS-vs-cash, by service/staff) with month + location filters, print stylesheet, and CSV export. Record refunds from booking detail (validated: never more than paid-minus-refunded); refunds deduct in the month recorded (cash basis).

### 3. Admin — `/admin`
- Everything CS can do.
- **CS Staff Management** (`/admin/cs`): Create CS staff accounts, toggle active/deactivate (instantly revoking sessions), and reset CS passwords.
- **Job Openings** (`/admin/jobs`): Post vacancies for the public Careers page, close/reopen them, review applications, and download CVs.
- **Customer Password Reset** (`/admin/customers/{id}/reset-password`).
- **Master Catalog & Locations**: Manage branch locations, business hours, and base service pricing.

## Careers

- Public page `/careers` lists open positions with an apply form (name, phone, email, message, CV upload up to 5 MB: PDF/DOC/JPG/PNG).
- CVs are stored in `UPLOAD_DIR` (`./uploads` locally, persisted via compose volume) and downloadable by admins only.
- Configure site contact info shown in the footer via `CONTACT_PHONE/WHATSAPP/EMAIL/ADDRESS/HOURS`.

## Language (EN/ID)

- Server-side toggle in the header (EN | ID), default Indonesian; choice persists in the `wangga-lang` cookie (1 year), `?lang=` overrides per request. Browser `Accept-Language` is honored only when no cookie/override exists.

## Stack

- Go 1.26 + Chi + `html/template` (embedded) + HTMX 2.0.4 + Alpine.js + Tailwind
- Shared head partial (`layouts/head.html`): Tailwind browser, Alpine CDN, `modern.css` (indicators/toasts/skeletons), `app.js` (toast via `HX-Trigger`, slot-picker fallback)
- HTMX helpers (`internal/http/htmx`): `IsRequest`, `Trigger`/`Toast`, `Redirect`, `Refresh`, `Retarget`, `Reswap`; `renderAuto` serves fragments for `HX-Request`
- PostgreSQL 16 + native session storage (no Redis required)
- Embedded migrations via Goose (`internal/db/migrations` — single source of truth)
- Midtrans QRIS + webhook (`/webhooks/midtrans`)
- SMTP email delivery (Mailpit in Compose)

## HTMX endpoints (fragments)

- `GET /book/services|staff|slots` → `services/staff/slots` partials (wizard cascade, `hx-indicator`, `aria-live`)
- `GET /bookings/{id}/status` → pay status poll (`every 3s`)
- `GET /portal/queue` → live queue rows (`every 30s` on `/portal`)
- `GET /portal/bookings` → rows partial when `HX-Request` (debounced live search + `hx-push-url`)
- `POST /portal/bookings/{id}/status` → `hx-post` + `hx-confirm` + `hx-disabled-elt`; returns `HX-Trigger: toast` + `HX-Refresh` (no full form post)

## Production notes

- Set strong `SESSION_SECRET`, production `DATABASE_URL`, Midtrans keys, SMTP
- Set `APP_ENV=production` (enables Secure cookies)
- Point Midtrans notification URL to `https://your-domain/webhooks/midtrans`
- Keep a single app replica unless shared session storage is added
- `GET /healthz` (compose/docker healthcheck) + `GET /readyz`
- Docker: multi-stage build with Go module cache, image `HEALTHCHECK`, non-root `nobody`; Caddy listens on `:80` (mapped to host `8089`)
- All compose services use `restart: unless-stopped` — the stack survives host reboots as long as Docker Desktop starts on login and the `cloudflared` service stays Automatic.
