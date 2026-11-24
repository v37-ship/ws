//go:build integration

package integration

import (
	"bytes"
	"context"
	"errors"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/jackc/pgx/v5/pgxpool"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
)

// The payroll tables are money history: staff_comp is the rate timeline (a new
// row supersedes, nothing is edited) and staff_payouts is an immutable
// per-period snapshot. These groups execute the real DDL and the real repo SQL,
// because the two rules a unit test cannot reach are exactly the ones that
// would corrupt money if the schema drifted:
//
//   - rate resolution is "latest effective_from <= period_start", so a period
//     that starts between two rows must not pick the later one; and
//   - one staff member's period is marked paid exactly once, enforced by the
//     database rather than by a caller remembering to check.

// payrollDate is a date-only fixture in UTC, matching how pgx scans DATE.
func payrollDate(year int, month time.Month, day int) time.Time {
	return time.Date(year, month, day, 0, 0, 0, 0, time.UTC)
}

// TestStaffCompEffectiveResolution pins the rate timeline's read order. The
// period-start boundary is the whole point of the history: a raise effective
// on the 1st applies to a period starting that same day, never to one that
// started the day before.
func TestStaffCompEffectiveResolution(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	ctx := context.Background()
	loc := insertLocation(t, pool, "Payroll Branch")
	therapist := insertStaff(t, pool, loc, "Payroll Therapist")
	admin := insertUser(t, pool, "payroll-admin@example.com", string(domain.RoleAdmin))
	comps := repo.NewStaffCompRepo(pool)

	older := &domain.StaffComp{
		StaffID:        therapist,
		PercentBp:      2500,
		FixedIDR:       100_000,
		Cadence:        domain.CompCadenceMonthly,
		CommissionBase: domain.CompBaseList,
		EffectiveFrom:  payrollDate(2026, time.January, 1),
		CreatedBy:      &admin,
	}
	newer := &domain.StaffComp{
		StaffID:        therapist,
		PercentBp:      3000,
		FixedIDR:       150_000,
		Cadence:        domain.CompCadenceWeekly,
		CommissionBase: domain.CompBaseNet,
		EffectiveFrom:  payrollDate(2026, time.June, 1),
	}
	if err := comps.Create(ctx, older); err != nil {
		t.Fatalf("create older rate: %v", err)
	}
	if err := comps.Create(ctx, newer); err != nil {
		t.Fatalf("create newer rate: %v", err)
	}
	if older.ID == uuid.Nil || newer.ID == uuid.Nil || older.ID == newer.ID {
		t.Fatalf("Create must assign distinct ids: older=%v newer=%v", older.ID, newer.ID)
	}
	if older.CreatedAt.IsZero() || newer.CreatedAt.IsZero() {
		t.Fatalf("Create must return created_at: older=%v newer=%v", older.CreatedAt, newer.CreatedAt)
	}

	cases := []struct {
		name      string
		period    time.Time
		wantID    uuid.UUID
		wantFound bool
	}{
		{"before the first row", payrollDate(2025, time.December, 31), uuid.Nil, false},
		{"between rows resolves to the earlier", payrollDate(2026, time.March, 15), older.ID, true},
		{"exactly on the newer row resolves to it", payrollDate(2026, time.June, 1), newer.ID, true},
		{"after both resolves to the newest", payrollDate(2027, time.February, 1), newer.ID, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := comps.EffectiveFor(ctx, therapist, tc.period)
			if err != nil {
				t.Fatalf("EffectiveFor(%s): %v", tc.period.Format("2006-01-02"), err)
			}
			if !tc.wantFound {
				if got != nil {
					t.Fatalf("EffectiveFor(%s) = %+v, want (nil, nil) before any rate", tc.period.Format("2006-01-02"), got)
				}
				return
			}
			if got == nil {
				t.Fatalf("EffectiveFor(%s) = nil, want rate %v", tc.period.Format("2006-01-02"), tc.wantID)
			}
			if got.ID != tc.wantID {
				t.Fatalf("EffectiveFor(%s) = %v, want %v", tc.period.Format("2006-01-02"), got.ID, tc.wantID)
			}
		})
	}

	// A staff member with no rate row is a normal state (the payroll page
	// flags it), not an error.
	if got, err := comps.EffectiveFor(ctx, uuid.New(), payrollDate(2026, time.June, 1)); err != nil || got != nil {
		t.Fatalf("EffectiveFor(unknown staff) = (%+v, %v), want (nil, nil)", got, err)
	}

	history, err := comps.HistoryFor(ctx, therapist)
	if err != nil {
		t.Fatalf("HistoryFor: %v", err)
	}
	if len(history) != 2 || history[0].ID != newer.ID || history[1].ID != older.ID {
		t.Fatalf("HistoryFor must be newest first: %+v", history)
	}

	all, err := comps.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll: %v", err)
	}
	if len(all) != 2 {
		t.Fatalf("ListAll = %d rows, want 2", len(all))
	}

	// A second staff member whose rates fall between the first staff's two
	// dates proves ListAll groups by staff rather than by date: a global date
	// sort would alternate the two staff members, which the staff-id/ordering
	// check below rejects whatever the two random UUIDs happen to be. The first
	// row of each group is that staff member's current rate.
	otherStaff := insertStaff(t, pool, loc, "Second Therapist")
	otherRates := []*domain.StaffComp{
		{
			StaffID:        otherStaff,
			PercentBp:      1500,
			FixedIDR:       50_000,
			Cadence:        domain.CompCadenceWeekly,
			CommissionBase: domain.CompBaseNet,
			EffectiveFrom:  payrollDate(2026, time.March, 15), // between the first staff's rows
		},
		{
			StaffID:        otherStaff,
			PercentBp:      2000,
			FixedIDR:       75_000,
			Cadence:        domain.CompCadenceMonthly,
			CommissionBase: domain.CompBaseList,
			EffectiveFrom:  payrollDate(2026, time.August, 15),
		},
	}
	for i, c := range otherRates {
		if err := comps.Create(ctx, c); err != nil {
			t.Fatalf("create second staff rate %d: %v", i, err)
		}
	}

	all, err = comps.ListAll(ctx)
	if err != nil {
		t.Fatalf("ListAll after second staff: %v", err)
	}
	if len(all) != 4 {
		t.Fatalf("ListAll after second staff = %d rows, want 4", len(all))
	}
	// Order is (staff_id, effective_from DESC): staff ids ascend, and within a
	// staff group effective dates descend. Interleaved dates make any
	// date-first ordering non-contiguous, which the id check below trips.
	for i := 1; i < len(all); i++ {
		prev, cur := all[i-1], all[i]
		switch cmp := bytes.Compare(prev.StaffID[:], cur.StaffID[:]); {
		case cmp > 0:
			t.Fatalf("ListAll row %d belongs to a lower staff id than row %d: %v then %v",
				i, i-1, prev.StaffID, cur.StaffID)
		case cmp == 0 && cur.EffectiveFrom.After(prev.EffectiveFrom):
			t.Fatalf("ListAll staff %v rates are not newest-first: %s then %s",
				cur.StaffID, prev.EffectiveFrom.Format("2006-01-02"), cur.EffectiveFrom.Format("2006-01-02"))
		}
	}
	// First row per staff group is what rate management shows as "current".
	firstByStaff := map[uuid.UUID]uuid.UUID{}
	for _, c := range all {
		if _, ok := firstByStaff[c.StaffID]; !ok {
			firstByStaff[c.StaffID] = c.ID
		}
	}
	if firstByStaff[therapist] != newer.ID {
		t.Fatalf("current rate for the first therapist = %v, want %v", firstByStaff[therapist], newer.ID)
	}
	if firstByStaff[otherStaff] != otherRates[1].ID {
		t.Fatalf("current rate for the second therapist = %v, want %v", firstByStaff[otherStaff], otherRates[1].ID)
	}
}

