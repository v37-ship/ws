//go:build integration

package integration

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/db"
	"spa-booking/internal/domain"
)

var (
	// testDSN is the guarded TEST_DATABASE_URL, set once by TestMain.
	testDSN string
	// testPool is the shared pool; TestMain owns its lifetime.
	testPool *pgxpool.Pool
	// dbUnavailable is true when TEST_DATABASE_URL was unset outside CI:
	// guard-only tests still run, DB-backed tests skip through OpenTestDB.
	dbUnavailable bool
)

// TestMain guards the URL, connects, resets the schema from blank (dropping
// everything, so the embedded Goose chain proves itself) and only then runs
// the package. Missing TEST_DATABASE_URL skips DB work locally with a loud
// message but fails when CI is set — a silent green is the one outcome the
// suite must never produce.
func TestMain(m *testing.M) {
	testDSN = strings.TrimSpace(os.Getenv("TEST_DATABASE_URL"))
	if testDSN == "" {
		if ciEnvSet() {
			fmt.Fprintln(os.Stderr, "integration: failing: TEST_DATABASE_URL is required when CI is set")
			os.Exit(1)
		}
		fmt.Fprintln(os.Stderr, "integration: TEST_DATABASE_URL is unset - DB-backed tests will SKIP "+
			"(point it at a disposable *_test database on loopback or the compose `postgres` host)")
		dbUnavailable = true
		os.Exit(m.Run())
	}

	if _, err := parseSafeDB(testDSN); err != nil {
		fmt.Fprintf(os.Stderr, "integration: refusing to run: %v\n", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	pool, err := connectTestPool(ctx, testDSN)
	if err != nil {
		cancel()
		fmt.Fprintf(os.Stderr, "integration: connect: %v\n", err)
		os.Exit(1)
	}
	// The report rollover test's premise is that a naive read of a timestamptz
	// agrees with UTC: only then does the WIB conversion visibly move the row.
	// A dev cluster whose default TimeZone is Asia/Jakarta would make the two
	// readings agree and quietly disarm that test, so pin it and prove it.
	var sessionTZ string
	if err := pool.QueryRow(ctx, `SHOW TimeZone`).Scan(&sessionTZ); err != nil {
		cancel()
		pool.Close()
		fmt.Fprintf(os.Stderr, "integration: read session TimeZone: %v\n", err)
		os.Exit(1)
	}
	if !strings.EqualFold(sessionTZ, "UTC") {
		cancel()
		pool.Close()
		fmt.Fprintf(os.Stderr, "integration: refusing to run: session TimeZone is %q, want UTC "+
			"(the harness pins it; a pool without that pin would disarm the report rollover test)\n", sessionTZ)
		os.Exit(1)
	}
	if err := resetSchema(ctx, pool, testDSN); err != nil {
		cancel()
		pool.Close()
		fmt.Fprintf(os.Stderr, "integration: reset schema: %v\n", err)
		os.Exit(1)
	}
	cancel()

	testPool = pool
	code := m.Run()
	pool.Close()
	os.Exit(code)
}

// connectTestPool opens the guarded pool with every session's TimeZone pinned
// to UTC. db.Connect cannot express that, and a one-shot `SET TIME ZONE` only
// touches one pooled connection, so the setting belongs in the conn config.
func connectTestPool(ctx context.Context, dsn string) (*pgxpool.Pool, error) {
	cfg, err := pgxpool.ParseConfig(dsn)
	if err != nil {
		return nil, err
	}
	if cfg.ConnConfig.RuntimeParams == nil {
		cfg.ConnConfig.RuntimeParams = map[string]string{}
	}
	cfg.ConnConfig.RuntimeParams["timezone"] = "UTC"
	pool, err := pgxpool.NewWithConfig(ctx, cfg)
	if err != nil {
		return nil, err
	}
	if err := pool.Ping(ctx); err != nil {
		pool.Close()
		return nil, err
	}
	return pool, nil
}

// ciEnvSet reports whether the environment looks like CI (GitHub Actions sets
// CI=true). An explicit CI=false/0 counts as local.
func ciEnvSet() bool {
	v := strings.TrimSpace(os.Getenv("CI"))
	return v != "" && v != "0" && !strings.EqualFold(v, "false")
}

// requireSafeDB parses and hard-fails unsafe URLs. The database must be named
// spa_test or end in _test, and the host must be loopback or the compose
// service name `postgres`. The same cluster holds `spa`, `umami` and
// `evolution`; a deny-list would let a typo wipe them, so this is an allowlist.
// Messages never carry credentials: the reported URL is rebuilt without
// userinfo or query values.
func requireSafeDB(t *testing.T, raw string) *url.URL {
	t.Helper()
	u, err := parseSafeDB(raw)
	if err != nil {
		t.Fatalf("integration: unsafe TEST_DATABASE_URL: %v", err)
	}
	return u
}

// parseSafeDB is the pure guard behind requireSafeDB (and TestMain), so the
// guard's own tests need no database.
func parseSafeDB(raw string) (*url.URL, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, errors.New("empty database URL")
	}
	u, err := url.Parse(raw)
	if err != nil {
		// Never echo raw: an unparseable URL may still contain a password.
		return nil, errors.New("unparseable database URL")
	}
	if scheme := strings.ToLower(u.Scheme); scheme != "postgres" && scheme != "postgresql" {
		return nil, fmt.Errorf("%s: scheme must be postgres:// or postgresql://", redactedURL(u))
	}
	if u.Opaque != "" {
		return nil, fmt.Errorf("%s: unsupported opaque URL form", redactedURL(u))
	}

	host := strings.ToLower(u.Hostname())
	if host == "" {
		return nil, fmt.Errorf("%s: missing host", redactedURL(u))
	}
	if !safeHost(host) {
		return nil, fmt.Errorf("%s: host %q is not loopback or `postgres`", redactedURL(u), host)
	}

	// pgx canonicalises libpq keywords — a `dbname` parameter is renamed to
	// `database` — and query parameters merge AFTER the URL path, so both
	// `?dbname=spa` and `?database=spa` re-point this connection at the live
	// database the mirrored path says is spa_test. A deny-list cannot track
	// that (nor `?host`, `?password`, `?user`, `?options`), so only the three
	// harmless tuning keys are allowed and every other key is a hard failure.
	// Do not widen this without re-checking pgx's keyword canonicalisation.
	for key := range u.Query() {
		switch key {
		case "sslmode", "connect_timeout", "application_name":
		default:
			return nil, fmt.Errorf("%s: query parameter %q is not on the harness allowlist (sslmode, connect_timeout, application_name)", redactedURL(u), key)
		}
	}

	name, unescapeErr := url.PathUnescape(strings.TrimPrefix(u.EscapedPath(), "/"))
	if unescapeErr != nil || name == "" || strings.Contains(name, "/") {
		return nil, fmt.Errorf("%s: missing database name", redactedURL(u))
	}
	if name != "spa_test" && !strings.HasSuffix(name, "_test") {
		return nil, fmt.Errorf("%s: database %q is not allowlisted (must be spa_test or end in _test)", redactedURL(u), name)
	}
	return u, nil
}

// safeHost accepts loopback hosts and the compose service name.
func safeHost(host string) bool {
	if host == "postgres" || host == "localhost" {
		return true
	}
	if ip := net.ParseIP(host); ip != nil {
		return ip.IsLoopback()
	}
	return false
}

// redactedURL renders a URL without userinfo, query values or fragment, so an
// error message can identify the target without leaking a password.
func redactedURL(u *url.URL) string {
	safe := *u
	safe.User = nil
	safe.RawQuery = ""
	safe.Fragment = ""
	return safe.String()
}

// ResetSchema drops and recreates `public` — taking goose_db_version and every
// table with it — then reapplies the embedded Goose chain. TestMain calls it
// once per package; a test may call it again to prove idempotency. It panics
// on failure rather than calling os.Exit, so a failing test is reported as a
// test failure instead of killing the binary.
func ResetSchema(pool *pgxpool.Pool) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if err := resetSchema(ctx, pool, testDSN); err != nil {
		panic("integration: " + err.Error())
	}
}

