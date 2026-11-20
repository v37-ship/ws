# GLOSSARY — Wangga Spa

The project's domain vocabulary. One entry per concept, defined by what the code
actually does. When an issue title, test name, ADR, or plan names one of these
concepts, use the term as written here — not a synonym.

Each entry names the file that settles the definition. If code and this file
disagree, the code wins and this file is the bug.

Ownership: `domain-modeling` maintains this file. Add a term when a decision
settles its meaning; do not pre-fill speculative terms.

---

## Booking lifecycle

**Booking** — one appointment row. It snapshots what the customer bought
(`service_name`, `price_idr`, `discount_idr`, `distance_fee_idr`,
`booking_fee_idr`) so later catalog edits never rewrite history.
`internal/domain/models.go`, `internal/repo/booking.go`.

**Hold** — a booking in `pending_payment` with no payment yet. It expires after
`BOOKING_HOLD_TTL` (default 15m); the 1-minute ticker in `cmd/server/main.go`
expires holds and deletes stale sessions. "Hold" is never a separate entity.

**Active status** — one of `pending_payment`, `confirmed`, `checked_in`,
`in_service`, `completed`. These five are what the Postgres `EXCLUDE` constraint
uses to prevent double-booking a slot. `cancelled`, `expired`, `no_show` are
inactive and free the slot. `internal/db/migrations/003_*.sql`.

**Source** — how the booking entered: `web`, `walkin`, `whatsapp`, `phone`.
Not a status and not a payment method.

## Money

**Net amount** — `price_idr + distance_fee_idr − discount_idr`, floored at 0.
This is the only number a gateway ever charges or a report ever sums as revenue.
`Booking.Net()` in `internal/domain/models.go`.

**Booking fee** — a separate platform fee (`booking_fee_idr`), snapshotted per
booking. It is **never** payable in cash: the rule lives in the service layer
*and* a DB `CHECK` constraint. Cash is treatments-only at the counter.

**Distance band** — homecare transport pricing unit. The customer picks a band
(one-way distance rounded up); the server maps the band to a fee that covers the
round trip. A posted amount is never trusted. No active bands means the feature
is off and the fee is 0. `distance_zones` table, `internal/service/distance.go`.

**Promo snapshot** — `promo_code` + `discount_idr` copied onto the booking at
purchase. Usage is consumed atomically (`TryRedeem`), rolled back if charging
fails. Codes are immutable: delete and recreate, never edit.
`internal/repo/promo.go`.

**Cash-basis revenue** — reporting rule: revenue counts in the Asia/Jakarta
month of `paid_at`; refunds deduct in the month the refund was recorded; a
cancelled-but-paid booking counts until it is refunded. `internal/service/report.go`.

**Void refund** — an admin correction that keeps the refund row and sets
`voided_at`; voided rows are excluded from totals rather than deleted.

## Trust boundaries

**Ownership** — three checks, one place each: `Booking.OwnedBy(userID)` for
account bookings, `Booking.TokenOK(token)` for guests (constant-time compare),
`Booking.IsGuest()` when `customer_id IS NULL`. Handlers never re-implement
these. `internal/domain/models.go`.

**Guest access token** — the `?t=` credential that lets a guest pay, poll, or
cancel without an account. Unique and revocable. History stays account-only.

**Correction** — admin-only edit of a booking's details. Logs old→new and flags
`priceBelowPaid` when the new total undercuts what was already paid.

## Scheduling

**Slot** — a bookable start time on a 15-minute grid, inside
`service hours ∩ therapist hours`, minus a 10-minute prep buffer, minus
`staff_time_off` blockouts.

**Blockout** — a `staff_time_off` window that removes a therapist from
availability. Not a booking, not a status.

**Place** — `onsite` or `homecare`. Homecare is a property of the booking, never
a second location, and requires `visit_address`.

## Messaging

**Outbox** — the `wa_outbox` table. Messages are *enqueued* by handlers and
services and delivered by a 15-second worker (`FOR UPDATE SKIP LOCKED`, retry
backoff). Never send inline from a request path. `internal/service/wa.go`.

**Scheduled message** — an outbox row with a future `scheduled_at` and a dedupe
key. Use `UpsertScheduled`/`CancelPending` when a message must move; a bare
`Enqueue` duplicates it.

**Effective settings** — DB-backed WhatsApp/payment config where a NULL or empty
column means "use the env default". Precedence is always DB → env, read at call
time, never cached into a struct field at startup.

## Presentation

**Language resolution** — `?lang=` > `wangga-lang` cookie (1 year) >
`Accept-Language` (EN only when ID is absent) > **ID**. Keys must exist in both
dictionaries; `T()` falls back EN → key.

**Computed template function** — values such as `WeekdayShort`, `MonthShort`,
`SlotLabel`, `DateLong`, `staticVersion`. All render in WIB; none take a
language argument because they read the request language.

**Static version** — `staticVersion` is a sha256 over every embedded static
asset, 12 hex chars. It is the deployment fingerprint: compare `app.css?v=` to
know which build a server is actually serving.