// TestStaffPayoutUniquePeriod pins the "mark paid once" contract: the database
// owns it, so a double click or two tabs cannot write two snapshots for the
// same staff/kind/period. It also round-trips a negative commission, which a
// late refund for an earlier visit legitimately produces.
func TestStaffPayoutUniquePeriod(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	ctx := context.Background()
	loc := insertLocation(t, pool, "Payroll Branch")
	therapist := insertStaff(t, pool, loc, "Payroll Therapist")
	other := insertStaff(t, pool, loc, "Other Therapist")
	admin := insertUser(t, pool, "payroll-admin@example.com", string(domain.RoleAdmin))
	payouts := repo.NewStaffPayoutRepo(pool)

	periodStart := payrollDate(2026, time.November, 1)
	periodEnd := payrollDate(2026, time.November, 30)
	paidAt := time.Date(2026, time.November, 24, 10, 0, 0, 0, time.UTC)

	first := &domain.StaffPayout{
		StaffID:       therapist,
		PeriodKind:    string(domain.PeriodMonthly),
		PeriodStart:   periodStart,
		PeriodEnd:     periodEnd,
		GrossIDR:      1_500_000,
		PercentBp:     2500,
		FixedIDR:      100_000,
		CommissionIDR: -25_000, // a late refund can make a period's commission negative
		TotalIDR:      175_000,
		Note:          "adjustment: late refund from October",
		PaidBy:        &admin,
		PaidAt:        paidAt,
	}
	if err := payouts.Create(ctx, first); err != nil {
		t.Fatalf("create payout: %v", err)
	}
	if first.ID == uuid.Nil {
		t.Fatal("Create must assign an id")
	}

	dup := *first
	dup.ID = uuid.Nil
	err := payouts.Create(ctx, &dup)
	if err == nil {
		t.Fatalf("a second payout for the same staff/kind/start was accepted; want a unique violation")
	}
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) || pgErr.Code != "23505" {
		t.Fatalf("duplicate payout error = %v, want SQLSTATE 23505 (unique violation)", err)
	}

	// A different staff member may be paid for the same period, and the same
	// staff member may be paid for a different period.
	okOther := *first
	okOther.ID = uuid.Nil
	okOther.StaffID = other
	if err := payouts.Create(ctx, &okOther); err != nil {
		t.Fatalf("same period for another staff member must be allowed: %v", err)
	}
	okNext := *first
	okNext.ID = uuid.Nil
	okNext.PeriodStart = payrollDate(2026, time.December, 1)
	okNext.PeriodEnd = payrollDate(2026, time.December, 31)
	if err := payouts.Create(ctx, &okNext); err != nil {
		t.Fatalf("next period for the same staff member must be allowed: %v", err)
	}

	got, err := payouts.ForPeriod(ctx, therapist, string(domain.PeriodMonthly), periodStart)
	if err != nil {
		t.Fatalf("ForPeriod: %v", err)
	}
	if got == nil {
		t.Fatal("ForPeriod after Create = nil, want the snapshot")
	}
	if got.ID != first.ID {
		t.Fatalf("ForPeriod id = %v, want %v", got.ID, first.ID)
	}
	if got.GrossIDR != 1_500_000 || got.PercentBp != 2500 || got.FixedIDR != 100_000 ||
		got.CommissionIDR != -25_000 || got.TotalIDR != 175_000 {
		t.Fatalf("ForPeriod money round-trip drifted: %+v", got)
	}
	if got.PeriodEnd.Format("2006-01-02") != "2026-11-30" {
		t.Fatalf("ForPeriod period_end = %s, want 2026-11-30", got.PeriodEnd.Format("2006-01-02"))
	}
	if got.Note != first.Note {
		t.Fatalf("ForPeriod note = %q, want %q", got.Note, first.Note)
	}
	if got.PaidBy == nil || *got.PaidBy != admin {
		t.Fatalf("ForPeriod paid_by = %v, want %v", got.PaidBy, admin)
	}
	if !got.PaidAt.Equal(paidAt) {
		t.Fatalf("ForPeriod paid_at = %v, want %v", got.PaidAt, paidAt)
	}

	// Not-yet-paid is a normal state, not an error: the portal renders the
	// badge off a nil snapshot.
	if got, err := payouts.ForPeriod(ctx, therapist, string(domain.PeriodWeekly), periodStart); err != nil || got != nil {
		t.Fatalf("ForPeriod(unpaid kind) = (%+v, %v), want (nil, nil)", got, err)
	}

	list, err := payouts.ListFor(ctx, payrollDate(2026, time.November, 1), payrollDate(2026, time.December, 31))
	if err != nil {
		t.Fatalf("ListFor: %v", err)
	}
	if len(list) != 3 {
		t.Fatalf("ListFor(Nov-1..Dec-31) = %d rows, want 3", len(list))
	}
	if !list[0].PeriodStart.After(list[len(list)-1].PeriodStart) {
		t.Fatalf("ListFor must return newest period first: %v", list)
	}
}

