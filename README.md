# Wangga Spa Baby, Mom, & Woman

Go + HTMX booking app for **Wangga Spa Baby, Mom, & Woman** — PostgreSQL, Docker Compose, Midtrans QRIS, Frontdesk Staff Portal, and Role-Based Access (Customer, CS, Admin).

## Quick start

```bash
docker compose up --build
```

- App: http://localhost:8088
- Caddy proxy: http://localhost:8089
- Mailpit UI: http://localhost:8025 (localhost-only, never expose publicly)
- Umami analytics: http://localhost:8030 (localhost-only dashboard; public tracker via tunnel)
- Admin: `admin@wangga.biz.id` / `admin12345`

## Visitor analytics (Umami)

Open-source, cookie-less analytics. One `umami` container reuses the existing Postgres server (own `umami` database).

First run (existing `pgdata` volume needs the DB created once):

```bash
docker compose up -d postgres umami
docker compose exec postgres psql -U spa -d spa -c "CREATE DATABASE umami;"
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

## Customer email (Gmail relay + Email Routing)

Booking confirmations/cancellations/reschedules go out as `halo@wangga.biz.id` via Gmail SMTP. Empty `SMTP_USER` = Mailpit catcher (local dev, unchanged).

**1. App Password** (Google Account → Security → enable **2-Step Verification** → search **App passwords** → create one for "Mail"):
```bash
SMTP_HOST=smtp.gmail.com
SMTP_PORT=587
SMTP_FROM=halo@wangga.biz.id
SMTP_USER=<your Gmail address>
SMTP_PASS=<16-char app password, spaces optional>
```
then `docker compose up -d app`. Never use your real Google password here.

**2. Send-as alias** (so customers see the domain, not your Gmail): Gmail → Settings → Accounts → *Send mail as* → add `halo@wangga.biz.id` → approve the verification mail (it arrives via step 3's forwarding).

**3. Inbound via Cloudflare Email Routing** (free, zero containers): Email → Email Routing → verify your destination inbox → add route `halo@wangga.biz.id` → forward to it → let Cloudflare add the MX records. Replies from customers land in your Gmail/Yahoo.

**4. Live test**: register a test customer with your real address → book → confirm the mail arrives *from* `halo@wangga.biz.id` → reply to confirm the forward works.

Limits & caveats: ~500 mails/day (far above spa volume); Gmail signs as `gmail.com`, so do **not** publish a strict DMARC `p=reject` record for `wangga.biz.id` — it would conflict with this relay setup.

(Host port 8080 is often busy; Compose maps the app to **8088**.)

Without Midtrans keys in local dev, payment pages show a **Simulate successful payment** button.

## Deploy to another machine (fresh install)

This guide moves **no data** — the new machine boots to a clean, demo-seeded instance. Old bookings, customers, CVs, and photos stay on the old machine.

### Before you start (both targets)

**1. Free ports.** These host ports must be free on the new machine:

| Port | Service |
|------|---------|
| 5432 | Postgres |
| 8025 / 1025 | Mailpit UI / SMTP |
| 8030 | Umami dashboard |
| 8088 | App |
| 8089 | Caddy proxy |

Check occupancy — Windows (PowerShell): `netstat -ano | findstr "8088 8030 5432"` (no output = free). Linux: `ss -tlnp | grep -E '8088|8030|5432'` (no output = free).

**2. Configure `.env`.** It is gitignored and never transfers — every machine gets its own:

```bash
cp .env.example .env
```

| Variable | Action |
|----------|--------|
| `SESSION_SECRET` | **Always fresh per machine:** `openssl rand -hex 32` (Git Bash on Windows works too). Sharing one secret lets machines forge each other's sessions. |
| `ADMIN_PASSWORD` | Set a new strong password (first boot creates the admin from `ADMIN_EMAIL`/`ADMIN_PASSWORD`; changing it later does not rotate an existing account — reset via SQL with a bcrypt hash). |
| `BASE_URL` | The machine's public URL (`http://localhost:8088` for local, `https://wangga.biz.id` for the live server). |
| `APP_ENV` | `production` on a live server (enables Secure cookies); leave `development` locally. |
| `CONTACT_*` | Copy as-is (same business info everywhere). |
| `MIDTRANS_*` | Sandbox keys for testing, production keys for live. Empty = "Simulate successful payment" button. |
| `ANALYTICS_*` | Leave empty for now — set after the Umami first-run in step D. |

**3. Know what first boot does** (so "is it working?" has a concrete answer): Goose migrations run → admin auto-created → demo catalog seeded (location "Wangga Kayu Putih" + services "Baby Spa Soft" and "Mom Recovery Massage"). Success = you can log in as admin and see that catalog in `/portal/services`.

### A. Same setup (Windows + WSL + Docker Desktop) — ~20 min

