//go:build integration

package integration

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
)

// The undo chain is the one lifecycle rule that reads its own audit trail back:
// every status write appends "Status changed to X", and UndoLastStatus derives
// the predecessor from those rows. The unit tests pin the parser against
// hand-built slices; this group executes the same chain against real rows
// through the real service, so a repo-side ordering or column drift surfaces
// here instead of in production.

// undoFixture is one confirmed booking created through the production path
// (BookingService.CreatePending in manual-payment mode), plus the real repos the
// assertions read through.
type undoFixture struct {
	pool     *pgxpool.Pool
	svc      *service.BookingService
	bookings *repo.BookingRepo
	booking  *domain.Booking
	actorID  uuid.UUID
}

// undoStaffGroupJID is a real Evolution group-JID shape. The undo assertions
// use it so a fan-out would have somewhere to write: unless the correction is
// quiet, a revert to confirmed pings the group and the row count moves.
const undoStaffGroupJID = "120363000000000000@g.us"

// newUndoService wires BookingService the way cmd/server does, with the test
// pool's repos, and no staff group. The only stubs are the two dependencies
// the exercised paths never touch: midtrans (manual-payment mode confirms
// without charging) and the mailer (none of create/status/undo sends mail). The
// WhatsApp notifier is the REAL WaService over the REAL outbox repo in mock
// mode (enabled=false), so scheduling and cancellation write and update
// genuine wa_outbox rows without a paired instance.
func newUndoService(t *testing.T, pool *pgxpool.Pool) *service.BookingService {
	t.Helper()
	return newUndoServiceWithGroup(t, pool, "")
}

// newUndoServiceWithGroup is newUndoService with a staff group JID wired into
// BookingOptions. The fixture creates its booking with the group-less service
// and hands the group-wired one to the test body: a staff_booking dedupe key is
// permanent, so a group wired from creation would make the undo's repeated ping
// a silent no-op and the quiet-undo assertion toothless.
func newUndoServiceWithGroup(t *testing.T, pool *pgxpool.Pool, staffGroup string) *service.BookingService {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	bookings := repo.NewBookingRepo(pool)
	catalog := repo.NewCatalogRepo(pool)
	// Customer messaging and the review ask are switched on so the completion
	// path really schedules its review row.
	wa := service.NewWaService(repo.NewWaRepo(pool), nil, log, false, 0, nil)
	return service.NewBookingService(
		bookings,
		repo.NewPaymentRepo(pool),
		catalog,
		repo.NewUserRepo(pool),
		repo.NewPromoRepo(pool),
		repo.NewDistanceRepo(pool),
		service.NewAvailabilityService(catalog),
		nil, // midtrans: qris_manual never charges
		nil, // mailer: none of these paths send mail
		15*time.Minute,
		"http://localhost:8080",
		log,
		service.BookingOptions{
			PaymentMode: "qris_manual",
			Wa:          wa,
			CustomerWA:  true,
			StaffGroup:  staffGroup,
			ReviewURL:   "https://g.page/r/integration-test/review",
			RemindHour:  18,
		},
	)
}

// newUndoFixture truncates, seeds one branch/treatment/therapist/customer, a CS
// account to act as the undo actor, and creates a confirmed booking through
// CreatePending (the manual-payment creation path, which writes the "Status
// changed to confirmed" row via logCreationStatus). The customer carries a phone
// number so the completion path's review request passes its recipient check.
func newUndoFixture(t *testing.T) *undoFixture {
	t.Helper()
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	loc := insertLocation(t, pool, "Undo Branch")
	svcID := insertService(t, pool, loc, "Undo Treatment", 200_000)
	staff := insertStaff(t, pool, loc, "Undo Therapist")
	customer := insertUser(t, pool, "undo@example.com", string(domain.RoleCustomer))
	// booking_logs.user_id and bookings.updated_by_id are real foreign keys, so
	// the actor must be a real account — a random UUID would fail the insert and
	// roll the whole transition back.
	actor := insertUser(t, pool, "undo-staff@example.com", string(domain.RoleCS))

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, `UPDATE users SET phone=$2 WHERE id=$1`, customer, "6281234500001"); err != nil {
		t.Fatalf("seed customer phone: %v", err)
	}

	bookingSvc := newUndoService(t, pool)
	// Three days out: comfortably inside the slot rules and far enough away
	// that the reminder scheduler has a window to work with.
	start := time.Now().Add(72 * time.Hour).Truncate(time.Minute)
	b, _, err := bookingSvc.CreatePending(ctx, customer, loc, svcID, staff, start,
		"integration undo fixture", domain.PlaceOnsite, "", "", nil)
	if err != nil {
		t.Fatalf("create booking through service: %v", err)
	}
	if b.Status != domain.BookingConfirmed {
		t.Fatalf("manual-payment creation status = %q, want confirmed", b.Status)
	}

	return &undoFixture{
		pool: pool,
		// From here on the group is wired: creation's pings already used their
		// permanent dedupe keys, so any *new* staff row after a status move can
		// only come from a fan-out the test is meant to observe.
		svc:      newUndoServiceWithGroup(t, pool, undoStaffGroupJID),
		bookings: repo.NewBookingRepo(pool),
		booking:  b,
		actorID:  actor,
	}
}