func resetSchema(ctx context.Context, pool *pgxpool.Pool, dsn string) error {
	for _, stmt := range []string{
		`DROP SCHEMA IF EXISTS public CASCADE`,
		`CREATE SCHEMA public`,
	} {
		if _, err := pool.Exec(ctx, stmt); err != nil {
			return fmt.Errorf("%s: %w", stmt, err)
		}
	}
	if err := db.Migrate(dsn); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}
	return nil
}

// TruncateAll empties every public table except goose_db_version, CASCADEing
// through foreign keys. Tests call it first so each starts from a known blank
// state without paying for a full migration run.
func TruncateAll(t *testing.T, pool *pgxpool.Pool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()

	rows, err := pool.Query(ctx, `SELECT table_name
		FROM information_schema.tables
		WHERE table_schema = 'public'
		  AND table_type = 'BASE TABLE'
		  AND table_name <> 'goose_db_version'
		ORDER BY table_name`)
	if err != nil {
		t.Fatalf("truncate: list tables: %v", err)
	}
	tables, err := pgx.CollectRows(rows, pgx.RowTo[string])
	if err != nil {
		t.Fatalf("truncate: read tables: %v", err)
	}
	if len(tables) == 0 {
		t.Fatal("truncate: no user tables in public - did TestMain run the migrations?")
	}

	quoted := make([]string, len(tables))
	for i, name := range tables {
		quoted[i] = pgx.Identifier{name}.Sanitize()
	}
	if _, err := pool.Exec(ctx, "TRUNCATE TABLE "+strings.Join(quoted, ", ")+" CASCADE"); err != nil {
		t.Fatalf("truncate: %v", err)
	}
}

