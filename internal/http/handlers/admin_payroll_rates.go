package handlers

import (
	"errors"
	"fmt"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// Rate management is the admin form over the append-only staff_comp timeline:
// a change is a new row with a later effective_from, never an edit, so a past
// period always resolves to the rate that was in force then. This page is the
// only writer of that table (the mark-paid POST is Task 5) and it is admin-only
// like the period report it belongs to.
//
// The repos are concrete pgx types, so the handler tests swap these seams the
// same way admin_tv.go does instead of opening a database.
var (
	payrollStaffList = func(s *Server, r *http.Request) ([]domain.Staff, error) {
		if s.payrollRepo == nil {
			return nil, errors.New("payroll repo not configured")
		}
		return s.payrollRepo.ListStaff(r.Context())
	}
	payrollRateHistory = func(s *Server, r *http.Request) ([]domain.StaffComp, error) {
		if s.staffComps == nil {
			return nil, errors.New("staff rate repo not configured")
		}
		return s.staffComps.ListAll(r.Context())
	}
	payrollRateCreate = func(s *Server, r *http.Request, c *domain.StaffComp) error {
		if s.staffComps == nil {
			return errors.New("staff rate repo not configured")
		}
		return s.staffComps.Create(r.Context(), c)
	}
	// payrollUserName resolves one creator id to a display name. A missing or
	// unreadable user falls back to the id prefix in the view, so the audit
	// trail never disappears.
	payrollUserName = func(s *Server, r *http.Request, id uuid.UUID) string {
		if s.auth == nil {
			return ""
		}
		u, err := s.auth.GetUser(r.Context(), id)
		if err != nil || u == nil {
			return ""
		}
		return u.FullName
	}
)

// rateForm is the submitted form kept verbatim, so a validation error can
// re-render the page without losing what the operator typed. Every field is a
// string: parsing is the page's job (see buildStaffRate).
type rateForm struct {
	StaffID   string
	Percent   string
	Fixed     string
	Cadence   string
	Base      string
	Effective string
}

// rateDefaults is the blank form: today's date, the monthly cadence and the net
// commission base, which is the house default (see the migration's defaults).
func rateDefaults(today time.Time) rateForm {
	return rateForm{
		Cadence:   domain.CompCadenceMonthly,
		Base:      domain.CompBaseNet,
		Effective: today.In(payrollWIB).Format(payrollDayLayout),
	}
}

// payrollRateRow is one history row, pre-formatted for the template: percent
// text, a display date and the creator's name. The template never formats
// money or percent itself.
type payrollRateRow struct {
	ID            uuid.UUID
	EffectiveFrom time.Time
	PercentText   string
	FixedIDR      int64
	Cadence       string
	Base          string
	CreatedAt     time.Time
	Creator       string
}

// payrollRateStaff is one therapist's section: the rate in force today (nil
// when no row has started yet) and the whole timeline, newest first.
type payrollRateStaff struct {
	ID      uuid.UUID
	Name    string
	Active  bool
	Current *payrollRateRow
	History []payrollRateRow
}

// ratePercent renders basis points without losing a hundredth: 1250 -> "12.5%",
// 1255 -> "12.55%", 2500 -> "25%". A trailing zero is dropped, so the common
// whole percent reads naturally; percentBp's fixed one-decimal form is for the
// period report's rates, where the source values are whole percents.
func ratePercent(bp int) string {
	if bp < 0 {
		bp = 0
	}
	whole, frac := bp/100, bp%100
	switch {
	case frac == 0:
		return fmt.Sprintf("%d%%", whole)
	case frac%10 == 0:
		return fmt.Sprintf("%d.%d%%", whole, frac/10)
	default:
		return fmt.Sprintf("%d.%02d%%", whole, frac)
	}
}

// payrollDateKey is the calendar date t names on its own clock, as YYYY-MM-DD.
// Effective dates are DATE columns scanned back as UTC midnight while "today"
// is WIB midnight, so comparing instants would be off by the zone offset; the
// strings compare correctly for either.
func payrollDateKey(t time.Time) string {
	y, m, d := t.Date()
	return fmt.Sprintf("%04d-%02d-%02d", y, m, d)
}

// payrollRateStaffViews joins the staff list with the rate timeline: every
// therapist gets a section (a missing rate is a normal state the page flags),
// history is newest first, and the current rate is the newest row whose
// effective date is not after today. names maps a creator id to a name; an
// unresolved creator falls back to the id prefix.
func payrollRateStaffViews(staff []domain.Staff, rates []domain.StaffComp, today time.Time, names map[uuid.UUID]string) []payrollRateStaff {
	byStaff := make(map[uuid.UUID][]domain.StaffComp, len(staff))
	for _, c := range rates {
		byStaff[c.StaffID] = append(byStaff[c.StaffID], c)
	}
	todayKey := payrollDateKey(today)

	out := make([]payrollRateStaff, 0, len(staff))
	for _, st := range staff {
		view := payrollRateStaff{ID: st.ID, Name: st.Name, Active: st.Active}
		rows := byStaff[st.ID]
		sort.SliceStable(rows, func(i, j int) bool {
			if !rows[i].EffectiveFrom.Equal(rows[j].EffectiveFrom) {
				return rows[i].EffectiveFrom.After(rows[j].EffectiveFrom)
			}
			return rows[i].CreatedAt.After(rows[j].CreatedAt)
		})
		for _, c := range rows {
			row := payrollRateRow{
				ID:            c.ID,
				EffectiveFrom: c.EffectiveFrom,
				PercentText:   ratePercent(c.PercentBp),
				FixedIDR:      c.FixedIDR,
				Cadence:       c.Cadence,
				Base:          c.CommissionBase,
				CreatedAt:     c.CreatedAt,
			}
			if c.CreatedBy != nil {
				if name := names[*c.CreatedBy]; name != "" {
					row.Creator = name
				} else {
					row.Creator = c.CreatedBy.String()[:8]
				}
			}
			view.History = append(view.History, row)
			if view.Current == nil && payrollDateKey(c.EffectiveFrom) <= todayKey {
				current := row
				view.Current = &current
			}
		}
		out = append(out, view)
	}
	return out
}

// parsePercentBp parses the percent field into basis points of a percent. Both
// locales are accepted ("12,5" and "12.5"), at most two decimals are allowed,
// and the range is 0–100 inclusive. It is integer math throughout: no float
// ever touches a rate.
func parsePercentBp(raw string) (int, bool) {
	s := strings.TrimSpace(strings.ReplaceAll(raw, ",", "."))
	if s == "" {
		return 0, false
	}
	whole, frac, hasDot := strings.Cut(s, ".")
	if !hasDot {
		frac = ""
	} else if len(frac) < 1 || len(frac) > 2 {
		return 0, false
	}
	if whole == "" && !hasDot {
		return 0, false
	}
	if whole != "" && !digitsOnly(whole) {
		return 0, false
	}
	if hasDot && !digitsOnly(frac) {
		return 0, false
	}
	for len(frac) < 2 {
		frac += "0"
	}
	w := 0
	if whole != "" {
		var err error
		w, err = strconv.Atoi(whole)
		if err != nil {
			return 0, false
		}
	}
	f := 0
	if frac != "" {
		var err error
		f, err = strconv.Atoi(frac)
		if err != nil {
			return 0, false
		}
	}
	if w > 100 {
		// Nothing past 100% can validate, and the bound keeps the multiply
		// below from overflowing on an absurd digit run.
		return 0, false
	}
	bp := w*100 + f
	if bp < 0 || bp > 10000 {
		return 0, false
	}
	return bp, true
}

// parseFixedIDR parses the fixed component: whole non-negative rupiah, empty is
// zero. A thousands separator is a typo, not a number, so it is refused rather
// than silently stripped.
func parseFixedIDR(raw string) (int64, bool) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return 0, true
	}
	if !digitsOnly(s) {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil || n < 0 {
		return 0, false
	}
	return n, true
}