// TestPayrollCheckConstraints pins the enum and money guards at the schema
// level. The portal validates too, but these rows are money: a direct SQL
// insert or a future writer must not be able to store a bogus cadence, an
// unknown commission base, or a percentage outside 0–100%.
func TestPayrollCheckConstraints(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	ctx := context.Background()
	loc := insertLocation(t, pool, "Payroll Branch")
	therapist := insertStaff(t, pool, loc, "Payroll Therapist")
	admin := insertUser(t, pool, "payroll-admin@example.com", string(domain.RoleAdmin))

	const insComp = `INSERT INTO staff_comp
		(staff_id, percent_bp, fixed_idr, cadence, commission_base, effective_from, created_by)
		VALUES ($1,$2,$3,$4,$5,$6,$7)`
	const insPayout = `INSERT INTO staff_payouts
		(staff_id, period_kind, period_start, period_end, gross_idr, percent_bp,
		 fixed_idr, commission_idr, total_idr, note, paid_by, paid_at)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12)`

	eff := payrollDate(2026, time.November, 1)
	compCases := []struct {
		name           string
		percentBp      int
		fixedIDR       int64
		cadence        string
		commissionBase string
		wantErr        bool
	}{
		{"valid", 2500, 100_000, "monthly", "list", false},
		{"valid weekly net", 0, 0, "weekly", "net", false},
		{"bad cadence", 2500, 0, "daily", "list", true},
		{"bad commission base", 2500, 0, "monthly", "gross", true},
		{"percent above 100%", 10_001, 0, "monthly", "list", true},
		{"negative percent", -1, 0, "monthly", "list", true},
		{"negative fixed", 2500, -1, "monthly", "list", true},
	}
	for _, tc := range compCases {
		t.Run("staff_comp/"+tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, insComp, therapist, tc.percentBp, tc.fixedIDR,
				tc.cadence, tc.commissionBase, eff, admin)
			if tc.wantErr && err == nil {
				t.Fatalf("insert cadence=%q base=%q percent=%d fixed=%d was accepted; want a CHECK violation",
					tc.cadence, tc.commissionBase, tc.percentBp, tc.fixedIDR)
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("valid row rejected: %v", err)
			}
			// Clean up so the "valid" subtests of the next case do not collide.
			if err == nil {
				if _, err := pool.Exec(ctx, `DELETE FROM staff_comp WHERE staff_id=$1`, therapist); err != nil {
					t.Fatalf("cleanup staff_comp: %v", err)
				}
			}
		})
	}

	// The design spec's default is net (commission on the discounted amount),
	// not list. Pin it by inserting without naming the column: a future DDL
	// edit that flips the default must fail here, not at the first marked-paid
	// period.
	t.Run("staff_comp/commission base defaults to net", func(t *testing.T) {
		var base string
		if err := pool.QueryRow(ctx, `INSERT INTO staff_comp (staff_id, effective_from)
			VALUES ($1,$2) RETURNING commission_base`, therapist, eff).Scan(&base); err != nil {
			t.Fatalf("insert with defaults: %v", err)
		}
		if base != "net" {
			t.Fatalf("default commission_base = %q, want %q", base, "net")
		}
		if _, err := pool.Exec(ctx, `DELETE FROM staff_comp WHERE staff_id=$1`, therapist); err != nil {
			t.Fatalf("cleanup staff_comp: %v", err)
		}
	})

	payoutCases := []struct {
		name      string
		kind      string
		percentBp int
		start     time.Time
		end       time.Time
		wantErr   bool
	}{
		{"valid", "monthly", 2500, eff, payrollDate(2026, time.November, 30), false},
		{"bad period kind", "daily", 2500, eff, payrollDate(2026, time.November, 30), true},
		{"period end before start", "monthly", 2500, eff, payrollDate(2026, time.October, 31), true},
		{"percent above 100%", "monthly", 10_001, eff, payrollDate(2026, time.November, 30), true},
	}
	for _, tc := range payoutCases {
		t.Run("staff_payouts/"+tc.name, func(t *testing.T) {
			_, err := pool.Exec(ctx, insPayout, therapist, tc.kind, tc.start, tc.end,
				1_000_000, tc.percentBp, 50_000, 10_000, 60_000, "", admin, time.Now())
			if tc.wantErr && err == nil {
				t.Fatalf("insert kind=%q percent=%d start=%s end=%s was accepted; want a CHECK violation",
					tc.kind, tc.percentBp, tc.start.Format("2006-01-02"), tc.end.Format("2006-01-02"))
			}
			if !tc.wantErr && err != nil {
				t.Fatalf("valid payout rejected: %v", err)
			}
			if err == nil {
				if _, err := pool.Exec(ctx, `DELETE FROM staff_payouts WHERE staff_id=$1`, therapist); err != nil {
					t.Fatalf("cleanup staff_payouts: %v", err)
				}
			}
		})
	}
	// A negative commission and total must be storable: a late refund with no
	// visits in the period legitimately drives a payout below zero.
	if _, err := pool.Exec(ctx, insPayout, therapist, "monthly", eff, payrollDate(2026, time.November, 30),
		0, 2500, 0, -12_500, -12_500, "late refund", admin, time.Now()); err != nil {
		t.Fatalf("negative commission/total must be allowed: %v", err)
	}

	// FK contracts: rate rows cascade with a removed therapist, payout
	// snapshots do not (a paid period must survive; staff removal is guarded
	// elsewhere when history exists).
	if _, err := pool.Exec(ctx, `DELETE FROM staff_payouts WHERE staff_id=$1`, therapist); err != nil {
		t.Fatalf("cleanup payouts: %v", err)
	}
	if _, err := pool.Exec(ctx, insComp, therapist, 2500, 0, "monthly", "list", eff, admin); err != nil {
		t.Fatalf("seed comp for FK test: %v", err)
	}
	if _, err := pool.Exec(ctx, insPayout, therapist, "monthly", eff, payrollDate(2026, time.November, 30),
		1_000_000, 2500, 0, 25_000, 25_000, "", admin, time.Now()); err != nil {
		t.Fatalf("seed payout for FK test: %v", err)
	}
	_, err := pool.Exec(ctx, `DELETE FROM staff WHERE id=$1`, therapist)
	if err == nil {
		t.Fatal("deleting a therapist with payout history was allowed; want a RESTRICT violation")
	}
	var pgErr *pgconn.PgError
	// ON DELETE RESTRICT's SQLSTATE is version-dependent: PostgreSQL 18 split
	// restrict_violation (23001) out of foreign_key_violation (23503), while
	// PG 16 (CI and the dev compose image) still reports 23503. Accept either;
	// the assertion still rejects CHECK (23514), UNIQUE (23505) and any
	// non-database error.
	if !errors.As(err, &pgErr) || (pgErr.Code != "23001" && pgErr.Code != "23503") {
		t.Fatalf("staff delete with payout history = %v, want an FK/restrict violation (23001 on PG18+, 23503 on PG16)", err)
	}
	if err := truncatePayrollRows(ctx, pool); err != nil {
		t.Fatalf("clear payroll rows: %v", err)
	}
	if _, err := pool.Exec(ctx, insComp, therapist, 2500, 0, "monthly", "list", eff, admin); err != nil {
		t.Fatalf("seed comp for cascade test: %v", err)
	}
	if _, err := pool.Exec(ctx, `DELETE FROM staff WHERE id=$1`, therapist); err != nil {
		t.Fatalf("delete a therapist with only rate rows: %v", err)
	}
	var comps int
	if err := pool.QueryRow(ctx, `SELECT count(*) FROM staff_comp WHERE staff_id=$1`, therapist).Scan(&comps); err != nil {
		t.Fatalf("count comps after cascade: %v", err)
	}
	if comps != 0 {
		t.Fatalf("staff_comp rows survived a staff delete: %d", comps)
	}
}

