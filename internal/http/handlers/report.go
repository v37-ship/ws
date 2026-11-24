package handlers

import (
	"encoding/csv"
	"fmt"
	"html/template"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

var yearRe = regexp.MustCompile(`^\d{4}$`)

var monRe = regexp.MustCompile(`^(0[1-9]|1[0-2])$`)

var dateRe = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}$`)

// wib is the reporting clock. Asia/Jakarta has no DST, so the fixed-offset
// fallback is exact even on a host without a tz database.
func wib() *time.Location {
	if loc, err := time.LoadLocation("Asia/Jakarta"); err == nil {
		return loc
	}
	return time.FixedZone("WIB", 7*3600)
}

// currentMonth returns the current YYYY-MM in Asia/Jakarta.
func currentMonth() string {
	return time.Now().In(wib()).Format("2006-01")
}

// currentDate returns the current YYYY-MM-DD in Asia/Jakarta.
func currentDate() string {
	return time.Now().In(wib()).Format("2006-01-02")
}

// percentBp formats integer basis points as one decimal of a percent
// (3000 -> "30.0%"), for the demand block's rates. Display only: every rate is
// computed in integers, and the template shows "—" when the denominator is zero.
func percentBp(bp int) string {
	if bp < 0 {
		bp = 0
	}
	return fmt.Sprintf("%d.%d%%", bp/100, (bp%100)/10)
}

// deltaPct renders a signed basis-point change as one decimal of a percent:
// 5000 -> "+50.0%", -2000 -> "-20.0%". A value that rounds to zero prints
// "0.0%" with no sign, so a flat month never reads as a move. It returns
// template.HTML because html/template escapes a literal "+" in text context
// (&#43;) — the string is built only from an int and fixed punctuation, so
// marking it trusted cannot carry markup.
func deltaPct(bp int) template.HTML {
	sign := ""
	if bp > 0 {
		sign = "+"
	}
	if bp < 0 {
		sign = "-"
		bp = -bp
	}
	tenths := (bp + 5) / 10 // 10 bp = 0.1%, rounded half up
	if tenths == 0 {
		return "0.0%"
	}
	return template.HTML(fmt.Sprintf("%s%d.%d%%", sign, tenths/10, tenths%10))
}

// normalizeReportDate keeps ?date= on a real WIB calendar day. Anything
// malformed, non-canonical or in the future resolves to today in Asia/Jakarta:
// the daily view is an operating picture, so a mistyped or bookmarked future
// link lands on today rather than on an empty forecast panel. Past days stay
// selectable (that is the point of the date picker).
func normalizeReportDate(v string, now time.Time) string {
	today := now.In(wib()).Format("2006-01-02")
	v = strings.TrimSpace(v)
	if !dateRe.MatchString(v) {
		return today
	}
	d, err := time.ParseInLocation("2006-01-02", v, wib())
	if err != nil || d.Format("2006-01-02") != v || v > today {
		return today
	}
	return v
}

// reportParams parses ?year=YYYY&mon=MM (preferred), ?month=YYYY-MM
// (legacy links/CSV), and ?location_id= with safe defaults.
func (s *Server) reportParams(r *http.Request) (string, uuid.UUID) {
	q := r.URL.Query()
	year, mon := q.Get("year"), q.Get("mon")
	month := ""
	if yearRe.MatchString(year) && monRe.MatchString(mon) {
		month = year + "-" + mon
	} else {
		month = q.Get("month")
	}
	if !monthRe.MatchString(month) {
		month = currentMonth()
	}
	locID, _ := uuid.Parse(q.Get("location_id"))
	return month, locID
}

// reportTabs is the reports page's tab set in reading order. The ID is the
// ?tab= value; the Key is the i18n suffix (r.tab.<key>) and stays short enough
// for the dictionary's 12-char key budget.
var reportTabs = []struct{ ID, Key string }{
	{"daily", "daily"},
	{"revenue", "rev"},
	{"ops", "ops"},
	{"staff", "staff"},
}

// reportTabLink is one tab in the shell's nav.
type reportTabLink struct {
	ID     string
	Key    string
	URL    string
	Active bool
}

// normalizeReportTab keeps ?tab= on the whitelist. Anything else (a stale
// bookmark, a typo, an empty param) falls back to "revenue" — the ledger the
// page was before the tabs existed — so a bad link never renders a blank shell.
func normalizeReportTab(v string) string {
	for _, tb := range reportTabs {
		if tb.ID == v {
			return v
		}
	}
	return "revenue"
}

// reportsInput is everything PortalReports hands to reportsData.
type reportsInput struct {
	Month     string
	LocID     uuid.UUID
	Summary   domain.ReportSummary
	Lines     []domain.ReportLine
	Refunds   []domain.ReportRefund
	Tab       string
	Page      int
	Date      string              // daily tab's WIB day (YYYY-MM-DD)
	Daily     domain.DailySummary // only fetched when Tab == "daily"
	Demand    domain.ReportDemand // only fetched when Tab == "ops"
	Trend     domain.MonthTrend   // MTD delta + six-month net series
	Years     []string
	Locations []domain.Location
	Query     url.Values
}

// reportsData is the whole template payload for /portal/reports. Revenue is
// copied straight out of the service's MonthlyReport result — nothing here
// recomputes money, which is what keeps the tabbed page's figures identical to
// the single-page ledger it replaced. Only the transactions table is windowed
// (repo.PageSize rows/page); the summary, the staff rows and the service rows
// always describe the whole month.
func reportsData(in reportsInput) pageData {
	month := in.Month
	if !monthRe.MatchString(month) {
		// The handler validates ?month= before this point; the guard keeps a
		// future caller (or a fixture) from slicing a short string and
		// panicking the whole page.
		month = currentMonth()
	}
	// Tab is normalized here as well as in the handler so the pure builder can
	// never render a panel the whitelist does not know.
	tab := normalizeReportTab(in.Tab)
	total := len(in.Lines)
	pages := totalPages(total)
	page := in.Page
	if page < 1 {
		page = 1
	}
	if page > pages {
		page = pages
	}
	start := (page - 1) * repo.PageSize
	if start > total {
		start = total
	}
	end := start + repo.PageSize
	if end > total {
		end = total
	}
	window := in.Lines[start:end]

	// links rebuild /portal/reports addresses that keep the month (canonical
	// ?month=), the tab and the location. Legacy ?year=&mon= pairs, the daily
	// ?date= and an explicit ?page= are dropped so no link disagrees about the
	// period, and page 1 keeps a clean address.
	base := func() url.Values {
		v := url.Values{}
		for k, vals := range in.Query {
			switch k {
			case "page", "tab", "month", "year", "mon", "date":
				continue
			}
			if len(vals) == 1 && vals[0] == "" {
				continue
			}
			v[k] = vals
		}
		return v
	}
	tabURL := func(tab string, page int) string {
		v := base()
		v.Set("month", month)
		v.Set("tab", tab)
		return pageURL("/portal/reports", v, page)
	}
	monthURL := func(m string) string {
		v := base()
		v.Set("month", m)
		v.Set("tab", tab)
		return pageURL("/portal/reports", v, 1)
	}
	now := time.Now()
	date := normalizeReportDate(in.Date, now)
	dayURL := func(d string) string {
		v := base()
		v.Set("tab", "daily")
		v.Set("date", d)
		return pageURL("/portal/reports", v, 1)
	}
	day, err := time.ParseInLocation("2006-01-02", date, wib())
	if err != nil {
		day = now.In(wib())
	}
	todayDate := now.In(wib()).Format("2006-01-02")
	nextDay := day.AddDate(0, 0, 1)

	// The daily summary's own Date is normalized too: a future ?date= must not
	// leave the panel labelled with the day the operator typed.
	daily := in.Daily
	daily.Date = date

	tabs := make([]reportTabLink, 0, len(reportTabs))
	for _, tb := range reportTabs {
		tabs = append(tabs, reportTabLink{
			ID: tb.ID, Key: tb.Key, URL: tabURL(tb.ID, 1), Active: tb.ID == tab,
		})
	}

	today := currentMonth()
	prevMonth, nextMonth := service.PrevMonth(month), service.NextMonth(month)
	if prevMonth == "" {
		prevMonth = month
	}
	if nextMonth == "" {
		nextMonth = month
	}
	// The sparkline is built here, from the series the handler loaded, so the
	// template receives finished markup. The values are int64 nets and the
	// builder writes only numeric coordinates into a fixed attribute set, so
	// the template.HTML wrapper is safe by construction — no user text passes
	// through it.
	spark := template.HTML("")
	if len(in.Trend.Series) > 0 {
		nets := make([]int64, len(in.Trend.Series))
		for i, point := range in.Trend.Series {
			nets[i] = point.NetIDR
		}
		spark = template.HTML(service.SparklineSVG(nets, 560, 72))
	}
	data := pageData{
		"Month":        month,
		"Year":         month[:4],
		"Mon":          month[5:7],
		"Years":        in.Years,
		"Today":        today,
		"IsCurrent":    month == today,
		"ShowLoc":      in.LocID == uuid.Nil,
		"LocID":        in.LocID.String(),
		"Locations":    in.Locations,
		"Summary":      in.Summary,
		"Lines":        window,
		"LineTotal":    total,
		"Refunds":      in.Refunds,
		"Tab":          tab,
		"Tabs":         tabs,
		"Page":         page,
		"TotalPages":   pages,
		"Date":         date,
		"DayMax":       todayDate,
		"IsToday":      date == todayDate,
		"Daily":        daily,
		"Demand":       in.Demand,
		"Trend":        in.Trend,
		"Spark":        spark,
		"PrevURL":      "",
		"NextURL":      "",
		"PrevMonthURL": monthURL(prevMonth),
		"NextMonthURL": monthURL(nextMonth),
		"TodayURL":     monthURL(today),
		"DayPrevURL":   dayURL(day.AddDate(0, 0, -1).Format("2006-01-02")),
		"DayNextURL":   "",
		"DayTodayURL":  "",
	}
	// The day picker's calendar caps at today; the next arrow only exists while
	// there is a future WIB day still allowed (a refused future date would
	// normalize straight back to today).
	if nextDay.Format("2006-01-02") <= todayDate {
		data["DayNextURL"] = dayURL(nextDay.Format("2006-01-02"))
	}
	if date != todayDate {
		data["DayTodayURL"] = dayURL(todayDate)
	}
	if page > 1 {
		data["PrevURL"] = tabURL(tab, page-1)
	}
	if page < pages {
		data["NextURL"] = tabURL(tab, page+1)
	}
	return data
}

// PortalReports renders the monthly revenue report (all portal staff).
func (s *Server) PortalReports(w http.ResponseWriter, r *http.Request) {
	month, locID := s.reportParams(r)
	qs := r.URL.Query()
	tab := normalizeReportTab(qs.Get("tab"))
	// The staff tab buys one extra read (schedule + time-off + completed
	// visits) for its utilization columns. Every other tab, the sticky strip
	// and the CSV keep the plain monthly aggregation.
	var (
		sum     domain.ReportSummary
		lines   []domain.ReportLine
		refunds []domain.ReportRefund
		err     error
	)
	if tab == "staff" {
		sum, lines, refunds, err = s.reports.MonthlyReportWithUtilization(r.Context(), month, locID)
	} else {
		sum, lines, refunds, err = s.reports.MonthlyReport(r.Context(), month, locID)
	}
	if err != nil {
		http.Error(w, "failed to load report", http.StatusInternalServerError)
		return
	}
	// Only the daily tab pays for the daily read; the monthly figures above are
	// the sticky strip every tab shows.
	date := normalizeReportDate(qs.Get("date"), time.Now())
	var daily domain.DailySummary
	if tab == "daily" {
		daily, err = s.reports.DailyReport(r.Context(), date, locID)
		if err != nil {
			http.Error(w, "failed to load report", http.StatusInternalServerError)
			return
		}
	}
	// The demand block is the ops tab's whole read. Its gross is the summary's
	// own cash-basis gross (already loaded above), so revenue is never
	// recomputed and no second payments query is spent on it.
	var demand domain.ReportDemand
	if tab == "ops" {
		demand, err = s.reports.DemandReport(r.Context(), month, locID, sum.GrossIDR)
		if err != nil {
			http.Error(w, "failed to load report", http.StatusInternalServerError)
			return
		}
	}
	// Trend layer: the strip's delta compares this month-to-date against the
	// same day-of-month last month. Only the current month has a month-to-date
	// read to compare — a completed month is simply complete — so past months
	// skip the delta and just carry the sparkline series. The sparkline only
	// renders on the revenue tab, so only that tab pays for the six month reads.
	trend := domain.MonthTrend{}
	if month == currentMonth() {
		prevMonth := service.PrevMonth(month)
		if prevMonth != "" {
			cutoff := time.Now().In(wib()).Day()
			prevSum, prevErr := s.reports.MonthlyReportThrough(r.Context(), prevMonth, locID, cutoff)
			if prevErr != nil {
				http.Error(w, "failed to load report", http.StatusInternalServerError)
				return
			}
			pctBp, asOf, ok := service.MonthDelta(sum, prevSum, cutoff)
			trend = domain.MonthTrend{
				PctBp: pctBp, AsOf: asOf, OK: ok,
				CurrentIDR: sum.NetIDR, PreviousIDR: prevSum.NetIDR,
			}
		}
	}
	if tab == "revenue" {
		series, seriesErr := s.reports.MonthlyNetSeries(r.Context(), month, locID, 6)
		if seriesErr != nil {
			http.Error(w, "failed to load report", http.StatusInternalServerError)
			return
		}
		trend.Series = series
	}
	today := currentMonth()
	// Year dropdown: distinct data years + current year, desc.
	years := []string{today[:4]}
	if avail, err := s.reports.AvailableMonths(r.Context(), locID); err == nil {
		for _, m := range avail {
			y := m[:4]
			dup := false
			for _, e := range years {
				if e == y {
					dup = true
					break
				}
			}
			if !dup {
				years = append(years, y)
			}
		}
	}
	sort.Slice(years, func(i, j int) bool { return years[i] > years[j] })
	locs, _ := s.catalog.ListLocations(r.Context(), true)
	s.render(w, r, "portal_reports.html", reportsData(reportsInput{
		Month:     month,
		LocID:     locID,
		Summary:   sum,
		Lines:     lines,
		Refunds:   refunds,
		Tab:       tab,
		Page:      normalizePage(qs.Get("page")),
		Date:      date,
		Daily:     daily,
		Demand:    demand,
		Trend:     trend,
		Years:     years,
		Locations: locs,
		Query:     qs,
	}))
}

// PortalReportsCSV exports the same monthly report as Excel-friendly CSV.
// Column headers follow the UI language (r.th.* keys).
func (s *Server) PortalReportsCSV(w http.ResponseWriter, r *http.Request) {
	month, locID := s.reportParams(r)
	lang := i18n.Resolve(r)
	sum, lines, refunds, err := s.reports.MonthlyReport(r.Context(), month, locID)
	if err != nil {
		http.Error(w, "failed to load report", http.StatusInternalServerError)
		return
	}
	t := func(key string) string { return i18n.T(lang, key) }
	w.Header().Set("Content-Type", "text/csv; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`attachment; filename="laporan-%s.csv"`, month))
	cw := csv.NewWriter(w)
	_, _ = w.Write([]byte("\xef\xbb\xbf")) // BOM for Excel
	_ = cw.Write([]string{t("r.title"), month})
	_ = cw.Write([]string{t("r.gross"), strconv.FormatInt(sum.GrossIDR, 10)})
	_ = cw.Write([]string{t("r.refunds"), strconv.FormatInt(sum.RefundIDR, 10)})
	_ = cw.Write([]string{t("r.net"), strconv.FormatInt(sum.NetIDR, 10)})
	_ = cw.Write([]string{t("r.count"), strconv.Itoa(sum.Count)})
	_ = cw.Write([]string{})
	_ = cw.Write([]string{t("r.th.date"), t("r.th.cust"), t("r.th.svc"), t("r.th.staff"), t("r.th.meth"), t("r.th.amt")})
	for _, ln := range lines {
		_ = cw.Write([]string{
			ln.PaidAt.Format("2006-01-02 15:04"), ln.CustomerName, ln.ServiceName,
			ln.StaffName, string(ln.PaymentMethod), strconv.FormatInt(ln.AmountIDR, 10),
		})
	}
	if len(sum.StaffRows) > 0 {
		_ = cw.Write([]string{})
		_ = cw.Write([]string{t("r.bystaff")})
		_ = cw.Write([]string{t("r.th.staff"), t("r.th.sessions"), t("r.th.uniq"), t("r.th.avg"), t("r.gross"), t("r.refunds"), t("r.net"), t("r.th.top")})
		for _, st := range sum.StaffRows {
			top := ""
			for i, s := range st.Services {
				if i > 0 {
					top += "; "
				}
				top += s.Name + " x" + strconv.Itoa(s.Count)
			}
			_ = cw.Write([]string{
				st.Name, strconv.Itoa(st.Sessions), strconv.Itoa(st.UniqueCustomers),
				strconv.FormatInt(st.AvgIDR, 10), strconv.FormatInt(st.GrossIDR, 10),
				strconv.FormatInt(st.RefundIDR, 10), strconv.FormatInt(st.NetIDR, 10), top,
			})
		}
	}
	if len(refunds) > 0 {
		_ = cw.Write([]string{})
		_ = cw.Write([]string{t("r.reflines")})
		_ = cw.Write([]string{t("r.th.date"), t("r.th.cust"), t("r.th.svc"), t("r.th.reason"), t("r.th.by"), t("r.th.amt")})
		for _, rf := range refunds {
			_ = cw.Write([]string{
				rf.CreatedAt.Format("2006-01-02 15:04"), rf.CustomerName, rf.ServiceName,
				rf.Reason, rf.CreatedBy, strconv.FormatInt(-rf.AmountIDR, 10),
			})
		}
	}
	cw.Flush()
}

// PortalRecordRefund stores a refund against a booking's paid payment.
func (s *Server) PortalRecordRefund(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	amount, err := strconv.ParseInt(r.FormValue("amount"), 10, 64)
	if err != nil {
		s.refundFail(w, r, id, "invalid amount")
		return
	}
	u := middleware.UserFrom(r.Context())
	var actorID *uuid.UUID
	if u != nil {
		actorID = &u.ID
	}
	if _, err := s.reports.RecordRefund(r.Context(), id, amount, r.FormValue("reason"), actorID); err != nil {
		s.refundFail(w, r, id, err.Error())
		return
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Refund recorded")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?success=%s", id, url.QueryEscape("Refund recorded")), http.StatusSeeOther)
}

func (s *Server) refundFail(w http.ResponseWriter, r *http.Request, id uuid.UUID, msg string) {
	if htmx.IsRequest(r) {
		s.toast(w, "error", msg)
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?error=%s", id, url.QueryEscape(msg)), http.StatusSeeOther)
}

// PortalVoidRefund voids a mistyped refund (admin-only correction).
func (s *Server) PortalVoidRefund(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	actorID, ok := s.requirePortalAdmin(r)
	if !ok {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	rid, err := uuid.Parse(chi.URLParam(r, "rid"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.reports.VoidRefund(r.Context(), rid, r.FormValue("void_reason"), actorID); err != nil {
		s.refundFail(w, r, id, err.Error())
		return
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Refund voided")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?success=%s", id, url.QueryEscape("Refund voided")), http.StatusSeeOther)
}
