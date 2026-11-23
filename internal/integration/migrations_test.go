//go:build integration

package integration

import (
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/jackc/pgx/v5/pgxpool"
)

// TestMigrationsFromBlank proves the embedded Goose chain builds this schema
// from nothing, twice, and that the booked version is the one the source tree
// describes.
//
// Cycle one is TestMain's own reset+migrate: it dropped `public` (taking
// goose_db_version with it) before the package's first test ran. Cycle two is
// explicit here because "a second blank rebuild reaches the same version" is
// the property CI actually depends on — a chain that only works on a virgin
// container would pass once and fail on every retry.
func TestMigrationsFromBlank(t *testing.T) {
	pool := OpenTestDB(t)
	want := expectedMigrationVersion(t)

	if got := maxAppliedVersion(t, pool); got != want {
		t.Fatalf("TestMain's reset+migrate reached version %d, want %d (internal/db/migrations/*.sql)", got, want)
	}
	assertAppliedVersions(t, pool, want)

	ResetSchema(pool)

	if got := maxAppliedVersion(t, pool); got != want {
		t.Fatalf("second reset+migrate reached version %d, want %d", got, want)
	}
	assertAppliedVersions(t, pool, want)

	// Correct version rows could still sit on half-built DDL, so check a table
	// owned by the newest migration (024): its presence proves the last file of
	// the chain ran, not just the bookkeeping.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var hasTVDevices bool
	if err := pool.QueryRow(ctx, `SELECT to_regclass('public.tv_devices') IS NOT NULL`).Scan(&hasTVDevices); err != nil {
		t.Fatalf("migrations: inspect last-migration table: %v", err)
	}
	if !hasTVDevices {
		t.Fatal("migrations: tv_devices (migration 024) is missing after a full chain run")
	}
}

// TestExpectedMigrationVersionIsDerived runs the derivation's own checks (the
// runtime.Caller path into internal/db/migrations, the numeric prefixes, the
// gap-free sequence) without a database. It exists so a broken expectation
// cannot hide behind a skipped DB-backed test on a box with no engine.
func TestExpectedMigrationVersionIsDerived(t *testing.T) {
	expectedMigrationVersion(t)
}

// expectedMigrationVersion derives the chain's last version from the files
// internal/db embeds. The embedded FS is package-private and the plan forbids
// production changes, so the test reads the same directory from the source
// tree — `go test` always runs where the source is present. The number is
// deliberately never hardcoded: adding a migration must move this expectation.
func expectedMigrationVersion(t *testing.T) int64 {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("migrations: cannot locate this test file")
	}
	dir := filepath.Join(filepath.Dir(file), "..", "..", "internal", "db", "migrations")
	files, err := filepath.Glob(filepath.Join(dir, "*.sql"))
	if err != nil {
		t.Fatalf("migrations: glob %s: %v", dir, err)
	}
	if len(files) == 0 {
		t.Fatalf("migrations: no *.sql files under %s - is the source tree present?", dir)
	}

	// Goose versions are the leading digits of the filename (`001_init.sql` ->
	// 1; filepath.Glob sorts names), and the file count equals the highest
	// version only for a gap-free 001..N chain — this repo's convention. A gap
	// or a timestamp version means the derivation needs revisiting, so name
	// that instead of reporting a bogus migration failure.
	for i, name := range files {
		base := filepath.Base(name)
		prefix := base
		if end := strings.IndexFunc(base, func(r rune) bool { return r < '0' || r > '9' }); end >= 0 {
			prefix = base[:end]
		}
		n, err := strconv.ParseInt(prefix, 10, 64)
		if err != nil {
			t.Fatalf("migrations: %s: version prefix is not numeric: %v", base, err)
		}
		if n != int64(i+1) {
			t.Fatalf("migrations: %s is out of sequence: expected version %d", base, i+1)
		}
	}
	return int64(len(files))
}

// maxAppliedVersion reads the highest version goose recorded (0 before the
// first migration).
func maxAppliedVersion(t *testing.T, pool *pgxpool.Pool) int64 {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var version int64
	if err := pool.QueryRow(ctx,
		`SELECT COALESCE(max(version_id), 0) FROM goose_db_version`,
	).Scan(&version); err != nil {
		t.Fatalf("migrations: read goose_db_version: %v", err)
	}
	return version
}

// assertAppliedVersions checks every version 1..want is applied exactly once
// (goose seeds a bookkeeping row for version 0, which is excluded) and that no
// down-migration row is left behind.
func assertAppliedVersions(t *testing.T, pool *pgxpool.Pool, want int64) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	var applied, rolledBack int64
	if err := pool.QueryRow(ctx, `SELECT
			count(DISTINCT version_id) FILTER (WHERE is_applied AND version_id > 0),
			count(*) FILTER (WHERE NOT is_applied)
		FROM goose_db_version`).Scan(&applied, &rolledBack); err != nil {
		t.Fatalf("migrations: summarise goose_db_version: %v", err)
	}
	if applied != want {
		t.Fatalf("goose recorded %d applied versions, want %d", applied, want)
	}
	if rolledBack != 0 {
		t.Fatalf("goose recorded %d rolled-back rows after an up-only chain", rolledBack)
	}
}
