package handlers

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/i18n"
	"spa-booking/internal/service"

	"github.com/google/uuid"
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

// payoutReader resolves the paid snapshot for one therapist's period
// (repo.StaffPayoutRepo). (nil, nil) means the period is still unpaid.
type payoutReader interface {
	ForPeriod(ctx context.Context, staffID uuid.UUID, kind string, periodStart time.Time) (*domain.StaffPayout, error)
}

const payrollDayLayout = "2006-01-02"

// payrollWIB is the reporting clock every period boundary is stated in.
var payrollWIB = time.FixedZone("WIB", 7*3600)

// payrollRowView is one computed row plus the paid snapshot the page badges.
// R19: this task renders the state only; the mark-paid POST arrives with
// Task 5, so there is no dead endpoint behind a button here.
type payrollRowView struct {
	service.PayrollRow
	Paid *domain.StaffPayout
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
			paid, err := s.payouts.ForPeriod(ctx, row.StaffID, report.Period.Kind, report.Period.Start)
			if err != nil {
				return payrollPage{}, err
			}
			view.Paid = paid
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

// AdminPayroll renders the period page. RequireAdmin owns the staff gate.
func (s *Server) AdminPayroll(w http.ResponseWriter, r *http.Request) {
	kind, start := payrollParams(r)
	page, err := s.loadPayroll(r.Context(), kind, start)
	if err != nil {
		s.log.Error("admin payroll", "err", err)
		http.Error(w, "Could not load payroll", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_payroll.html", payrollPageData(payrollLang(r), page))
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
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM for Excel

	_ = cw.Write([]string{
		t("pr.th.staff"), t("pr.th.sess"), t("pr.th.gross"), t("pr.th.refund"),
		t("pr.th.rate"), t("pr.th.comm"), t("pr.th.fixed"), t("pr.th.total"),
		t("pr.th.paid"),
	})
	for _, row := range page.Rows {
		_ = cw.Write([]string{
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
	_ = cw.Write([]string{
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
	_ = cw.Write(labelRow)
	cw.Flush()
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