// reload returns the booking as the database currently has it.
func (fx *undoFixture) reload(t *testing.T) *domain.Booking {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	b, err := fx.bookings.ByID(ctx, fx.booking.ID)
	if err != nil {
		t.Fatalf("reload booking: %v", err)
	}
	return b
}

// logs returns the audit trail newest-first, exactly as UndoLastStatus sees it.
func (fx *undoFixture) logs(t *testing.T) []domain.BookingLog {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	logs, err := fx.bookings.ListLogs(ctx, fx.booking.ID)
	if err != nil {
		t.Fatalf("list logs: %v", err)
	}
	return logs
}

// statusActions filters the trail to the parsable status rows, newest-first.
func statusActions(logs []domain.BookingLog) []string {
	var out []string
	for _, l := range logs {
		if strings.HasPrefix(l.Action, domain.StatusLogPrefix) {
			out = append(out, l.Action)
		}
	}
	return out
}

// countLogs counts every audit row, used to prove a refusal wrote nothing.
func (fx *undoFixture) countLogs(t *testing.T) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var n int
	if err := fx.pool.QueryRow(ctx, `SELECT count(*) FROM booking_logs WHERE booking_id=$1`, fx.booking.ID).Scan(&n); err != nil {
		t.Fatalf("count logs: %v", err)
	}
	return n
}

// TestUndoChainCreationRowAndMove is the base fixture: the production creation
// path records the status a booking was born with in the same "Status changed
// to X" form every later transition uses, and a service-driven move appends on
// top of it. Newest-first, that is the chain undo parses.
func TestUndoChainCreationRowAndMove(t *testing.T) {
	fx := newUndoFixture(t)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := fx.svc.UpdateStatus(ctx, fx.booking.ID, domain.BookingCheckedIn, &fx.actorID, "arrived"); err != nil {
		t.Fatalf("move to checked_in: %v", err)
	}

	if got := fx.reload(t).Status; got != domain.BookingCheckedIn {
		t.Fatalf("status after move = %q, want checked_in", got)
	}

	logs := fx.logs(t)
	statuses := statusActions(logs)
	want := []string{
		domain.StatusLogAction(domain.BookingCheckedIn),
		domain.StatusLogAction(domain.BookingConfirmed),
	}
	if len(statuses) < len(want) {
		t.Fatalf("status rows = %v, want at least %v (all logs: %v)", statuses, want, logActions(logs))
	}
	for i, w := range want {
		if statuses[i] != w {
			t.Errorf("status row %d = %q, want %q (newest-first)", i, statuses[i], w)
		}
	}
	// The human-readable creation note is an activity row, not a status row; it
	// must be present and must not disturb the parsed chain.
	if !hasAction(logs, "Booking created online (manual QRIS pending)") {
		t.Errorf("creation activity row missing from the trail: %v", logActions(logs))
	}
	if got := statuses[len(statuses)-1]; got != domain.StatusLogAction(domain.BookingConfirmed) {
		t.Errorf("oldest status row = %q, want the creation status", got)
	}
}

// TestUndoRestoresPreviousStatus executes undo end to end: the service derives
// the predecessor from real rows, writes the revert through applyStatus, and
// records the undo note on the new row.
func TestUndoRestoresPreviousStatus(t *testing.T) {
	fx := newUndoFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := fx.svc.UpdateStatus(ctx, fx.booking.ID, domain.BookingCheckedIn, &fx.actorID, "arrived"); err != nil {
		t.Fatalf("move to checked_in: %v", err)
	}

	// The revert is quiet (ruling 3): a fan-out on undo would re-run the
	// confirmed lifecycle and, because the fixture wires a staff group, insert a
	// staff_booking row the creation path never queued. The count is the
	// observable that tells the two behaviours apart.
	beforeOutbox := countOutbox(t, fx.pool, fx.booking.ID)
	if beforeOutbox == 0 {
		t.Fatal("fixture queued no outbox rows, so the quiet-undo assertion would be vacuous")
	}

	prev, err := fx.svc.UndoLastStatus(ctx, fx.booking.ID, &fx.actorID)
	if err != nil {
		t.Fatalf("undo: %v", err)
	}
	if prev != domain.BookingConfirmed {
		t.Errorf("undo returned prev = %q, want confirmed", prev)
	}
	if got := fx.reload(t).Status; got != domain.BookingConfirmed {
		t.Fatalf("status after undo = %q, want confirmed", got)
	}
	if got := countOutbox(t, fx.pool, fx.booking.ID); got != beforeOutbox {
		t.Errorf("quiet undo changed the outbox count: %d -> %d (a fan-out would ping the staff group)", beforeOutbox, got)
	}

	logs := fx.logs(t)
	if len(logs) == 0 {
		t.Fatal("undo wrote no audit row")
	}
	if logs[0].Action != domain.StatusLogAction(domain.BookingConfirmed) {
		t.Fatalf("newest log = %+v, want the revert row", logs[0])
	}
	if want := "Undo: restored from checked_in"; logs[0].Notes != want {
		t.Errorf("undo note = %q, want %q", logs[0].Notes, want)
	}
	if logs[0].UserName == "" {
		t.Errorf("undo row has no actor name; ListLogs did not resolve the seeded actor")
	}
}