// OpenTestDB returns the shared pool. The guard and schema reset already ran in
// TestMain; this helper deliberately does not reset anything, so callers choose
// TruncateAll (the normal case) or inspect state left by earlier steps.
func OpenTestDB(t *testing.T) *pgxpool.Pool {
	t.Helper()
	if testPool == nil {
		if dbUnavailable {
			t.Skip("integration: TEST_DATABASE_URL unset - skipping DB-backed test")
		}
		t.Fatal("integration: test pool is nil (TestMain did not initialise it)")
	}
	return testPool
}

// --- seeding helpers -------------------------------------------------------
//
// Minimal plain-SQL fixtures. They exist so the first wave's groups can seed
// rows without repeating column lists; callers set only what their test cares
// about and the zero values fall back to defaults that satisfy the DB checks.

// bookingSeed is the writable subset insertBooking accepts. Required:
// LocationID, ServiceID, StaffID, StartAt (EndAt defaults to +1h). Zero
// values: Status "confirmed", Source "web", Place "onsite". CustomerID nil
// makes it a guest booking.
type bookingSeed struct {
	CustomerID    *uuid.UUID
	LocationID    uuid.UUID
	ServiceID     uuid.UUID
	StaffID       uuid.UUID
	StartAt       time.Time
	EndAt         time.Time
	Status        string
	PriceIDR      int64
	BookingFeeIDR int64
	PromoCode     string
	DiscountIDR   int64
	Place         string
	VisitAddress  string
	GuestName     string
	GuestPhone    string
	GuestEmail    string
	AccessToken   *string
	Source        string
}

