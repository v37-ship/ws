package handlers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/service"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgconn"
)

// The payroll page is the admin-only accrual report: what each therapist earned
// on their completed visits in one WIB week or month. It is deliberately not
// the portal's revenue tab — that one is cash-basis (paid_at), this one is
// visit-basis and payment-blind, and the copy on the page says so.
//
// The reads are interfaces so the page can be exercised through the real router
// without a database: the money math is Task 2's unit-tested service and this
// file only composes the page and its CSV.

// payrollReporter computes one normalised period (service.PayrollService).
type payrollReporter interface {
	Report(ctx context.Context, kind string, start time.Time) (service.PayrollReport, error)
}

// payoutStore resolves the paid snapshot for one therapist's period and writes
// the one that marks it paid (repo.StaffPayoutRepo). ForPeriod returns
// (nil, nil) while the period is still unpaid.
type payoutStore interface {
	ForPeriod(ctx context.Context, staffID uuid.UUID, kind string, periodStart time.Time) (*domain.StaffPayout, error)
	Create(ctx context.Context, p *domain.StaffPayout) error
}

const payrollDayLayout = "2006-01-02"

// payrollWIB is the reporting clock every period boundary is stated in.
var payrollWIB = time.FixedZone("WIB", 7*3600)

// payrollRowView is one computed row plus the paid snapshot the page badges.
type payrollRowView struct {
	service.PayrollRow
	Paid *domain.StaffPayout
	// SnapshotDiff marks a closed period whose frozen total no longer matches
	// the recomputed row (a late refund, or a rate row added afterwards). The
	// page keeps showing the snapshot and says the difference is corrected in
	// a later period; it never silently rewrites either figure.
	SnapshotDiff bool
}

// payrollPage is everything one period render needs.
type payrollPage struct {
	Period   service.PayrollPeriod
	Report   service.PayrollReport
	Rows     []payrollRowView
	Missing  []string
	Sessions int
}

// payrollToday is the current WIB calendar day at midnight.
func payrollToday() time.Time {
	now := time.Now().In(payrollWIB)
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, payrollWIB)
}

// payrollParams whitelists the page's query into a period. Anything but week is
// the month view, and a missing or malformed start is today in WIB, so a stale
// bookmark never renders an error page.
func payrollParams(r *http.Request) (kind string, start time.Time) {
	kind = r.URL.Query().Get("kind")
	if kind != service.PayrollKindWeek {
		kind = service.PayrollKindMonth
	}
	start = payrollToday()
	if v := r.URL.Query().Get("start"); v != "" {
		if t, err := time.ParseInLocation(payrollDayLayout, v, payrollWIB); err == nil {
			start = t
		}
	}
	return kind, start
}

// payrollStep returns the neighbouring period starts for the nav links. It
// normalises first, so a Wednesday start steps from that week's Monday and a
// mid-month start from day 1. kind is already whitelisted by payrollParams; an
// unknown kind simply keeps the same day.
func payrollStep(kind string, start time.Time) (prev, next time.Time) {
	period, err := service.PayrollPeriodFor(kind, start)
	if err != nil {
		t := start.In(payrollWIB)
		return t.AddDate(0, 0, -7), t.AddDate(0, 0, 7)
	}
	if period.Kind == service.PayrollKindWeek {
		return period.Start.AddDate(0, 0, -7), period.Start.AddDate(0, 0, 7)
	}
	return period.Start.AddDate(0, -1, 0), period.Start.AddDate(0, 1, 0)
}

// payrollURL is one page link. The start is always a concrete date so the link
// survives the period rounding (a Monday for a week, day 1 for a month).
func payrollURL(kind string, start time.Time) string {
	return "/admin/payroll?kind=" + kind + "&start=" + start.In(payrollWIB).Format(payrollDayLayout)
}

// payrollLang is the render language, including a ?lang= override that render()
// will persist after the label is built.
func payrollLang(r *http.Request) string {
	if lang, ok := i18n.QueryOverride(r); ok {
		return lang
	}
	return i18n.Resolve(r)
}

// payrollPeriodLabel is the human period title: "23 Nov – 29 Nov 2026" for a
// week, "Nov 2026" for a month, in the page's language.
func payrollPeriodLabel(lang string, period service.PayrollPeriod) string {
	start := period.Start.In(payrollWIB)
	end := period.End.In(payrollWIB)
	if period.Kind == service.PayrollKindWeek {
		return fmt.Sprintf("%d %s – %d %s %d",
			start.Day(), i18n.MonthShort(lang, start.Month()),
			end.Day(), i18n.MonthShort(lang, end.Month()), end.Year())
	}
	return fmt.Sprintf("%s %d", i18n.MonthShort(lang, start.Month()), start.Year())
}

