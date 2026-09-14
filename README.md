# Wangga: Baby & Mom Spa

Go + HTMX booking app for **Wangga: Baby & Mom Spa** — PostgreSQL, Docker Compose, Midtrans QRIS, Frontdesk Staff Portal, and Role-Based Access (Customer, CS, Admin).

## Quick start

```bash
docker compose up --build
```

- App: http://localhost:8088
- Caddy proxy: http://localhost:8089
- Mailpit UI: http://localhost:8025
- Admin: `admin@spa.local` / `admin12345`

(Host port 8080 is often busy; Compose maps the app to **8088**.)

Without Midtrans keys in local dev, payment pages show a **Simulate successful payment** button.

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
