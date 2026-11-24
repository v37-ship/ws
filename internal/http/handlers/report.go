package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strconv"
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

// currentMonth returns the current YYYY-MM in Asia/Jakarta.
func currentMonth() string {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	if loc == nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	return time.Now().In(loc).Format("2006-01")
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
	// ?month=), the tab and the location. Legacy ?year=&mon= pairs and an
	// explicit ?page= are dropped so no link disagrees about the month, and
	// page 1 keeps a clean address.
	base := func() url.Values {
		v := url.Values{}
		for k, vals := range in.Query {
			switch k {
			case "page", "tab", "month", "year", "mon":
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
		"PrevURL":      "",
		"NextURL":      "",
		"PrevMonthURL": monthURL(prevMonth),
		"NextMonthURL": monthURL(nextMonth),
		"TodayURL":     monthURL(today),
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
	sum, lines, refunds, err := s.reports.MonthlyReport(r.Context(), month, locID)
	if err != nil {
		http.Error(w, "failed to load report", http.StatusInternalServerError)
		return
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
	qs := r.URL.Query()
	s.render(w, r, "portal_reports.html", reportsData(reportsInput{
		Month:     month,
		LocID:     locID,
		Summary:   sum,
		Lines:     lines,
		Refunds:   refunds,
		Tab:       normalizeReportTab(qs.Get("tab")),
		Page:      normalizePage(qs.Get("page")),
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