// digitsOnly reports whether s is non-empty and made only of ASCII digits.
func digitsOnly(s string) bool {
	if s == "" {
		return false
	}
	for i := 0; i < len(s); i++ {
		if s[i] < '0' || s[i] > '9' {
			return false
		}
	}
	return true
}

// buildStaffRate is the form contract in one place. It returns the row to
// insert, or an i18n key for the inline error. The duplicate-date check is
// explicit: the created_at tie-break would otherwise let two rows share a date
// and make which one pays undefined.
func buildStaffRate(f rateForm, staff []domain.Staff, existing []domain.StaffComp) (*domain.StaffComp, string) {
	staffID, err := uuid.Parse(strings.TrimSpace(f.StaffID))
	if err != nil {
		return nil, "pr.staff.bad"
	}
	known := false
	for _, st := range staff {
		if st.ID == staffID {
			known = true
			break
		}
	}
	if !known {
		return nil, "pr.staff.bad"
	}

	bp, ok := parsePercentBp(f.Percent)
	if !ok {
		return nil, "pr.pct.bad"
	}
	fixed, ok := parseFixedIDR(f.Fixed)
	if !ok {
		return nil, "pr.fixed.bad"
	}
	cadence := strings.TrimSpace(f.Cadence)
	if !domain.ValidCadence(cadence) {
		return nil, "pr.cad.bad"
	}
	base := strings.TrimSpace(f.Base)
	if !domain.ValidCommissionBase(base) {
		return nil, "pr.base.bad"
	}
	effective, err := time.ParseInLocation(payrollDayLayout, strings.TrimSpace(f.Effective), payrollWIB)
	if err != nil {
		return nil, "pr.eff.bad"
	}

	key := payrollDateKey(effective)
	for _, c := range existing {
		if c.StaffID == staffID && payrollDateKey(c.EffectiveFrom) == key {
			return nil, "pr.dup"
		}
	}
	return &domain.StaffComp{
		StaffID:        staffID,
		PercentBp:      bp,
		FixedIDR:       fixed,
		Cadence:        cadence,
		CommissionBase: base,
		EffectiveFrom:  effective,
	}, ""
}