// TestSecondUndoIsOneStep documents what a second undo actually does against
// real rows: because the revert itself is a status change, the chain now ends
// at confirmed, and the next undo derives checked_in — one step, not a rewind
// through the whole history. Undo can therefore toggle, but it can never skip a
// status: the revert is logged in the same "Status changed to X" form as any
// move, and the parser has no marker for "this row was an undo", so refusing
// here would require a production change.
func TestSecondUndoIsOneStep(t *testing.T) {
	fx := newUndoFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := fx.svc.UpdateStatus(ctx, fx.booking.ID, domain.BookingCheckedIn, &fx.actorID, "arrived"); err != nil {
		t.Fatalf("move to checked_in: %v", err)
	}
	if prev, err := fx.svc.UndoLastStatus(ctx, fx.booking.ID, &fx.actorID); err != nil || prev != domain.BookingConfirmed {
		t.Fatalf("first undo = (%q, %v), want (confirmed, nil)", prev, err)
	}

	prev, err := fx.svc.UndoLastStatus(ctx, fx.booking.ID, &fx.actorID)
	if err != nil {
		t.Fatalf("second undo refused: %v", err)
	}
	if prev != domain.BookingCheckedIn {
		t.Errorf("second undo returned prev = %q, want checked_in (exactly one step)", prev)
	}
	if got := fx.reload(t).Status; got != domain.BookingCheckedIn {
		t.Errorf("status after second undo = %q, want checked_in", got)
	}

	// The invariant that protects staff work: the newest status row always
	// matches the booking's current status, so undo never derived from a stale
	// chain.
	statuses := statusActions(fx.logs(t))
	if len(statuses) == 0 || statuses[0] != domain.StatusLogAction(domain.BookingCheckedIn) {
		t.Fatalf("newest status row = %v, want %q", statuses, domain.StatusLogAction(domain.BookingCheckedIn))
	}
}

// TestUndoRefusesWhenChainMoved is the concurrency guard. A status change that
// bypassed the logging update (the repo's unlogged UpdateStatus, the seam this
// test uses directly and the 1-minute ticker's raw ExpireHolds UPDATE) makes the
// newest logged status disagree with the row; undo must refuse and touch
// nothing, rather than overwrite the change it cannot account for.
func TestUndoRefusesWhenChainMoved(t *testing.T) {
	fx := newUndoFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// One logged move first, so the trail really has a predecessor: without it
	// the parser could refuse merely because the chain is too short, and the
	// chain-match guard would go untested.
	if err := fx.svc.UpdateStatus(ctx, fx.booking.ID, domain.BookingCheckedIn, &fx.actorID, "arrived"); err != nil {
		t.Fatalf("logged move to checked_in: %v", err)
	}

	// Out-of-band move: the row changes, the audit trail does not.
	if err := fx.bookings.UpdateStatus(ctx, fx.booking.ID, domain.BookingInService); err != nil {
		t.Fatalf("out-of-band status write: %v", err)
	}
	if got := fx.reload(t).Status; got != domain.BookingInService {
		t.Fatalf("status after the out-of-band write = %q, want in_service", got)
	}
	before := fx.countLogs(t)

	prev, err := fx.svc.UndoLastStatus(ctx, fx.booking.ID, &fx.actorID)
	if err == nil {
		t.Fatalf("undo on a mismatched chain succeeded: prev=%q", prev)
	}
	if !errors.Is(err, service.ErrValidation) {
		t.Fatalf("undo error = %v, want ErrValidation", err)
	}
	if !strings.Contains(err.Error(), "no undoable status change") {
		t.Errorf("undo error = %q, want the no-undoable-status-change copy", err.Error())
	}
	if got := fx.reload(t).Status; got != domain.BookingInService {
		t.Errorf("status after a refused undo = %q, want the out-of-band in_service", got)
	}
	if got := fx.countLogs(t); got != before {
		t.Errorf("refused undo wrote %d audit rows", got-before)
	}
}