// insertBooking writes one booking row and returns its id.
func insertBooking(t *testing.T, pool *pgxpool.Pool, seed bookingSeed) uuid.UUID {
	t.Helper()
	switch {
	case seed.LocationID == uuid.Nil:
		t.Fatal("insertBooking: LocationID is required")
	case seed.ServiceID == uuid.Nil:
		t.Fatal("insertBooking: ServiceID is required")
	case seed.StaffID == uuid.Nil:
		t.Fatal("insertBooking: StaffID is required")
	case seed.StartAt.IsZero():
		t.Fatal("insertBooking: StartAt is required")
	}
	if seed.EndAt.IsZero() {
		seed.EndAt = seed.StartAt.Add(time.Hour)
	}
	if seed.Status == "" {
		seed.Status = string(domain.BookingConfirmed)
	}
	if seed.Source == "" {
		seed.Source = "web"
	}
	if seed.Place == "" {
		seed.Place = string(domain.PlaceOnsite)
	}

	id := uuid.New()
	const q = `INSERT INTO bookings (
			id, customer_id, location_id, service_id, staff_id, start_at, end_at, status,
			price_idr, booking_fee_idr, promo_code, discount_idr, place, visit_address,
			guest_name, guest_phone, guest_email, access_token, source
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16,$17,$18,$19)`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, q,
		id, seed.CustomerID, seed.LocationID, seed.ServiceID, seed.StaffID,
		seed.StartAt, seed.EndAt, seed.Status, seed.PriceIDR, seed.BookingFeeIDR,
		seed.PromoCode, seed.DiscountIDR, seed.Place, seed.VisitAddress,
		seed.GuestName, seed.GuestPhone, seed.GuestEmail, seed.AccessToken, seed.Source,
	); err != nil {
		t.Fatalf("insertBooking: %v", err)
	}
	return id
}

// paymentSeed is the writable subset insertPayment accepts. Required:
// BookingID, AmountIDR. Zero values: Status "paid", Method "qris", Purpose
// "full", and a generated unique OrderID; a paid row without PaidAt gets
// now() so refund/report tests have a real window. CustomerReportedAt is the
// manual-QRIS "customer says they paid" stamp the portal's claim filter reads
// (it never changes the status, which stays pending until staff confirm).
type paymentSeed struct {
	BookingID          uuid.UUID
	OrderID            string
	AmountIDR          int64
	Status             string
	Method             string
	Purpose            string
	PaidAt             *time.Time
	Reference          string
	ReceiptPath        string
	CustomerReportedAt *time.Time
}

// insertPayment writes one payment row (one per booking, per the unique
// constraint) and returns its id.
func insertPayment(t *testing.T, pool *pgxpool.Pool, seed paymentSeed) uuid.UUID {
	t.Helper()
	if seed.BookingID == uuid.Nil {
		t.Fatal("insertPayment: BookingID is required")
	}
	if seed.Status == "" {
		seed.Status = string(domain.PaymentPaid)
	}
	if seed.Method == "" {
		seed.Method = string(domain.PaymentMethodQRIS)
	}
	if seed.Purpose == "" {
		seed.Purpose = string(domain.PaymentPurposeFull)
	}
	if seed.OrderID == "" {
		seed.OrderID = "test-" + uuid.NewString()
	}
	if seed.PaidAt == nil && seed.Status == string(domain.PaymentPaid) {
		now := time.Now()
		seed.PaidAt = &now
	}

	id := uuid.New()
	const q = `INSERT INTO payments (
			id, booking_id, order_id, amount_idr, status, payment_method, payment_purpose,
			reference, receipt_path, paid_at, customer_reported_at
		) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)`

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx, q,
		id, seed.BookingID, seed.OrderID, seed.AmountIDR, seed.Status,
		seed.Method, seed.Purpose, seed.Reference, seed.ReceiptPath, seed.PaidAt,
		seed.CustomerReportedAt,
	); err != nil {
		t.Fatalf("insertPayment: %v", err)
	}
	return id
}

// insertLog writes one booking_logs row (the audit trail undo derives from)
// and returns its id.
func insertLog(t *testing.T, pool *pgxpool.Pool, bookingID uuid.UUID, userID *uuid.UUID, action, notes string) uuid.UUID {
	t.Helper()
	if bookingID == uuid.Nil {
		t.Fatal("insertLog: bookingID is required")
	}
	if action == "" {
		t.Fatal("insertLog: action is required")
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO booking_logs (id, booking_id, user_id, action, notes) VALUES ($1,$2,$3,$4,$5)`,
		id, bookingID, userID, action, notes,
	); err != nil {
		t.Fatalf("insertLog: %v", err)
	}
	return id
}

// insertLocation writes one active branch and returns its id.
func insertLocation(t *testing.T, pool *pgxpool.Pool, name string) uuid.UUID {
	t.Helper()
	if name == "" {
		t.Fatal("insertLocation: name is required")
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO locations (id, name) VALUES ($1,$2)`, id, name,
	); err != nil {
		t.Fatalf("insertLocation: %v", err)
	}
	return id
}