// payrollRatesData loads and shapes the page. A read error is an outage, not an
// empty page, so it is returned to the caller.
func (s *Server) payrollRatesData(r *http.Request, today time.Time) ([]payrollRateStaff, error) {
	staff, err := payrollStaffList(s, r)
	if err != nil {
		return nil, err
	}
	rates, err := payrollRateHistory(s, r)
	if err != nil {
		return nil, err
	}
	names := make(map[uuid.UUID]string, 4)
	for _, c := range rates {
		if c.CreatedBy == nil {
			continue
		}
		if _, ok := names[*c.CreatedBy]; ok {
			continue
		}
		names[*c.CreatedBy] = payrollUserName(s, r, *c.CreatedBy)
	}
	return payrollRateStaffViews(staff, rates, today, names), nil
}

// renderPayrollRates renders the page, optionally with a form to re-show (a
// validation error) and a success/error i18n key.
func (s *Server) renderPayrollRates(w http.ResponseWriter, r *http.Request, form rateForm, errKey, okKey string) {
	today := payrollToday()
	staff, err := s.payrollRatesData(r, today)
	if err != nil {
		s.log.Error("payroll rates", "err", err)
		http.Error(w, "Could not load the rate history", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_payroll_rates.html", pageData{
		"Staff":   staff,
		"Form":    form,
		"Today":   today.Format(payrollDayLayout),
		"Error":   errKey,
		"Success": okKey,
	})
}

// AdminPayrollRates is the rate management page: the create form plus every
// therapist's current rate and full timeline. RequireAdmin owns the staff gate.
func (s *Server) AdminPayrollRates(w http.ResponseWriter, r *http.Request) {
	okKey := ""
	if r.URL.Query().Get("ok") != "" {
		okKey = "pr.rateok"
	}
	s.renderPayrollRates(w, r, rateDefaults(payrollToday()), "", okKey)
}

// AdminPayrollRateCreate appends one rate row. A validation failure re-renders
// the page inline with the submitted values; a write failure takes staffFail, so
// an HTMX submit can never look like a dead button. Nothing is ever edited: a
// correction is a new row, which keeps every past period's numbers.
func (s *Server) AdminPayrollRateCreate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
		return
	}

	form := rateForm{
		StaffID:   r.FormValue("staff_id"),
		Percent:   r.FormValue("percent"),
		Fixed:     r.FormValue("fixed"),
		Cadence:   r.FormValue("cadence"),
		Base:      r.FormValue("base"),
		Effective: r.FormValue("effective"),
	}

	staff, err := payrollStaffList(s, r)
	if err != nil {
		s.log.Error("payroll rates staff", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failPayRates)
		return
	}
	existing, err := payrollRateHistory(s, r)
	if err != nil {
		s.log.Error("payroll rates history", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failPayRates)
		return
	}

	comp, key := buildStaffRate(form, staff, existing)
	if key != "" {
		// Re-render the page with the submitted values: a validation error is
		// the operator's to fix, not a session or server fault.
		s.renderPayrollRates(w, r, form, key, "")
		return
	}
	comp.CreatedBy = &u.ID
	if err := payrollRateCreate(s, r, comp); err != nil {
		// The pre-flight read above catches the normal duplicate; the unique
		// index catches the race. Both answer the same inline message, so the
		// operator sees one behaviour whichever path finds it.
		if isDuplicateRate(err) {
			s.renderPayrollRates(w, r, form, "pr.dup", "")
			return
		}
		s.log.Error("payroll rate create", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failPayRates)
		return
	}
	s.redirectHTMX(w, r, "/admin/payroll/rates?ok=1")
}

const (
	failPayRates = "Could not save the rate. Try again."
)

// isDuplicateRate reports whether err is the database's unique
// (staff_id, effective_from) violation — another submit inserted the same date
// between this form's pre-flight read and its insert. Only that index counts:
// an FK violation is 23503 and belongs to the generic failure path.
func isDuplicateRate(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "staff_comp_staff_effective_idx"
}