// truncatePayrollRows clears the payroll tables only, so a test that needs to
// drop a staff row is not blocked by unrelated fixtures.
func truncatePayrollRows(ctx context.Context, pool *pgxpool.Pool) error {
	if _, err := pool.Exec(ctx, `TRUNCATE TABLE staff_payouts, staff_comp CASCADE`); err != nil {
		return err
	}
	return nil
}

// TestPayrollDomainEnums pins the Go constants to the SQL checks, so a typo in
// a constant is caught here instead of as a runtime insert failure in the
// portal.
func TestPayrollDomainEnums(t *testing.T) {
	pairs := []struct {
		name string
		got  string
		want string
	}{
		{"cadence weekly", domain.CompCadenceWeekly, "weekly"},
		{"cadence monthly", domain.CompCadenceMonthly, "monthly"},
		{"base list", domain.CompBaseList, "list"},
		{"base net", domain.CompBaseNet, "net"},
		{"period weekly", domain.PeriodWeekly, "weekly"},
		{"period monthly", domain.PeriodMonthly, "monthly"},
	}
	for _, p := range pairs {
		if p.got != p.want {
			t.Errorf("%s = %q, want %q (the migration's CHECK lists the same value)", p.name, p.got, p.want)
		}
	}
}

// ListStaff feeds the rate form: payroll is not branch-scoped, so the read
// spans every location, keeps inactive therapists (a former therapist still has
// a rate timeline to correct), and is ordered by name.
func TestPayrollListStaff(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	ctx := context.Background()
	branchA := insertLocation(t, pool, "Rate Branch A")
	branchB := insertLocation(t, pool, "Rate Branch B")
	zed := insertStaff(t, pool, branchA, "Zed Therapist")
	ayu := insertStaff(t, pool, branchB, "Ayu Therapist")
	bima := insertStaff(t, pool, branchA, "Bima Therapist")

	staff, err := repo.NewPayrollRepo(pool).ListStaff(ctx)
	if err != nil {
		t.Fatalf("ListStaff: %v", err)
	}
	if len(staff) != 3 {
		t.Fatalf("ListStaff = %d rows, want 3", len(staff))
	}
	wantOrder := []uuid.UUID{ayu, bima, zed}
	for i, want := range wantOrder {
		if staff[i].ID != want {
			t.Fatalf("row %d = %v, want %v (ListStaff must be ordered by name)", i, staff[i].ID, want)
		}
		if staff[i].Name == "" {
			t.Errorf("row %d has no name", i)
		}
	}
	// Ayu lives in the other branch and is still in the list: the read is not
	// branch-scoped (LocationID is deliberately not part of this row).
	if staff[0].ID != ayu || staff[0].Name != "Ayu Therapist" {
		t.Errorf("Ayu (second branch) is missing from the payroll staff read: %+v", staff[0])
	}
	if !staff[0].Active || !staff[2].Active {
		t.Errorf("active flags = %v/%v, want true/true", staff[0].Active, staff[2].Active)
	}

	// Inactive therapists stay in the list: a former therapist's history still
	// needs correcting.
	if _, err := pool.Exec(ctx, `UPDATE staff SET active=false WHERE id=$1`, bima); err != nil {
		t.Fatalf("deactivate: %v", err)
	}
	staff, err = repo.NewPayrollRepo(pool).ListStaff(ctx)
	if err != nil {
		t.Fatalf("ListStaff after deactivate: %v", err)
	}
	if len(staff) != 3 || staff[1].ID != bima {
		t.Fatalf("ListStaff after deactivate = %d rows, want 3 with Bima second", len(staff))
	}
	if staff[1].Active {
		t.Error("Bima is still active after the update")
	}
}

