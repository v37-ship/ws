# AGENTS.md — Wangga Spa Baby, Mom, & Woman

Booking app for the spa. Go 1.26 + Chi + `html/template` (embedded) + vendored
HTMX + Alpine.js + Tailwind browser CDN + Postgres 16 + Goose + Midtrans QRIS.

## Layout

- `cmd/server` — entrypoint.
- `internal/config`, `internal/db/migrations` (Goose, single source of truth),
  `internal/domain` (models), `internal/repo` (SQL), `internal/service`
  (business logic), `internal/i18n` (EN+ID maps), `internal/http/handlers`
  (routes + `templates/` + `static/`), `internal/email`, `internal/payment`.
- `docker/` (Caddyfile, postgres-init), `scripts/`, `uploads/` (gitignored
  runtime data), `.github/workflows/ci.yml`.

## Commands

- Verify like CI before pushing: `./scripts/ci-local.sh`
  (gofmt check, `go vet`, `go test -race -count=1`, `go build`, docker build,
  compose config). `make ci` runs the same script.
- Ship: `./scripts/deploy-local.sh` (rebuilds, waits for health, smoke-tests).
- Local Go run: `docker compose up -d postgres mailpit && make run`.
- Smoke: `make smoke` (healthz + readyz).
- Ports: app **8088**→8080, caddy proxy **8089**→80, mailpit UI **8025** /
  SMTP **1025**, umami **8030**, postgres **5432**.

## i18n — hard rules

- Every key must exist in **both** EN and ID maps (`TestDictParity` fails
  otherwise). Prefer value-only edits; keep keys **≤12 chars**.
- Default language is **ID**. Resolution: `?lang=` > `wangga-lang` cookie >
  Accept-Language. `T()` falls back EN → key.
- Brand name stays identical in both languages. Error strings (`.Error`)
  stay English. User-facing copy is translated.

## Templates — embedded, rebuild to verify

- Templates and static assets are compiled into the binary. Text/markup
  changes are invisible until you rebuild + `deploy-local.sh`.
- `TestTemplatesBothLanguages` renders every page in both languages: copy
  changes can break its assertions — update them (e.g. the slogan change
  updated the `home.html` hero assertions).
- `app.css?v=<hash>` fingerprints builds; compare hashes to check what a
  deployment actually serves.

## Deploy topology

- The Cloudflare Tunnel serves **this machine's** `:8088`, so local deploy =
  public deploy. Verify with `curl https://wangga.biz.id/` (compare
  `app.css?v=` or content markers against localhost).
- No separate prod-machine step. Local and prod DBs are separate; data-only
  backfills stay uncommitted.

## Domain decisions (do not "simplify" these)

- Homecare is `Booking.Place` (`onsite`/`homecare`), never a second Location.
  `VisitAddress` is required for homecare. `?place=homecare` pre-checks the
  wizard's Homecare radio (homepage Homecare card links there).
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

## Git

- `feat:` / `fix:` / `chore:` prefixes on `main`. Inspect `git status`,
  `git diff`, recent log before committing; stage only intended files.
- Never commit `.env` or secrets. `uploads/` and local DB volumes are not
  transferable artifacts.