1. Install **Docker Desktop** (tick *Use WSL 2*) from docker.com. Open a WSL terminal and confirm your distro: `wsl -l -v` (needs STATE `Running`, VERSION `2`).
2. Docker Desktop → Settings → General → ✅ **Start Docker Desktop when you log in**.
3. Clone **inside WSL home** (not `/mnt/c` — bind mounts are slow there):
   ```bash
   git clone https://github.com/v37-ship/ws.git
   cd ws
   cp .env.example .env   # fill in per the table above
   ./scripts/deploy-local.sh
   ```
   Success ends with: `LOCAL DEPLOY OK -> http://localhost:8088 (proxy: ..., mail: ...)`.
4. Verify, in order:
   ```bash
   docker ps                  # 5 containers Up: postgres, mailpit, app, caddy, umami
   curl -fsS http://localhost:8088/healthz && curl -fsS http://localhost:8088/readyz
   ```
   Then open http://localhost:8088, log in as admin, and check `/portal/services` shows the seeded catalog.

### B. Linux server (native Docker Engine, Ubuntu) — ~25 min

1. Install Docker Engine + compose plugin:
   ```bash
   sudo apt update && sudo apt install -y docker.io docker-compose-plugin
   sudo systemctl enable --now docker
   sudo usermod -aG docker $USER   # re-login after this for passwordless docker
   ```
2. Clone, configure, deploy (same commands as Guide A, step 3). Production `.env` values: `APP_ENV=production`, `BASE_URL=https://wangga.biz.id`, production Midtrans keys.
3. Firewall — the tunnel only needs **outbound** HTTPS. Keep everything else closed:
   ```bash
   sudo ufw allow OpenSSH
   sudo ufw deny 8088/tcp && sudo ufw deny 8030/tcp && sudo ufw deny 8025/tcp
   sudo ufw enable
   ```
   (The Umami dashboard stays reachable via its tunnel hostname, gated by Cloudflare Access.)
4. Verify with the same checks as Guide A, step 4, using `ss -tlnp` for port inspection. Reboot survival comes from `restart: unless-stopped` + the enabled docker service — confirm once with a real reboot.

### C. Tunnel cutover (both targets)

Your tunnel is dashboard-managed, so connectors are disposable — DNS already points at the tunnel ID and never changes.

1. Zero Trust dashboard → Networks → Tunnels → your tunnel → **Connectors** tab → copy the install command for the new machine's OS (Windows service / Linux service) using the **same tunnel token**.
2. Install + start the connector on the new machine. Confirm it appears in the Connectors tab with status `Connected`.
3. Cutover, in this order: verify the site + Umami hostname through the **new** connector → stop the **old** connector (`Stop-Service cloudflared` / `systemctl stop cloudflared`) → delete the old connector row in the dashboard.
4. Rollback (DNS untouched, so it's instant): restart the old connector, stop the new one.

Expect a brief Cloudflare `502` on the site while the new machine's app container is still starting (~1 min) — normal, resolves itself.

### D. Umami first-run (both targets)

1. `docker compose exec postgres psql -U spa -d spa -c "CREATE DATABASE umami;"` (skip on fresh clones — `docker/postgres-init/` already did it), then `docker compose up -d umami`.
2. Open the dashboard (`http://localhost:8030` locally), log in `admin` / `umami` → **change the password immediately** (avatar → Profile → Change password).
3. **Add Website**: name `Wangga`, domain `wangga.biz.id` → open the website → copy the **Website ID** (from the URL or Settings).
4. Back in `.env`: set `ANALYTICS_URL` (local `http://localhost:8030`, or `https://umami.wangga.biz.id` once the tunnel hostname exists) + `ANALYTICS_SITE_ID=<id>`, then restart only the app (no rebuild): `docker compose up -d app`.
5. Confirm the snippet renders: `curl -s http://localhost:8088/ | grep -o 'script.js'` → shows your Umami host. Then visit the site and watch the visit land in Umami realtime.

### E. Troubleshooting

| Symptom | Cause → Fix |
|---------|-------------|
| `curl healthz` fails after deploy | App still starting — `deploy-local.sh` already waits ~3 min; check `docker compose ps` and `docker compose logs app`. |
| Umami container CrashLoops | `umami` database missing → step D.1, then `docker compose up -d umami`. |
| Tunnel shows `502` for >2 min | Connector can't reach the app: from the tunnel machine, `curl http://localhost:8088/healthz` must answer. On Windows, the tunnel and Docker must share the same localhost view (both on the host, not split across WSL distros). |
| Tracker loads but no visits recorded | Hostname-mismatch rule: Umami drops visits whose hostname ≠ Website domain. Localhost visits need a second Website with domain `localhost`. |
| `.env` changes seem ignored | Compose bakes env at container creation — always follow edits with `docker compose up -d app` (or the affected service). |
| Port already in use | Free it or remap the host side in `docker-compose.yml` (e.g. `"8088:8080"` → `"8090:8080`), then update `BASE_URL` to match. |

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
- Configure site contact info shown in the footer via `CONTACT_PHONE/WHATSAPP/EMAIL/ADDRESS/HOURS/MAPS_URL/INSTAGRAM`.

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