// insertService writes one active 60-minute treatment and returns its id.
func insertService(t *testing.T, pool *pgxpool.Pool, locationID uuid.UUID, name string, priceIDR int64) uuid.UUID {
	t.Helper()
	if locationID == uuid.Nil {
		t.Fatal("insertService: locationID is required")
	}
	if name == "" {
		t.Fatal("insertService: name is required")
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO services (id, location_id, name, duration_min, price_idr) VALUES ($1,$2,$3,$4,$5)`,
		id, locationID, name, 60, priceIDR,
	); err != nil {
		t.Fatalf("insertService: %v", err)
	}
	return id
}

// insertStaff writes one active therapist at a branch and returns its id.
func insertStaff(t *testing.T, pool *pgxpool.Pool, locationID uuid.UUID, name string) uuid.UUID {
	t.Helper()
	if locationID == uuid.Nil {
		t.Fatal("insertStaff: locationID is required")
	}
	if name == "" {
		t.Fatal("insertStaff: name is required")
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO staff (id, location_id, name) VALUES ($1,$2,$3)`, id, locationID, name,
	); err != nil {
		t.Fatalf("insertStaff: %v", err)
	}
	return id
}

// insertUser writes one customer/staff account and returns its id. The
// password hash is a placeholder: no fixture logs in.
func insertUser(t *testing.T, pool *pgxpool.Pool, email, role string) uuid.UUID {
	t.Helper()
	if email == "" {
		t.Fatal("insertUser: email is required")
	}
	if role == "" {
		role = string(domain.RoleCustomer)
	}
	id := uuid.New()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if _, err := pool.Exec(ctx,
		`INSERT INTO users (id, email, password_hash, full_name, role) VALUES ($1,$2,$3,$4,$5)`,
		id, email, "x", "Fixture "+email, role,
	); err != nil {
		t.Fatalf("insertUser: %v", err)
	}
	return id
}

// TestRequireSafeDB proves the guard without a database: it must reject the
// live `spa` database, the cluster's other tenants, and every non-loopback
// host, while accepting disposable *_test databases.
func TestRequireSafeDB(t *testing.T) {
	reject := []string{
		"postgres://spa:spa@localhost:5432/spa",
		"postgres://spa:spa@localhost:5432/umami",
		"postgres://spa:spa@localhost:5432/evolution",
		"postgres://spa:spa@127.0.0.1:5432/spa",
		"postgres://spa:spa@db.example.com:5432/spa_test",
		"postgres://spa:spa@10.0.0.5:5432/spa_test",
		"postgres://spa:spa@localhost:5432/",
		"postgres://spa:spa@localhost:5432/other_schema/spa_test",
		"mysql://spa:spa@localhost:5432/spa_test",
		"not a url",
		"",
		// Query overrides would sidestep the host/database checks. pgx renames
		// dbname to database and merges query params after the path, so both
		// spellings must fail, as must every key outside the allowlist.
		"postgres://spa:spa@localhost:5432/spa_test?host=evil.example.com",
		"postgres://spa:spa@localhost:5432/spa_test?dbname=spa",
		"postgres://spa:spa@localhost:5432/spa_test?database=spa",
		"postgres://spa:spa@localhost:5432/spa_test?host=evil",
		"postgres://spa:spa@localhost:5432/spa_test?password=x",
		"postgres://spa:spa@localhost:5432/spa_test?user=postgres",
		"postgres://spa:spa@localhost:5432/spa_test?options=-csearch_path%3Dspa",
		"postgres://spa:spa@localhost:5432/spa_test?unknown_key=1",
	}
	for _, raw := range reject {
		if _, err := parseSafeDB(raw); err == nil {
			t.Errorf("parseSafeDB(%q) accepted an unsafe URL", raw)
		}
	}

	accept := []string{
		"postgres://spa:spa@localhost:5432/spa_test",
		"postgres://spa:spa@127.0.0.1:5432/spa_test?sslmode=disable",
		"postgres://spa:spa@127.0.0.1:5432/spa_test?sslmode=disable&connect_timeout=5&application_name=integration",
		"postgres://spa:spa@[::1]:5432/spa_test",
		"postgres://spa:spa@postgres:5432/spa_test",
		"postgresql://spa:spa@localhost:5432/scratch_test",
	}
	for _, raw := range accept {
		if _, err := parseSafeDB(raw); err != nil {
			t.Errorf("parseSafeDB(%q) rejected a safe URL: %v", raw, err)
		}
	}

	// The refusal message must never echo the password or query values.
	const secret = "hunter2-secret"
	_, err := parseSafeDB("postgres://spa:" + secret + "@localhost:5432/spa?host=evil.example.com")
	if err == nil {
		t.Fatal("expected the guarded URL to be rejected")
	}
	for _, leak := range []string{secret, "evil.example.com"} {
		if strings.Contains(err.Error(), leak) {
			t.Errorf("guard error leaked %q: %v", leak, err)
		}
	}
}