// payrollPayoutKind maps the page's view kind (week/month) to the payout and
// rate vocabulary (weekly/monthly). They are deliberately different: a weekly
// fixed can appear on the month view, so the view kind is not a cadence.
func payrollPayoutKind(kind string) string {
	if kind == service.PayrollKindWeek {
		return domain.PeriodWeekly
	}
	return domain.PeriodMonthly
}

// loadPayroll reads the period report and each row's paid state. ForPeriod is
// called per row (there is no bulk read that matches the page's period), and a
// page-level read error is an outage, not an empty payroll.
func (s *Server) loadPayroll(ctx context.Context, kind string, start time.Time) (payrollPage, error) {
	if s.payroll == nil {
		return payrollPage{}, errors.New("payroll service not configured")
	}
	report, err := s.payroll.Report(ctx, kind, start)
	if err != nil {
		return payrollPage{}, err
	}
	page := payrollPage{
		Period: report.Period,
		Report: report,
		Rows:   make([]payrollRowView, 0, len(report.Rows)),
	}
	for _, row := range report.Rows {
		view := payrollRowView{PayrollRow: row}
		if s.payouts != nil {
			paid, err := s.payouts.ForPeriod(ctx, row.StaffID, payrollPayoutKind(report.Period.Kind), report.Period.Start)
			if err != nil {
				return payrollPage{}, err
			}
			view.Paid = paid
			view.SnapshotDiff = paid != nil && paid.TotalIDR != row.TotalIDR
		}
		if row.RateMissing {
			page.Missing = append(page.Missing, row.StaffName)
		}
		page.Sessions += row.Sessions
		page.Rows = append(page.Rows, view)
	}
	// The template reads the totals from Report and the rows themselves; drop
	// the duplicate slice so the page payload carries each row once.
	page.Report.Rows = nil
	return page, nil
}

// payrollPageData is the template payload. Report carries the totals and the
// missing-rate count; Rows is the same list enriched with the paid snapshot.
func payrollPageData(lang string, page payrollPage) pageData {
	prev, next := payrollStep(page.Period.Kind, page.Period.Start)
	start := page.Period.Start.In(payrollWIB).Format(payrollDayLayout)
	return pageData{
		"Kind":        page.Period.Kind,
		"Start":       start,
		"PeriodLabel": payrollPeriodLabel(lang, page.Period),
		"PrevURL":     payrollURL(page.Period.Kind, prev),
		"NextURL":     payrollURL(page.Period.Kind, next),
		"TodayURL":    payrollURL(page.Period.Kind, payrollToday()),
		"Report":      page.Report,
		"Rows":        page.Rows,
		"Missing":     page.Missing,
		"Sessions":    page.Sessions,
		"CSVURL":      "/admin/payroll.csv?kind=" + page.Period.Kind + "&start=" + start,
	}
}

// renderPayroll renders one period with an optional inline error or success
// banner. Reads fail loudly: an outage is not an empty payroll.
func (s *Server) renderPayroll(w http.ResponseWriter, r *http.Request, kind string, start time.Time, errKey, okKey string) {
	page, err := s.loadPayroll(r.Context(), kind, start)
	if err != nil {
		s.log.Error("admin payroll", "err", err)
		http.Error(w, "Could not load payroll", http.StatusInternalServerError)
		return
	}
	data := payrollPageData(payrollLang(r), page)
	data["Error"] = errKey
	data["Success"] = okKey
	s.render(w, r, "admin_payroll.html", data)
}

// AdminPayroll renders the period page. RequireAdmin owns the staff gate.
func (s *Server) AdminPayroll(w http.ResponseWriter, r *http.Request) {
	kind, start := payrollParams(r)
	okKey := ""
	if r.URL.Query().Get("ok") != "" {
		okKey = "pr.payok"
	}
	s.renderPayroll(w, r, kind, start, "", okKey)
}