// A later rate never rewrites a past period: resolution is "newest row with
// effective_from <= the period's start", so the new row only pays periods that
// start after it, and the old row stays exactly as written (append-only).
func TestPayrollRateSupersedeKeepsPastPeriod(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)

	ctx := context.Background()
	loc := insertLocation(t, pool, "Rate Branch")
	therapist := insertStaff(t, pool, loc, "Rate Therapist")
	admin := insertUser(t, pool, "rates-admin@example.com", string(domain.RoleAdmin))
	comps := repo.NewStaffCompRepo(pool)

	jan := &domain.StaffComp{
		StaffID:        therapist,
		PercentBp:      2500,
		FixedIDR:       100_000,
		Cadence:        domain.CompCadenceMonthly,
		CommissionBase: domain.CompBaseNet,
		EffectiveFrom:  payrollDate(2026, time.January, 1),
		CreatedBy:      &admin,
	}
	if err := comps.Create(ctx, jan); err != nil {
		t.Fatalf("create January rate: %v", err)
	}
	past := payrollDate(2026, time.January, 15)
	before, err := comps.EffectiveFor(ctx, therapist, past)
	if err != nil {
		t.Fatalf("EffectiveFor before the raise: %v", err)
	}
	if before == nil || before.ID != jan.ID {
		t.Fatalf("january period resolved to %+v, want the January row", before)
	}

	nov := &domain.StaffComp{
		StaffID:        therapist,
		PercentBp:      3000,
		FixedIDR:       150_000,
		Cadence:        domain.CompCadenceMonthly,
		CommissionBase: domain.CompBaseNet,
		EffectiveFrom:  payrollDate(2026, time.November, 24),
		CreatedBy:      &admin,
	}
	if err := comps.Create(ctx, nov); err != nil {
		t.Fatalf("create November rate: %v", err)
	}

	after, err := comps.EffectiveFor(ctx, therapist, past)
	if err != nil {
		t.Fatalf("EffectiveFor after the raise: %v", err)
	}
	if after == nil || after.ID != jan.ID || after.PercentBp != 2500 || after.FixedIDR != 100_000 {
		t.Fatalf("a past period changed under a later row: %+v", after)
	}
	current, err := comps.EffectiveFor(ctx, therapist, payrollDate(2026, time.December, 1))
	if err != nil {
		t.Fatalf("EffectiveFor on the next period: %v", err)
	}
	if current == nil || current.ID != nov.ID {
		t.Fatalf("the new rate does not pay a later period: %+v", current)
	}

	hist, err := comps.HistoryFor(ctx, therapist)
	if err != nil {
		t.Fatalf("HistoryFor: %v", err)
	}
	if len(hist) != 2 || hist[0].ID != nov.ID || hist[1].ID != jan.ID {
		t.Fatalf("history = %+v, want the November row then the January row", hist)
	}
	if hist[1].PercentBp != 2500 || hist[1].FixedIDR != 100_000 {
		t.Fatalf("the superseded row was rewritten: %+v", hist[1])
	}
}
