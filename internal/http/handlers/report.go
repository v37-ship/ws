package handlers

import (
	"encoding/csv"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"time"

	"spa-booking/internal/http/htmx"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

var monthRe = regexp.MustCompile(`^\d{4}-(0[1-9]|1[0-2])$`)

// currentMonth returns the current YYYY-MM in Asia/Jakarta.
func currentMonth() string {
	loc, _ := time.LoadLocation("Asia/Jakarta")
	if loc == nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	return time.Now().In(loc).Format("2006-01")
}

// reportParams parses ?month=YYYY-MM&location_id= with safe defaults.
func (s *Server) reportParams(r *http.Request) (string, uuid.UUID) {
	month := r.URL.Query().Get("month")
	if !monthRe.MatchString(month) {
		month = currentMonth()
	}
	locID, _ := uuid.Parse(r.URL.Query().Get("location_id"))
	return month, locID
}

// PortalReports renders the monthly revenue report (all portal staff).
func (s *Server) PortalReports(w http.ResponseWriter, r *http.Request) {
	month, locID := s.reportParams(r)
	sum, lines, refunds, err := s.reports.MonthlyReport(r.Context(), month, locID)
	if err != nil {
		http.Error(w, "failed to load report", http.StatusInternalServerError)
		return
	}
	locs, _ := s.catalog.ListLocations(r.Context(), true)
	s.render(w, r, "portal_reports.html", pageData{
		"Month":     month,
		"LocID":     locID.String(),
		"Locations": locs,
		"Summary":   sum,
		"Lines":     lines,
		"Refunds":   refunds,
	})
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
		http.Error(w, "invalid request", http.StatusBadRequest)
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