// AdminPayrollCSV exports the same period: localized header, one row per
// therapist, the totals, then the period label. The BOM matches the reports
// export so Excel opens both the same way.
func (s *Server) AdminPayrollCSV(w http.ResponseWriter, r *http.Request) {
	kind, start := payrollParams(r)
	page, err := s.loadPayroll(r.Context(), kind, start)
	if err != nil {
		s.log.Error("admin payroll csv", "err", err)
		http.Error(w, "Could not load payroll", http.StatusInternalServerError)
		return
	}
	lang := payrollLang(r)
	t := func(key string) string { return i18n.T(lang, key) }

	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition",
		fmt.Sprintf(`attachment; filename="payroll-%s.csv"`, page.Period.Start.In(payrollWIB).Format(payrollDayLayout)))
	cw := csv.NewWriter(w)
	// A truncated export must leave a trace: the client can vanish mid-stream,
	// and discarding the error would make a half-written payroll file look fine.
	write := func(rec []string) {
		if err := cw.Write(rec); err != nil {
			s.log.Error("admin payroll csv write", "err", err)
		}
	}
	if _, err := w.Write([]byte("\xef\xbb\xbf")); err != nil { // BOM for Excel
		s.log.Error("admin payroll csv bom", "err", err)
	}

	write([]string{
		t("pr.th.staff"), t("pr.th.sess"), t("pr.th.gross"), t("pr.th.refund"),
		t("pr.th.rate"), t("pr.th.comm"), t("pr.th.fixed"), t("pr.th.total"),
		t("pr.th.paid"),
	})
	for _, row := range page.Rows {
		write([]string{
			row.StaffName,
			strconv.Itoa(row.Sessions),
			strconv.FormatInt(row.GrossIDR, 10),
			strconv.FormatInt(row.RefundIDR, 10),
			payrollRateCSV(lang, row.PayrollRow),
			strconv.FormatInt(row.CommissionIDR, 10),
			strconv.FormatInt(row.FixedIDR, 10),
			strconv.FormatInt(row.TotalIDR, 10),
			payrollPaidLabel(lang, row.Paid),
		})
	}
	write([]string{
		t("pr.total"),
		strconv.Itoa(page.Sessions),
		strconv.FormatInt(page.Report.TotalGrossIDR, 10),
		strconv.FormatInt(page.Report.TotalRefundIDR, 10),
		"",
		strconv.FormatInt(page.Report.TotalCommissionIDR, 10),
		strconv.FormatInt(page.Report.TotalFixedIDR, 10),
		strconv.FormatInt(page.Report.TotalIDR, 10),
		"",
	})
	// The period label closes the export as a padded row so the file stays a
	// rectangular table (a short row would make strict CSV readers refuse it).
	labelRow := make([]string, 9)
	labelRow[0] = t("pr.period")
	labelRow[1] = payrollPeriodLabel(lang, page.Period)
	write(labelRow)
	cw.Flush()
	if err := cw.Error(); err != nil {
		s.log.Error("admin payroll csv flush", "err", err)
	}
}

// payrollRateCSV is the rate cell's text: "30.0% (net)", or the missing-rate
// label. A missing base is never guessed.
func payrollRateCSV(lang string, row service.PayrollRow) string {
	if row.RateMissing {
		return i18n.T(lang, "pr.norate")
	}
	base := ""
	if row.Base != "" {
		base = " (" + i18n.T(lang, "pr.base."+row.Base) + ")"
	}
	return percentBp(row.PercentBp) + base
}

// payrollPaidLabel is the paid cell's text; the note rides along in the export
// because the CSV is what the bookkeeper files.
func payrollPaidLabel(lang string, paid *domain.StaffPayout) string {
	if paid == nil {
		return i18n.T(lang, "pr.unpaid")
	}
	label := i18n.T(lang, "pr.paid")
	if paid.Note != "" {
		label += ": " + paid.Note
	}
	return label
}

// payrollNoteMaxRunes bounds the free-text note on a paid snapshot. It is a
// soft guard on a single-line note, not a document store.
const payrollNoteMaxRunes = 500

// payrollFormPeriod parses a mark-paid POST's period. Unlike the GET's
// payrollParams, a write never guesses: an unknown kind or a malformed start is
// refused, and the caller re-renders the default page with the inline error.
func payrollFormPeriod(kindRaw, startRaw string) (string, time.Time, bool) {
	kind := strings.TrimSpace(kindRaw)
	if kind != service.PayrollKindWeek && kind != service.PayrollKindMonth {
		return "", time.Time{}, false
	}
	start, err := time.ParseInLocation(payrollDayLayout, strings.TrimSpace(startRaw), payrollWIB)
	if err != nil {
		return "", time.Time{}, false
	}
	period, err := service.PayrollPeriodFor(kind, start)
	if err != nil {
		return "", time.Time{}, false
	}
	return period.Kind, period.Start, true
}