// TestHarnessSeedingRoundTrip exercises the helpers against the real schema so
// a column drift breaks this package instead of a downstream group.
func TestHarnessSeedingRoundTrip(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	loc := insertLocation(t, pool, "Harness Branch")
	svc := insertService(t, pool, loc, "Harness Treatment", 150_000)
	staff := insertStaff(t, pool, loc, "Harness Therapist")
	cust := insertUser(t, pool, "harness@example.com", string(domain.RoleCustomer))

	start := time.Date(2026, 11, 23, 10, 0, 0, 0, time.UTC)
	bookingID := insertBooking(t, pool, bookingSeed{
		CustomerID: &cust,
		LocationID: loc,
		ServiceID:  svc,
		StaffID:    staff,
		StartAt:    start,
		EndAt:      start.Add(time.Hour),
		PriceIDR:   150_000,
	})
	paidAt := start.Add(-24 * time.Hour)
	paymentID := insertPayment(t, pool, paymentSeed{
		BookingID: bookingID,
		AmountIDR: 150_000,
		PaidAt:    &paidAt,
	})
	logID := insertLog(t, pool, bookingID, &cust, domain.StatusLogAction(domain.BookingConfirmed), "seed")

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var bookings, payments, logs int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM bookings WHERE id = $1`, bookingID).Scan(&bookings); err != nil {
		t.Fatalf("read booking: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM payments WHERE id = $1 AND paid_at IS NOT NULL`, paymentID).Scan(&payments); err != nil {
		t.Fatalf("read payment: %v", err)
	}
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM booking_logs WHERE id = $1`, logID).Scan(&logs); err != nil {
		t.Fatalf("read log: %v", err)
	}
	if bookings != 1 || payments != 1 || logs != 1 {
		t.Fatalf("seeded rows missing: bookings=%d payments=%d logs=%d", bookings, payments, logs)
	}

	TruncateAll(t, pool)
	var remaining int
	if err := pool.QueryRow(ctx, `SELECT
			(SELECT count(*) FROM bookings) + (SELECT count(*) FROM payments) +
			(SELECT count(*) FROM booking_logs) + (SELECT count(*) FROM users) +
			(SELECT count(*) FROM locations) + (SELECT count(*) FROM services) +
			(SELECT count(*) FROM staff)`).Scan(&remaining); err != nil {
		t.Fatalf("read after truncate: %v", err)
	}
	if remaining != 0 {
		t.Fatalf("TruncateAll left %d rows behind across bookings, payments, booking_logs, users, locations, services and staff", remaining)
	}
}
