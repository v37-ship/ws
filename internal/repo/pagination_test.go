package repo

import (
	"fmt"
	"strings"
	"testing"
)

// The list query must paginate in SQL: page N is a LIMIT PageSize window at
// (N-1)*PageSize. This is the seam that replaces the old hard 100-row cap, and
// it is testable without a database because the query builder is pure.
func TestFilteredListSQLPaginates(t *testing.T) {
	cases := []struct {
		page       int
		wantLimit  int
		wantOffset int
	}{
		{page: 1, wantLimit: PageSize, wantOffset: 0},
		{page: 2, wantLimit: PageSize, wantOffset: PageSize},
		{page: 3, wantLimit: PageSize, wantOffset: 2 * PageSize},
	}
	for _, c := range cases {
		q, args := bookingListSQL(FilterParams{Page: c.page})
		if !strings.Contains(q, " LIMIT ") || !strings.Contains(q, " OFFSET ") {
			t.Fatalf("page %d: list SQL is not paginated:\n%s", c.page, q)
		}
		if got := args[len(args)-2]; got != c.wantLimit {
			t.Errorf("page %d: LIMIT arg = %v, want %d", c.page, got, c.wantLimit)
		}
		if got := args[len(args)-1]; got != c.wantOffset {
			t.Errorf("page %d: OFFSET arg = %v, want %d", c.page, got, c.wantOffset)
		}
	}
}

// The unbounded form stays available for callers that are not a portal page
// (dashboard day list, digest); Page is opt-in. The legacy Limit cap is kept
// for the same callers but must not ride along with a page.
func TestFilteredListSQLUnpagedAndLegacyLimit(t *testing.T) {
	q, args := bookingListSQL(FilterParams{Date: "2026-11-21"})
	if strings.Contains(q, " LIMIT ") {
		t.Errorf("no Page and no Limit must stay unbounded:\n%s", q)
	}
	if len(args) != 1 {
		t.Errorf("date filter args = %d, want 1", len(args))
	}

	q, args = bookingListSQL(FilterParams{Limit: 200})
	if !strings.Contains(q, " LIMIT ") || strings.Contains(q, " OFFSET ") {
		t.Errorf("Limit-only callers keep the old cap shape:\n%s", q)
	}
	if got := args[len(args)-1]; got != 200 {
		t.Errorf("Limit arg = %v, want 200", got)
	}

	// Both set: the page wins, so the portal list can never mix a cap into a
	// page window.
	q, args = bookingListSQL(FilterParams{Page: 2, Limit: 100})
	if len(args) != 2 {
		t.Errorf("paged args = %v, want only LIMIT + OFFSET (no legacy cap)", args)
	}
	if got := args[len(args)-2]; got != PageSize {
		t.Errorf("paged LIMIT arg = %v, want %d", got, PageSize)
	}
}

// The count query shares the filter predicate with the list, so the pager's
// "of Y" always describes the same rows the table filtered.
func TestFilteredCountSQL(t *testing.T) {
	q, args := bookingCountSQL(FilterParams{Search: "ana", Payment: "claim"})
	if !strings.HasPrefix(q, "SELECT count(*)") {
		t.Errorf("count SQL must be a count:\n%s", q)
	}
	if strings.Contains(q, " LIMIT ") || strings.Contains(q, " OFFSET ") {
		t.Errorf("count SQL must not paginate:\n%s", q)
	}
	for _, want := range []string{
		"lower(b.guest_name) LIKE $1",
		"p.status = 'pending'",
		"p.customer_reported_at IS NOT NULL",
	} {
		if !strings.Contains(q, want) {
			t.Errorf("count SQL lost the filter predicate %q:\n%s", want, q)
		}
	}
	if len(args) != 1 || args[0] != "%ana%" {
		t.Errorf("count args = %v, want one search pattern", args)
	}
}

// Every filter the list understands must appear in the count too, or the pager
// would promise pages that the rows do not have.
func TestFilteredCountMatchesListPredicate(t *testing.T) {
	f := FilterParams{
		Status:  "confirmed",
		Date:    "2026-11-21",
		Search:  "ana",
		Payment: "unpaid",
		Sort:    "upcoming",
		Page:    2,
	}
	listSQL, listArgs := bookingListSQL(f)
	countSQL, countArgs := bookingCountSQL(f)
	if len(listArgs) < len(countArgs) {
		t.Fatalf("list args %d shorter than count args %d", len(listArgs), len(countArgs))
	}
	if fmt.Sprint(listArgs[:len(countArgs)]) != fmt.Sprint(countArgs) {
		t.Errorf("count args %v do not match the list's filter args %v", countArgs, listArgs[:len(countArgs)])
	}
	// Sorting/paging are list-only; strip them and the predicates must agree.
	where := countSQL[strings.Index(countSQL, " WHERE "):]
	if !strings.Contains(listSQL, where) {
		t.Errorf("list and count disagree on the WHERE predicate:\nlist: %s\ncount: %s", listSQL, countSQL)
	}
}

// The customer directory paginates through the same window, with the same
// search predicate in its count.
func TestCustomerListSQLPaginates(t *testing.T) {
	q, args := customerListSQL("Mom", 3)
	if !strings.Contains(q, " LIMIT ") || !strings.Contains(q, " OFFSET ") {
		t.Fatalf("customer list SQL is not paginated:\n%s", q)
	}
	if !strings.Contains(q, "ORDER BY full_name, id") {
		t.Errorf("customer paging needs a stable tiebreak:\n%s", q)
	}
	if got := args[0]; got != "%mom%" {
		t.Errorf("search arg = %v, want lowercased pattern", got)
	}
	if got := args[len(args)-1]; got != 2*PageSize {
		t.Errorf("offset arg = %v, want %d", got, 2*PageSize)
	}

	empty, args := customerListSQL("", 1)
	if strings.Contains(empty, "LIKE") {
		t.Errorf("an empty query must not search:\n%s", empty)
	}
	if len(args) != 2 {
		t.Errorf("empty-query args = %v, want just the window", args)
	}

	countSQL, countArgs := customerCountSQL(" Mom ")
	if !strings.HasPrefix(countSQL, "SELECT count(*)") || strings.Contains(countSQL, "LIMIT") {
		t.Errorf("customer count SQL shape wrong:\n%s", countSQL)
	}
	if len(countArgs) != 1 || countArgs[0] != "%mom%" {
		t.Errorf("customer count args = %v", countArgs)
	}
}