// payrollPayFail is an inline refusal: the operator can fix it on the page, so
// it is not a session or server fault. Plain forms get the page at 200 with the
// translated message; HTMX gets a toast and keeps the last good DOM, because
// the app's HTMX config would silently swallow a bare 4xx.
func (s *Server) payrollPayFail(w http.ResponseWriter, r *http.Request, kind string, start time.Time, errKey string) {
	if isHTMX(r) {
		s.toast(w, "error", i18n.T(payrollLang(r), errKey))
		htmx.Reswap(w, "none")
		w.WriteHeader(http.StatusOK)
		return
	}
	s.renderPayroll(w, r, kind, start, errKey, "")
}

// AdminPayrollPay marks one therapist's period paid. The period is recomputed
// here and the row found by staff id — a posted amount is never trusted — and
// the resulting numbers are frozen as an immutable snapshot. The database's
// unique key owns "once": a second submit answers "already paid" inline, never
// a 500. A rate-less row is refused because its payout would be meaningless.
func (s *Server) AdminPayrollPay(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil || u.Role != domain.RoleAdmin {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
		return
	}
	if s.payroll == nil || s.payouts == nil {
		s.log.Error("admin payroll pay: payroll not configured")
		s.staffFail(w, r, http.StatusInternalServerError, failPayPay)
		return
	}

	kind, start, ok := payrollFormPeriod(r.FormValue("kind"), r.FormValue("start"))
	if !ok {
		// A bad period cannot name a page to return to; fall back to the
		// page's own default so the operator still sees a form.
		fbKind, fbStart := payrollParams(r)
		s.payrollPayFail(w, r, fbKind, fbStart, "pr.per.bad")
		return
	}
	refuse := func(key string) { s.payrollPayFail(w, r, kind, start, key) }

	staffID, err := uuid.Parse(strings.TrimSpace(r.FormValue("staff")))
	if err != nil {
		refuse("pr.staff.bad")
		return
	}
	note := strings.TrimSpace(r.FormValue("note"))
	if utf8.RuneCountInString(note) > payrollNoteMaxRunes {
		refuse("pr.paynote")
		return
	}

	report, err := s.payroll.Report(r.Context(), kind, start)
	if err != nil {
		s.log.Error("admin payroll pay report", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failPayPay)
		return
	}
	var row *service.PayrollRow
	for i := range report.Rows {
		if report.Rows[i].StaffID == staffID {
			row = &report.Rows[i]
			break
		}
	}
	if row == nil {
		refuse("pr.nostaff")
		return
	}
	if row.RateMissing {
		// R25: no rate means no commission, so a snapshot would record a
		// number nobody agreed to. The rate page is the fix.
		refuse("pr.payrate")
		return
	}

	payout := &domain.StaffPayout{
		StaffID:       row.StaffID,
		PeriodKind:    payrollPayoutKind(report.Period.Kind),
		PeriodStart:   report.Period.Start,
		PeriodEnd:     report.Period.End,
		GrossIDR:      row.GrossIDR,
		RefundIDR:     row.RefundIDR,
		NetIDR:        row.NetIDR,
		PercentBp:     row.PercentBp,
		FixedIDR:      row.FixedIDR,
		CommissionIDR: row.CommissionIDR,
		TotalIDR:      row.TotalIDR,
		Note:          note,
		PaidBy:        &u.ID,
		PaidAt:        time.Now(),
	}
	if err := s.payouts.Create(r.Context(), payout); err != nil {
		if isDuplicatePayout(err) {
			refuse("pr.already")
			return
		}
		s.log.Error("admin payroll pay", "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failPayPay)
		return
	}
	s.redirectHTMX(w, r, payrollURL(report.Period.Kind, report.Period.Start)+"&ok=1")
}

const failPayPay = "Could not mark the period paid. Try again."

// isDuplicatePayout reports whether err is the unique period key losing a race
// (a double click or two tabs). Only that constraint is "already paid"; an FK
// or another table's unique key is a real fault.
func isDuplicatePayout(err error) bool {
	var pgErr *pgconn.PgError
	if !errors.As(err, &pgErr) {
		return false
	}
	return pgErr.Code == "23505" && pgErr.ConstraintName == "staff_payouts_period_key"
}