// TestUndoCompletedCancelsReviewRequest covers the side effect unique to
// un-finishing a visit: completing queues the +2h review ask in the real
// outbox; undoing to in_service must cancel that pending row (asking for a
// review of a visit that is no longer complete is wrong) and enqueue nothing
// new — undo is quiet toward the customer.
func TestUndoCompletedCancelsReviewRequest(t *testing.T) {
	fx := newUndoFixture(t)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	for _, st := range []domain.BookingStatus{domain.BookingInService, domain.BookingCompleted} {
		if err := fx.svc.UpdateStatus(ctx, fx.booking.ID, st, &fx.actorID, "status "+string(st)); err != nil {
			t.Fatalf("move to %s: %v", st, err)
		}
	}

	key := "review:" + fx.booking.ID.String()
	if got := outboxStatus(t, fx.pool, fx.booking.ID, key); got != "pending" {
		t.Fatalf("review request row status = %q, want pending after completion", got)
	}
	if got := outboxTemplate(t, fx.pool, fx.booking.ID, key); got != "review_request" {
		t.Errorf("review row template = %q, want review_request", got)
	}
	if got := outboxPhone(t, fx.pool, fx.booking.ID, key); got != "6281234500001" {
		t.Errorf("review row phone = %q, want the customer's number", got)
	}

	beforeOutbox := countOutbox(t, fx.pool, fx.booking.ID)

	prev, err := fx.svc.UndoLastStatus(ctx, fx.booking.ID, &fx.actorID)
	if err != nil {
		t.Fatalf("undo completed: %v", err)
	}
	if prev != domain.BookingInService {
		t.Errorf("undo prev = %q, want in_service", prev)
	}
	if got := fx.reload(t).Status; got != domain.BookingInService {
		t.Fatalf("status after undo = %q, want in_service", got)
	}

	if got := outboxStatus(t, fx.pool, fx.booking.ID, key); got != "cancelled" {
		t.Errorf("review request row status after undo = %q, want cancelled", got)
	}
	if got := countOutbox(t, fx.pool, fx.booking.ID); got != beforeOutbox {
		t.Errorf("undo added outbox rows: %d -> %d", beforeOutbox, got)
	}
	if got := countOutboxMatching(t, fx.pool, fx.booking.ID, "review:%"); got != 1 {
		t.Errorf("review rows for the booking = %d, want 1", got)
	}
}

// --- small query helpers ---------------------------------------------------

func logActions(logs []domain.BookingLog) []string {
	out := make([]string, len(logs))
	for i, l := range logs {
		out[i] = l.Action
	}
	return out
}

func hasAction(logs []domain.BookingLog, action string) bool {
	for _, l := range logs {
		if l.Action == action {
			return true
		}
	}
	return false
}

func outboxStatus(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID, key string) string {
	t.Helper()
	var status string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx,
		`SELECT status FROM wa_outbox WHERE booking_id=$1 AND dedupe_key=$2`,
		bookingID, key).Scan(&status); err != nil {
		t.Fatalf("read outbox row %s: %v", key, err)
	}
	return status
}

func outboxTemplate(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID, key string) string {
	t.Helper()
	var tpl string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx,
		`SELECT template_key FROM wa_outbox WHERE booking_id=$1 AND dedupe_key=$2`,
		bookingID, key).Scan(&tpl); err != nil {
		t.Fatalf("read outbox template %s: %v", key, err)
	}
	return tpl
}

func outboxPhone(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID, key string) string {
	t.Helper()
	var phone string
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx,
		`SELECT phone FROM wa_outbox WHERE booking_id=$1 AND dedupe_key=$2`,
		bookingID, key).Scan(&phone); err != nil {
		t.Fatalf("read outbox phone %s: %v", key, err)
	}
	return phone
}

func countOutbox(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID) int {
	t.Helper()
	var n int
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM wa_outbox WHERE booking_id=$1`, bookingID).Scan(&n); err != nil {
		t.Fatalf("count outbox rows: %v", err)
	}
	return n
}

func countOutboxMatching(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID, pattern string) int {
	t.Helper()
	var n int
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := pool.QueryRow(ctx,
		`SELECT count(*) FROM wa_outbox WHERE booking_id=$1 AND dedupe_key LIKE $2`,
		bookingID, pattern).Scan(&n); err != nil {
		t.Fatalf("count outbox rows like %s: %v", pattern, err)
	}
	return n
}
