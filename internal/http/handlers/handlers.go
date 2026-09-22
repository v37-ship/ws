package handlers

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/auth"
	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

func (s *Server) Home(w http.ResponseWriter, r *http.Request) {
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		http.Error(w, "failed to load locations", http.StatusInternalServerError)
		return
	}
	var preview []domain.Service
	for _, l := range locs {
		svcs, _ := s.catalog.ListServices(r.Context(), l.ID, true)
		preview = append(preview, svcs...)
		if len(preview) >= 6 {
			break
		}
	}
	if len(preview) > 6 {
		preview = preview[:6]
	}
	openJobs, _ := s.career.ListJobs(r.Context(), true)
	testimonials, _ := s.tm.ListApproved(r.Context(), 12)
	banners, _ := s.hero.ListActive(r.Context())
	s.render(w, r, "home.html", pageData{
		"Locations": locs, "PreviewServices": preview,
		"OpenJobCount": len(openJobs), "Testimonials": testimonials,
		"HeroBanners": banners,
	})
}

func (s *Server) LoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login.html", pageData{"Next": r.URL.Query().Get("next"), "Success": r.URL.Query().Get("success")})
}

func (s *Server) Login(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u, sess, err := s.auth.Login(r.Context(), r.FormValue("email"), r.FormValue("password"))
	if err != nil {
		s.render(w, r, "login.html", pageData{
			"Error": "Invalid email or password",
			"Email": r.FormValue("email"),
			"Next":  r.FormValue("next"),
		})
		return
	}
	s.setSessionCookie(w, sess.ID)
	next := r.FormValue("next")
	if next == "" || !strings.HasPrefix(next, "/") {
		if u.Role.IsStaff() {
			next = "/portal"
		} else {
			next = "/book"
		}
	}
	http.Redirect(w, r, next, http.StatusSeeOther)
}

func (s *Server) RegisterForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "register.html", nil)
}

func (s *Server) Register(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	_, sess, err := s.auth.Register(r.Context(), r.FormValue("email"), r.FormValue("password"), r.FormValue("full_name"), r.FormValue("phone"))
	if err != nil {
		msg := "Could not register"
		if errors.Is(err, service.ErrEmailTaken) {
			msg = "Email already registered"
		} else if errors.Is(err, service.ErrValidation) {
			msg = err.Error()
		}
		s.render(w, r, "register.html", pageData{
			"Error": msg, "Email": r.FormValue("email"), "FullName": r.FormValue("full_name"), "Phone": r.FormValue("phone"),
		})
		return
	}
	s.setSessionCookie(w, sess.ID)
	http.Redirect(w, r, "/book", http.StatusSeeOther)
}

func (s *Server) Logout(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	if c, err := r.Cookie("session"); err == nil {
		_ = s.auth.Logout(r.Context(), c.Value)
	}
	s.clearSessionCookie(w)
	http.Redirect(w, r, "/", http.StatusSeeOther)
}

func (s *Server) BookPage(w http.ResponseWriter, r *http.Request) {
	// Staff always book on behalf of customers via the single portal flow.
	if u := middleware.UserFrom(r.Context()); u != nil && u.Role.IsStaff() {
		http.Redirect(w, r, "/portal/bookings/new", http.StatusSeeOther)
		return
	}
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	place := string(domain.PlaceOnsite)
	if r.URL.Query().Get("place") == string(domain.PlaceHomecare) {
		place = string(domain.PlaceHomecare)
	}
	locID := r.URL.Query().Get("location_id")
	if _, err := uuid.Parse(locID); err != nil && len(locs) == 1 {
		// Single-branch shop: pre-answer the required location radio.
		locID = locs[0].ID.String()
	}
	data := pageData{
		"Locations":  locs,
		"LocationID": locID,
		"Place":      place,
		"Date":       time.Now().In(time.FixedZone("WIB", 7*3600)).Format("2006-01-02"),
		"Days":       nextDays(14, i18n.Resolve(r)),
	}
	if lid, err := uuid.Parse(locID); err == nil {
		svcs, _ := s.catalog.ListServices(r.Context(), lid, true)
		data["Services"] = svcs
	}
	s.render(w, r, "book.html", data)
}

func (s *Server) BookServices(w http.ResponseWriter, r *http.Request) {
	lid, err := uuid.Parse(r.URL.Query().Get("location_id"))
	if err != nil {
		s.renderPartial(w, r, "services_partial.html", pageData{})
		return
	}
	svcs, err := s.catalog.ListServices(r.Context(), lid, true)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("view") == "cards" {
		s.renderPartial(w, r, "service_cards.html", pageData{"Services": svcs, "LocationID": lid.String()})
		return
	}
	s.renderPartial(w, r, "services_partial.html", pageData{"Services": svcs, "LocationID": lid.String()})
}

func (s *Server) BookStaff(w http.ResponseWriter, r *http.Request) {
	lid, _ := uuid.Parse(r.URL.Query().Get("location_id"))
	sid, err := uuid.Parse(r.URL.Query().Get("service_id"))
	if err != nil {
		s.renderPartial(w, r, "staff_partial.html", pageData{})
		return
	}
	staff, err := s.catalog.StaffForService(r.Context(), lid, sid)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	if r.URL.Query().Get("view") == "cards" {
		s.renderPartial(w, r, "staff_cards.html", pageData{"Staff": staff, "ServiceID": sid.String(), "LocationID": lid.String()})
		return
	}
	s.renderPartial(w, r, "staff_partial.html", pageData{"Staff": staff, "ServiceID": sid.String(), "LocationID": lid.String()})
}

func (s *Server) BookSlots(w http.ResponseWriter, r *http.Request) {
	lid, err1 := uuid.Parse(r.URL.Query().Get("location_id"))
	sid, err2 := uuid.Parse(r.URL.Query().Get("service_id"))
	dateStr := r.URL.Query().Get("date")
	if err1 != nil || err2 != nil || dateStr == "" {
		s.renderPartial(w, r, "slots_partial.html", pageData{"SlotsError": "Choose location, service, and date"})
		return
	}
	day, err := time.ParseInLocation("2006-01-02", dateStr, time.FixedZone("WIB", 7*3600))
	if err != nil {
		s.renderPartial(w, r, "slots_partial.html", pageData{"SlotsError": "Invalid date"})
		return
	}
	var staffID uuid.UUID
	if v := r.URL.Query().Get("staff_id"); v != "" {
		staffID, _ = uuid.Parse(v)
	}
	slots, err := s.avail.SlotsDetailed(r.Context(), lid, sid, staffID, day)
	if err != nil {
		s.renderPartial(w, r, "slots_partial.html", pageData{"SlotsError": err.Error()})
		return
	}
	if r.URL.Query().Get("view") == "cards" {
		s.renderPartial(w, r, "slot_groups.html", pageData{"SlotGroups": groupSlots(slots, i18n.Resolve(r))})
		return
	}
	free := make([]domain.Slot, 0, len(slots))
	for _, s := range slots {
		if s.Available {
			free = append(free, s.Slot)
		}
	}
	s.renderPartial(w, r, "slots_partial.html", pageData{"Slots": free})
}

type bookingDay struct {
	Value   string
	Weekday string
	DayNum  string
	Month   string
}

func nextDays(n int, lang string) []bookingDay {
	wib := time.FixedZone("WIB", 7*3600)
	now := time.Now().In(wib)
	days := make([]bookingDay, 0, n)
	for i := 0; i < n; i++ {
		d := now.AddDate(0, 0, i)
		label := i18n.T(lang, "book.today")
		if i == 1 {
			label = i18n.T(lang, "book.tmrow")
		} else if i > 1 {
			label = i18n.WeekdayShort(lang, d.Weekday())
		}
		days = append(days, bookingDay{
			Value:   d.Format("2006-01-02"),
			Weekday: label,
			DayNum:  d.Format("2"),
			Month:   i18n.MonthShort(lang, d.Month()),
		})
	}
	return days
}

type slotGroup struct {
	Title string
	Slots []service.SlotInfo
}

func groupSlots(slots []service.SlotInfo, lang string) []slotGroup {
	wib := time.FixedZone("WIB", 7*3600)
	groups := []slotGroup{
		{Title: i18n.T(lang, "slots.morn")},
		{Title: i18n.T(lang, "slots.aft")},
		{Title: i18n.T(lang, "slots.eve")},
	}
	for _, s := range slots {
		h := s.Slot.StartAt.In(wib).Hour()
		switch {
		case h < 11:
			groups[0].Slots = append(groups[0].Slots, s)
		case h < 15:
			groups[1].Slots = append(groups[1].Slots, s)
		default:
			groups[2].Slots = append(groups[2].Slots, s)
		}
	}
	out := groups[:0]
	for _, g := range groups {
		if len(g.Slots) > 0 {
			out = append(out, g)
		}
	}
	return out
}

func (s *Server) CreateBooking(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	lid, err1 := uuid.Parse(r.FormValue("location_id"))
	sid, err2 := uuid.Parse(r.FormValue("service_id"))
	staffID, err3 := uuid.Parse(r.FormValue("staff_id_selected"))
	if staffID == uuid.Nil {
		staffID, err3 = uuid.Parse(r.FormValue("staff_id"))
	}
	startAt, err4 := time.Parse(time.RFC3339, r.FormValue("start_at"))
	if err1 != nil || err2 != nil || err3 != nil || err4 != nil {
		http.Error(w, "Please select location, service, and a time slot", http.StatusBadRequest)
		return
	}
	place := domain.Place(r.FormValue("place"))
	visitAddress := r.FormValue("visit_address")
	promoCode := r.FormValue("promo_code")
	if u == nil {
		b, _, err := s.book.CreateGuestPending(r.Context(), service.GuestBookingRequest{
			LocationID: lid, ServiceID: sid, StaffID: staffID, StartAt: startAt,
			Notes: r.FormValue("notes"), Place: place, VisitAddress: visitAddress,
			PromoCode: promoCode, Name: r.FormValue("guest_name"),
			Phone: r.FormValue("guest_phone"), Email: r.FormValue("guest_email"),
		})
		if err != nil {
			if errors.Is(err, service.ErrConflict) {
				http.Error(w, "That slot was just taken. Pick another time.", http.StatusConflict)
				return
			}
			if errors.Is(err, service.ErrValidation) {
				http.Error(w, err.Error(), http.StatusBadRequest)
				return
			}
			s.log.Error("create guest booking", "err", err)
			http.Error(w, "Could not create booking", http.StatusInternalServerError)
			return
		}
		token := ""
		if b.AccessToken != nil {
			token = *b.AccessToken
		}
		http.Redirect(w, r, "/bookings/"+b.ID.String()+"/pay?t="+token, http.StatusSeeOther)
		return
	}
	b, _, err := s.book.CreatePending(r.Context(), u.ID, lid, sid, staffID, startAt,
		r.FormValue("notes"), place, visitAddress, promoCode)
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			http.Error(w, "That slot was just taken. Pick another time.", http.StatusConflict)
			return
		}
		if errors.Is(err, service.ErrValidation) {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		s.log.Error("create booking", "err", err)
		http.Error(w, "Could not create booking", http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, "/bookings/"+b.ID.String()+"/pay", http.StatusSeeOther)
}

func (s *Server) MyBookings(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	list, err := s.book.ListMine(r.Context(), u.ID)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	data := pageData{"Bookings": list, "Error": r.URL.Query().Get("error")}
	if cid := r.URL.Query().Get("cancelled"); cid != "" {
		if cid, err := uuid.Parse(cid); err == nil {
			if b, err := s.book.GetBooking(r.Context(), cid); err == nil &&
				(b.OwnedBy(u.ID) || u.Role == domain.RoleAdmin) {
				data["CancelledBooking"] = b
			}
		}
	}
	s.render(w, r, "bookings.html", data)
}

func (s *Server) PayPage(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, token, ok := s.bookingAccess(w, r, id)
	if !ok {
		return
	}
	p, _ := s.book.GetPaymentForBooking(r.Context(), id)
	payLink := ""
	if token != "" {
		payLink = s.cfg.BaseURL + "/bookings/" + id.String() + "/pay?t=" + token
	}
	data := pageData{
		"Booking":  b,
		"Payment":  p,
		"Token":    token,
		"PayLink":  payLink,
		"MockMode": s.cfg.MidtransServer == "",
	}
	if p != nil && strings.HasPrefix(p.QRString, "http") {
		data["QRImageURL"] = p.QRString
	} else if p != nil && p.QRString != "" {
		data["QRImageURL"] = "https://api.qrserver.com/v1/create-qr-code/?size=220x220&data=" + url.QueryEscape(p.QRString)
	}
	s.render(w, r, "pay.html", data)
}

func (s *Server) BookingStatusPartial(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, _, ok := s.bookingAccess(w, r, id)
	if !ok {
		return
	}
	s.renderPartial(w, r, "pay_status.html", pageData{"Booking": b})
}

func (s *Server) CancelBooking(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, token, ok := s.bookingAccess(w, r, id)
	if !ok {
		return
	}
	var actorID *uuid.UUID
	if u != nil && !b.IsGuest() {
		actorID = &u.ID
	}
	if err := s.book.Cancel(r.Context(), id, actorID, u != nil && u.Role.IsStaff(), r.FormValue("reason")); err != nil {
		if b.IsGuest() {
			http.Redirect(w, r, "/bookings/"+id.String()+"/pay?t="+token+"&error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		} else {
			http.Redirect(w, r, "/bookings?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		}
		return
	}
	if b.IsGuest() {
		http.Redirect(w, r, "/bookings/"+id.String()+"/pay?t="+token, http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/bookings?cancelled="+id.String(), http.StatusSeeOther)
}

func (s *Server) MockPay(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) || s.cfg.MidtransServer != "" {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, token, ok := s.bookingAccess(w, r, id)
	if !ok {
		return
	}
	var actorID *uuid.UUID
	if u != nil && !b.IsGuest() {
		actorID = &u.ID
	}
	if err := s.book.ConfirmMockPayment(r.Context(), id, actorID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	dest := "/bookings/" + id.String() + "/pay"
	if token != "" {
		dest += "?t=" + token
	}
	http.Redirect(w, r, dest, http.StatusSeeOther)
}

func (s *Server) MidtransWebhook(w http.ResponseWriter, r *http.Request) {
	body, err := io.ReadAll(io.LimitReader(r.Body, 1<<20))
	if err != nil {
		http.Error(w, "bad body", http.StatusBadRequest)
		return
	}
	if err := s.book.HandleMidtransNotification(r.Context(), body); err != nil {
		s.log.Warn("midtrans webhook", "err", err)
		http.Error(w, "rejected", http.StatusBadRequest)
		return
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte("ok"))
}

func (s *Server) AdminHome(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "admin_home.html", nil)
}

func (s *Server) AdminLocations(w http.ResponseWriter, r *http.Request) {
	locs, _ := s.catalog.ListLocations(r.Context(), false)
	s.render(w, r, "admin_locations.html", pageData{"Locations": locs})
}

func (s *Server) AdminCreateLocation(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	l := &domain.Location{
		Name: r.FormValue("name"), Address: r.FormValue("address"),
		City: r.FormValue("city"), Timezone: r.FormValue("timezone"), Active: true,
	}
	if err := s.catalog.SaveLocation(r.Context(), l); err != nil {
		locs, _ := s.catalog.ListLocations(r.Context(), false)
		s.render(w, r, "admin_locations.html", pageData{"Locations": locs, "Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/locations", http.StatusSeeOther)
}

func (s *Server) AdminBookings(w http.ResponseWriter, r *http.Request) {
	list, err := s.book.ListAdmin(r.Context())
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_bookings.html", pageData{"Bookings": list})
}

type PortalStats struct {
	TodayTotal int
	Confirmed  int
	CheckedIn  int
	InService  int
	Completed  int
}

type locationServices struct {
	Location domain.Location
	Services []domain.Service
}

func (s *Server) ServicesPage(w http.ResponseWriter, r *http.Request) {
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		http.Error(w, "failed to load locations", http.StatusInternalServerError)
		return
	}
	groups := make([]locationServices, 0, len(locs))
	for _, l := range locs {
		svcs, _ := s.catalog.ListServices(r.Context(), l.ID, false)
		groups = append(groups, locationServices{Location: l, Services: svcs})
	}
	summaries := make(map[string]string)
	for _, g := range groups {
		for _, svc := range g.Services {
			if _, ok := summaries[svc.ID.String()]; !ok {
				hours, _ := s.catalog.ListServiceHours(r.Context(), svc.ID)
				summaries[svc.ID.String()] = service.SummarizeServiceHours(hours, i18n.Resolve(r))
			}
		}
	}
	s.render(w, r, "services.html", pageData{"LocationGroups": groups, "Hours": summaries})
}

func (s *Server) PortalDashboard(w http.ResponseWriter, r *http.Request) {
	nowWIB := time.Now().In(time.FixedZone("WIB", 7*3600))
	todayStr := nowWIB.Format("2006-01-02")
	bookings, err := s.book.ListFiltered(r.Context(), repo.FilterParams{Date: todayStr})
	if err != nil {
		http.Error(w, "failed to load bookings", http.StatusInternalServerError)
		return
	}
	var stats PortalStats
	stats.TodayTotal = len(bookings)
	for _, b := range bookings {
		switch b.Status {
		case domain.BookingConfirmed:
			stats.Confirmed++
		case domain.BookingCheckedIn:
			stats.CheckedIn++
		case domain.BookingInService:
			stats.InService++
		case domain.BookingCompleted:
			stats.Completed++
		}
	}
	s.render(w, r, "portal_dashboard.html", pageData{
		"TodayStr":      i18n.DateLong(i18n.Resolve(r), nowWIB),
		"TodayBookings": bookings,
		"Stats":         stats,
	})
}

func (s *Server) PortalBookings(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	date := r.URL.Query().Get("date")
	status := domain.BookingStatus(r.URL.Query().Get("status"))
	list, err := s.book.ListFiltered(r.Context(), repo.FilterParams{
		Search: q, Date: date, Status: status, Limit: 100,
	})
	if err != nil {
		http.Error(w, "failed to load bookings", http.StatusInternalServerError)
		return
	}
	data := pageData{
		"Bookings": list,
		"Search":   q,
		"Date":     date,
		"Status":   string(status),
	}
	s.renderAuto(w, r, "portal_booking_rows.html", "portal_bookings.html", data)
}

// PortalQueuePartial serves today's live queue rows for HTMX polling.
func (s *Server) PortalQueuePartial(w http.ResponseWriter, r *http.Request) {
	nowWIB := time.Now().In(time.FixedZone("WIB", 7*3600))
	bookings, err := s.book.ListFiltered(r.Context(), repo.FilterParams{Date: nowWIB.Format("2006-01-02")})
	if err != nil {
		http.Error(w, "failed to load queue", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, r, "portal_queue_rows.html", pageData{
		"TodayBookings": bookings,
		"TodayStr":      i18n.DateLong(i18n.Resolve(r), nowWIB),
	})
}

func (s *Server) PortalNewBookingForm(w http.ResponseWriter, r *http.Request) {
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		http.Error(w, "failed to load locations", http.StatusInternalServerError)
		return
	}
	customerID := r.URL.Query().Get("customer_id")
	today := time.Now().In(time.FixedZone("WIB", 7*3600)).Format("2006-01-02")
	s.render(w, r, "portal_booking_new.html", pageData{
		"Locations":       locs,
		"CustomerID":      customerID,
		"PrefillCustomer": s.prefillCustomer(r.Context(), customerID),
		"Date":            today,
	})
}

// prefillCustomer resolves a customer id for the booking picker chip.
// Returns nil when missing, invalid, or not a customer.
func (s *Server) prefillCustomer(ctx context.Context, idStr string) *domain.User {
	id, err := uuid.Parse(idStr)
	if err != nil {
		return nil
	}
	cust, err := s.auth.GetUser(ctx, id)
	if err != nil || cust.Role != domain.RoleCustomer {
		return nil
	}
	return cust
}

// PortalCustomerSearch serves the booking picker live-search results.
func (s *Server) PortalCustomerSearch(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	list, err := s.auth.SearchCustomers(r.Context(), q)
	if err != nil {
		http.Error(w, "search failed", http.StatusInternalServerError)
		return
	}
	s.renderPartial(w, r, "customer_search_rows.html", pageData{
		"Customers": list,
		"Query":     q,
	})
}

func (s *Server) PortalCreateBooking(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	custID, err := uuid.Parse(r.FormValue("customer_id"))
	if err != nil {
		http.Error(w, "invalid customer", http.StatusBadRequest)
		return
	}
	locID, _ := uuid.Parse(r.FormValue("location_id"))
	svcID, _ := uuid.Parse(r.FormValue("service_id"))
	staffID, _ := uuid.Parse(r.FormValue("staff_id_selected"))
	startAtStr := r.FormValue("start_at")
	notes := r.FormValue("notes")
	place := domain.Place(r.FormValue("place"))
	visitAddress := r.FormValue("visit_address")
	source := r.FormValue("source")
	payMethod := domain.PaymentMethod(r.FormValue("payment_method"))
	payPurpose := domain.PaymentPurpose(r.FormValue("payment_purpose"))

	startAt, err := time.Parse(time.RFC3339, startAtStr)
	if err != nil {
		loc, _ := time.LoadLocation("Asia/Jakarta")
		if loc == nil {
			loc = time.FixedZone("WIB", 7*3600)
		}
		startAt, err = time.ParseInLocation("2006-01-02T15:04", startAtStr, loc)
		if err != nil {
			locs, _ := s.catalog.ListLocations(r.Context(), true)
			s.render(w, r, "portal_booking_new.html", pageData{
				"Locations": locs, "Error": "Please select an available time slot",
				"CustomerID":      r.FormValue("customer_id"),
				"PrefillCustomer": s.prefillCustomer(r.Context(), r.FormValue("customer_id")),
				"LocationID":      r.FormValue("location_id"),
				"PromoCode":       r.FormValue("promo_code"),
				"Date":            r.FormValue("date"), "Notes": notes,
			})
			return
		}
	}

	b, _, err := s.book.CreateStaffBooking(r.Context(), service.StaffBookingRequest{
		CustomerID: custID, LocationID: locID, ServiceID: svcID, StaffID: staffID,
		StartAt: startAt, Notes: notes, Place: place, VisitAddress: visitAddress, Source: source,
		PaymentMethod: payMethod, PaymentPurpose: payPurpose, PromoCode: r.FormValue("promo_code"), ActorID: u.ID,
	})
	if err != nil {
		locs, _ := s.catalog.ListLocations(r.Context(), true)
		s.render(w, r, "portal_booking_new.html", pageData{
			"Locations": locs, "Error": err.Error(),
			"CustomerID":      r.FormValue("customer_id"),
			"PrefillCustomer": s.prefillCustomer(r.Context(), r.FormValue("customer_id")),
			"LocationID":      r.FormValue("location_id"),
			"PromoCode":       r.FormValue("promo_code"),
			"Date":            r.FormValue("date"), "Notes": notes,
		})
		return
	}
	http.Redirect(w, r, "/portal/bookings/"+b.ID.String(), http.StatusSeeOther)
}

func (s *Server) PortalBookingDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := s.book.GetBooking(r.Context(), id)
	if errors.Is(err, repo.ErrNotFound) {
		http.NotFound(w, r)
		return
	}
	if err != nil {
		s.log.Error("portal booking detail", "id", id, "err", err)
		http.Error(w, "Could not load booking", http.StatusInternalServerError)
		return
	}
	staffList, _ := s.catalog.ListStaff(r.Context(), b.LocationID, true)
	services, _ := s.catalog.ListServices(r.Context(), b.LocationID, false)
	logs, _ := s.book.ListLogs(r.Context(), b.ID)
	pay, payRefunds, refundedTotal, _ := s.reports.BookingRefunds(r.Context(), b.ID)
	var refundable int64
	if pay != nil && pay.Status == domain.PaymentPaid {
		refundable = pay.AmountIDR - refundedTotal
	}
	s.render(w, r, "portal_booking_detail.html", pageData{
		"Booking":   b,
		"StaffList": staffList,
		"Services":  services,
		"AllStatuses": []domain.BookingStatus{
			domain.BookingPendingPayment, domain.BookingConfirmed, domain.BookingCheckedIn,
			domain.BookingInService, domain.BookingCompleted, domain.BookingCancelled,
			domain.BookingExpired, domain.BookingNoShow,
		},
		"Logs":          logs,
		"Payment":       pay,
		"Refunds":       payRefunds,
		"RefundedTotal": refundedTotal,
		"Refundable":    refundable,
		"GuestLink":     s.cfg.BaseURL + "/bookings/" + b.ID.String() + "/pay?t=" + b.TokenParam(),
		"Success":       r.URL.Query().Get("success"),
		"Error":         r.URL.Query().Get("error"),
	})
}

func (s *Server) PortalUpdateBookingStatus(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u := middleware.UserFrom(r.Context())
	newStatus := domain.BookingStatus(r.FormValue("status"))
	note := r.FormValue("note")
	var actorID *uuid.UUID
	if u != nil {
		actorID = &u.ID
	}
	if err := s.book.UpdateStatus(r.Context(), id, newStatus, actorID, note); err != nil {
		if htmx.IsRequest(r) {
			s.toast(w, "error", err.Error())
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?error=%s", id, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Status updated to "+string(newStatus))
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?toast=%s&kind=%s", id, url.QueryEscape("Status updated to "+string(newStatus)), "success"), http.StatusSeeOther)
}

func (s *Server) PortalRescheduleBooking(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	u := middleware.UserFrom(r.Context())
	staffID, _ := uuid.Parse(r.FormValue("staff_id"))
	startAtStr := r.FormValue("start_at")
	note := r.FormValue("note")

	loc, _ := time.LoadLocation("Asia/Jakarta")
	if loc == nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	startAt, err := time.ParseInLocation("2006-01-02T15:04", startAtStr, loc)
	if err != nil {
		http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?error=%s", id, url.QueryEscape("Invalid date/time format")), http.StatusSeeOther)
		return
	}

	var actorID *uuid.UUID
	if u != nil {
		actorID = &u.ID
	}
	if err := s.book.Reschedule(r.Context(), id, staffID, startAt, actorID, note); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?error=%s", id, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?success=%s", id, url.QueryEscape("Booking rescheduled successfully")), http.StatusSeeOther)
}

// PortalCorrectBooking applies an admin correction to booking details
// (service/reprice, place/address, notes) with a mandatory logged reason.
func (s *Server) PortalCorrectBooking(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	actorID, ok := s.requirePortalAdmin(r)
	if !ok {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	svcID, _ := uuid.Parse(r.FormValue("service_id"))
	priceBelowPaid, err := s.book.CorrectDetails(r.Context(), id, service.CorrectDetailsRequest{
		ServiceID:    svcID,
		Place:        domain.Place(r.FormValue("place")),
		VisitAddress: r.FormValue("visit_address"),
		Notes:        r.FormValue("notes"),
		Reason:       r.FormValue("reason"),
		ActorID:      *actorID,
	})
	if err != nil {
		if htmx.IsRequest(r) {
			s.toast(w, "error", err.Error())
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?error=%s", id, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}
	msg := "Booking details corrected"
	if priceBelowPaid {
		msg += " — paid amount exceeds new price, consider refunding the difference"
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", msg)
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/bookings/%s?success=%s", id, url.QueryEscape(msg)), http.StatusSeeOther)
}

func (s *Server) PortalCustomers(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")
	list, err := s.auth.ListCustomers(r.Context(), q)
	if err != nil {
		http.Error(w, "failed to load customers", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "portal_customers.html", pageData{
		"Customers": list,
		"Query":     q,
	})
}

func (s *Server) PortalNewCustomerForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "portal_customer_new.html", nil)
}

func (s *Server) PortalCreateCustomer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	fullName := r.FormValue("full_name")
	phone := r.FormValue("phone")
	email := r.FormValue("email")
	babyName := r.FormValue("baby_name")
	momCondition := r.FormValue("mom_condition")
	medicalNotes := r.FormValue("medical_notes")
	dobStr := r.FormValue("baby_dob")
	var babyDOB *time.Time
	if dobStr != "" {
		if t, err := time.Parse("2006-01-02", dobStr); err == nil {
			babyDOB = &t
		}
	}

	u, err := s.auth.RegisterCustomerByStaff(r.Context(), fullName, phone, email, babyName, babyDOB, momCondition, medicalNotes)
	if err != nil {
		s.render(w, r, "portal_customer_new.html", pageData{
			"Error":        err.Error(),
			"FullName":     fullName,
			"Phone":        phone,
			"Email":        email,
			"BabyName":     babyName,
			"BabyDOB":      dobStr,
			"MomCondition": momCondition,
			"MedicalNotes": medicalNotes,
		})
		return
	}
	http.Redirect(w, r, "/portal/bookings/new?customer_id="+u.ID.String(), http.StatusSeeOther)
}

func (s *Server) PortalCustomerDetail(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cust, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	bookings, _ := s.book.ListMine(r.Context(), id)
	s.render(w, r, "portal_customer_detail.html", pageData{
		"Customer": cust,
		"Bookings": bookings,
		"Success":  r.URL.Query().Get("success"),
		"Error":    r.URL.Query().Get("error"),
		"Toast":    r.URL.Query().Get("toast"),
		"Kind":     r.URL.Query().Get("kind"),
	})
}

func (s *Server) PortalEditCustomerForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cust, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	dob := ""
	if cust.BabyDOB != nil {
		dob = cust.BabyDOB.Format("2006-01-02")
	}
	s.render(w, r, "portal_customer_edit.html", pageData{
		"Customer": cust,
		"FullName": cust.FullName, "Phone": cust.Phone, "Email": cust.Email,
		"BabyName": cust.BabyName, "BabyDOB": dob,
		"MomCondition": cust.MomCondition, "MedicalNotes": cust.MedicalNotes,
	})
}

func (s *Server) PortalUpdateCustomer(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var babyDOB *time.Time
	if dobStr := r.FormValue("baby_dob"); dobStr != "" {
		if t, err := time.Parse("2006-01-02", dobStr); err == nil {
			babyDOB = &t
		}
	}
	form := pageData{
		"FullName": r.FormValue("full_name"), "Phone": r.FormValue("phone"),
		"Email": r.FormValue("email"), "BabyName": r.FormValue("baby_name"),
		"BabyDOB": r.FormValue("baby_dob"), "MomCondition": r.FormValue("mom_condition"),
		"MedicalNotes": r.FormValue("medical_notes"),
	}
	cust, err := s.auth.UpdateCustomer(r.Context(), id,
		r.FormValue("full_name"), r.FormValue("phone"), r.FormValue("email"),
		r.FormValue("baby_name"), babyDOB,
		r.FormValue("mom_condition"), r.FormValue("medical_notes"))
	if err != nil {
		form["Error"] = err.Error()
		if existing, gerr := s.auth.GetUser(r.Context(), id); gerr == nil {
			form["Customer"] = existing
		}
		s.render(w, r, "portal_customer_edit.html", form)
		return
	}
	http.Redirect(w, r, "/portal/customers/"+cust.ID.String()+"?toast="+url.QueryEscape("Customer updated")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) PortalDeleteCustomer(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.auth.DeleteCustomer(r.Context(), id); err != nil {
		if htmx.IsRequest(r) {
			s.toast(w, "error", err.Error())
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, fmt.Sprintf("/portal/customers/%s?error=%s", id, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Customer removed")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/customers?toast="+url.QueryEscape("Customer removed")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) PortalToggleCustomerActive(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cust, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.auth.SetUserActive(r.Context(), id, !cust.IsActive)
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Customer status updated")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/portal/customers/%s?toast=%s&kind=success", id, url.QueryEscape("Customer status updated")), http.StatusSeeOther)
}

func (s *Server) PortalSchedule(w http.ResponseWriter, r *http.Request) {
	locs, _ := s.catalog.ListLocations(r.Context(), false)
	var staff []domain.Staff
	var services []domain.Service
	for _, l := range locs {
		st, _ := s.catalog.ListStaff(r.Context(), l.ID, false)
		staff = append(staff, st...)
		svcs, _ := s.catalog.ListServices(r.Context(), l.ID, true)
		services = append(services, svcs...)
	}
	svcNames := make(map[string]string, len(services))
	for _, svc := range services {
		svcNames[svc.ID.String()] = svc.Name
	}
	staffServices := make(map[string][]string, len(staff))
	for _, st := range staff {
		ids, _ := s.catalog.StaffServiceIDs(r.Context(), st.ID)
		for _, id := range ids {
			if name, ok := svcNames[id.String()]; ok {
				staffServices[st.ID.String()] = append(staffServices[st.ID.String()], name)
			}
		}
	}
	timeOffs, _ := s.catalog.ListStaffTimeOff(r.Context(), uuid.Nil)
	s.render(w, r, "portal_schedule.html", pageData{
		"Locations": locs,
		"Staff":     staff,
		"Services":  services,
		"StaffSvcs": staffServices,
		"TimeOffs":  timeOffs,
		"Success":   r.URL.Query().Get("success"),
		"Error":     r.URL.Query().Get("error"),
	})
}

func (s *Server) PortalCreateStaff(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	lid, _ := uuid.Parse(r.FormValue("location_id"))
	var serviceIDs []uuid.UUID
	for _, v := range r.Form["service_ids"] {
		if id, err := uuid.Parse(v); err == nil {
			serviceIDs = append(serviceIDs, id)
		}
	}
	st := &domain.Staff{
		LocationID: lid, Name: r.FormValue("name"), Bio: r.FormValue("bio"), Active: true,
		PhotoURL: strings.TrimSpace(r.FormValue("photo_url")),
	}
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "staff")
		if msg != "" {
			http.Redirect(w, r, "/portal/schedule?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		st.PhotoPath = rel
	}
	if err := s.catalog.SaveStaff(r.Context(), st, serviceIDs); err != nil {
		http.Redirect(w, r, "/portal/schedule?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/schedule?toast="+url.QueryEscape("Therapist added")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) PortalCreateTimeOff(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	staffID, err := uuid.Parse(r.FormValue("staff_id"))
	if err != nil {
		http.Redirect(w, r, "/portal/schedule?error=Invalid+staff", http.StatusSeeOther)
		return
	}
	loc, _ := time.LoadLocation("Asia/Jakarta")
	if loc == nil {
		loc = time.FixedZone("WIB", 7*3600)
	}
	startAt, err1 := time.ParseInLocation("2006-01-02T15:04", r.FormValue("start_at"), loc)
	endAt, err2 := time.ParseInLocation("2006-01-02T15:04", r.FormValue("end_at"), loc)
	if err1 != nil || err2 != nil {
		http.Redirect(w, r, "/portal/schedule?error=Invalid+time+range", http.StatusSeeOther)
		return
	}
	to := &domain.StaffTimeOff{
		StaffID: staffID,
		StartAt: startAt.UTC(),
		EndAt:   endAt.UTC(),
		Reason:  r.FormValue("reason"),
	}
	if err := s.catalog.CreateStaffTimeOff(r.Context(), to); err != nil {
		http.Redirect(w, r, "/portal/schedule?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/schedule?success=Time-off+blockout+added", http.StatusSeeOther)
}

func (s *Server) PortalDeleteTimeOff(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	_ = s.catalog.DeleteStaffTimeOff(r.Context(), id)
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Time-off removed")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/schedule?success=Time-off+removed", http.StatusSeeOther)
}

func (s *Server) PortalServices(w http.ResponseWriter, r *http.Request) {
	locs, _ := s.catalog.ListLocations(r.Context(), false)
	var all []domain.Service
	for _, l := range locs {
		svcs, _ := s.catalog.ListServices(r.Context(), l.ID, false)
		all = append(all, svcs...)
	}
	summaries := make(map[string]string, len(all))
	for _, svc := range all {
		hours, _ := s.catalog.ListServiceHours(r.Context(), svc.ID)
		summaries[svc.ID.String()] = service.SummarizeServiceHours(hours, i18n.Resolve(r))
	}
	s.render(w, r, "portal_services.html", pageData{
		"Locations": locs,
		"Services":  all,
		"Hours":     summaries,
		"Error":     r.URL.Query().Get("error"),
	})
}

func (s *Server) PortalCreateService(w http.ResponseWriter, r *http.Request) {
	svc := &domain.Service{Active: true}
	hours, errMsg := s.parseServiceForm(r, svc, true)
	if errMsg != "" {
		if htmx.IsRequest(r) {
			s.toast(w, "error", errMsg)
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/portal/services?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	if err := s.catalog.SaveService(r.Context(), svc); err != nil {
		if htmx.IsRequest(r) {
			s.toast(w, "error", err.Error())
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/portal/services?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	_ = s.catalog.SaveServiceHours(r.Context(), svc.ID, hours)
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Service added")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/services?toast="+url.QueryEscape("Service added")+"&kind=success", http.StatusSeeOther)
}

// storeUpload validates and stores an uploaded picture under
// <UploadDir>/<subdir>, returning the relative path or an error message.
func (s *Server) storeUpload(file io.Reader, closer io.Closer, original string, size int64, subdir string) (string, string) {
	defer closer.Close()
	stored, err := service.ValidatePhotoName(original, size)
	if err != nil {
		return "", err.Error()
	}
	dir := filepath.Join(s.cfg.UploadDir, subdir)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return "", "Could not store photo"
	}
	dst, err := os.Create(filepath.Join(dir, stored))
	if err != nil {
		s.log.Error("photo save", "err", err)
		return "", "Could not store photo"
	}
	if _, err := io.Copy(dst, file); err != nil {
		_ = dst.Close()
		return "", "Could not store photo"
	}
	_ = dst.Close()
	return subdir + "/" + stored, ""
}
func (s *Server) parseServiceForm(r *http.Request, svc *domain.Service, withLocation bool) ([]domain.ServiceHours, string) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			return nil, "Could not read form"
		}
	}
	if !s.requireCSRF(r) {
		return nil, "invalid request"
	}
	if withLocation {
		lid, _ := uuid.Parse(r.FormValue("location_id"))
		svc.LocationID = lid
	}
	dur, _ := strconv.Atoi(r.FormValue("duration_min"))
	price, _ := strconv.ParseInt(r.FormValue("price_idr"), 10, 64)
	svc.Name = r.FormValue("name")
	svc.NameID = strings.TrimSpace(r.FormValue("name_id"))
	svc.Description = r.FormValue("description")
	svc.DescriptionID = strings.TrimSpace(r.FormValue("description_id"))
	svc.DurationMin = dur
	svc.PriceIDR = price
	svc.ImageURL = strings.TrimSpace(r.FormValue("image_url"))
	if r.FormValue("remove_photo") == "1" && svc.ImagePath != "" {
		_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.Clean(svc.ImagePath)))
		svc.ImagePath = ""
	}
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "services")
		if msg != "" {
			return nil, msg
		}
		svc.ImagePath = rel
	}
	var hours []domain.ServiceHours
	for d := 0; d < 7; d++ {
		from := strings.TrimSpace(r.FormValue(fmt.Sprintf("hours_%d_from", d)))
		to := strings.TrimSpace(r.FormValue(fmt.Sprintf("hours_%d_to", d)))
		if from == "" || to == "" {
			continue // closed this weekday
		}
		hours = append(hours, domain.ServiceHours{Weekday: d, StartTime: from, EndTime: to})
	}
	return hours, ""
}

type weekdayRow struct {
	Num  int
	Name string
	From string
	To   string
}

// weekdayNames returns full weekday names (Sunday-first) in the request language.
func weekdayNames(lang string) []string {
	names := make([]string, 7)
	for d := 0; d < 7; d++ {
		names[d] = i18n.T(lang, fmt.Sprintf("p.day.%d", d))
	}
	return names
}

func weekdayRows(hours []domain.ServiceHours, lang string) []weekdayRow {
	names := weekdayNames(lang)
	byDay := map[int]domain.ServiceHours{}
	for _, h := range hours {
		byDay[h.Weekday] = h
	}
	// Monday-first order for the form grid.
	order := []int{1, 2, 3, 4, 5, 6, 0}
	rows := make([]weekdayRow, 0, 7)
	for _, d := range order {
		rows = append(rows, weekdayRow{Num: d, Name: names[d], From: byDay[d].StartTime, To: byDay[d].EndTime})
	}
	return rows
}

func (s *Server) PortalEditServiceForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	svc, err := s.catalog.GetService(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hours, _ := s.catalog.ListServiceHours(r.Context(), id)
	s.render(w, r, "portal_service_edit.html", pageData{
		"Service": svc,
		"Hours":   weekdayRows(hours, i18n.Resolve(r)),
		"Error":   r.URL.Query().Get("error"),
	})
}

func (s *Server) PortalUpdateService(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	svc, err := s.catalog.GetService(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hours, errMsg := s.parseServiceForm(r, svc, false)
	if errMsg != "" {
		http.Redirect(w, r, "/portal/services/"+id.String()+"/edit?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	if err := s.catalog.SaveService(r.Context(), svc); err != nil {
		http.Redirect(w, r, "/portal/services/"+id.String()+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if err := s.catalog.SaveServiceHours(r.Context(), svc.ID, hours); err != nil {
		http.Redirect(w, r, "/portal/services/"+id.String()+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/services?toast="+url.QueryEscape("Service updated")+"&kind=success", http.StatusSeeOther)
}

// ServeServicePhoto serves uploaded service photos (public, traversal-safe).
func (s *Server) ServeServicePhoto(w http.ResponseWriter, r *http.Request) {
	file := chi.URLParam(r, "file")
	if file == "" || strings.Contains(file, "/") || strings.Contains(file, "\\") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.UploadDir, "services", filepath.Clean(file)))
}

func (s *Server) PortalToggleService(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err == nil {
		_ = s.catalog.ToggleServiceActive(r.Context(), id)
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Service availability updated")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/services", http.StatusSeeOther)
}

func (s *Server) PortalDeleteService(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.catalog.DeleteService(r.Context(), id); err != nil {
		if htmx.IsRequest(r) {
			s.toast(w, "error", err.Error())
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/portal/services?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Service removed")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/services?toast="+url.QueryEscape("Service removed")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) PortalToggleStaff(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err == nil {
		_ = s.catalog.ToggleStaffActive(r.Context(), id)
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Therapist status updated")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/schedule", http.StatusSeeOther)
}

func (s *Server) PortalEditStaffForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.catalog.GetStaff(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	svcs, _ := s.catalog.ListServices(r.Context(), st.LocationID, false)
	current, _ := s.catalog.StaffServiceIDs(r.Context(), id)
	have := make(map[string]bool, len(current))
	for _, sid := range current {
		have[sid.String()] = true
	}
	s.render(w, r, "portal_staff_edit.html", pageData{
		"Staff": st, "Services": svcs, "Have": have,
		"Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) PortalUpdateStaff(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			http.Error(w, "invalid", http.StatusBadRequest)
			return
		}
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.catalog.GetStaff(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var serviceIDs []uuid.UUID
	for _, v := range r.Form["service_ids"] {
		if sid, err := uuid.Parse(v); err == nil {
			serviceIDs = append(serviceIDs, sid)
		}
	}
	st.Name = r.FormValue("name")
	st.Bio = r.FormValue("bio")
	st.PhotoURL = strings.TrimSpace(r.FormValue("photo_url"))
	if r.FormValue("remove_photo") == "1" && st.PhotoPath != "" {
		_ = os.Remove(filepath.Join(s.cfg.UploadDir, filepath.Clean(st.PhotoPath)))
		st.PhotoPath = ""
	}
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "staff")
		if msg != "" {
			http.Redirect(w, r, "/portal/staff/"+id.String()+"/edit?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		st.PhotoPath = rel
	}
	if err := s.catalog.SaveStaff(r.Context(), st, serviceIDs); err != nil {
		http.Redirect(w, r, "/portal/staff/"+id.String()+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/schedule?toast="+url.QueryEscape("Therapist updated")+"&kind=success", http.StatusSeeOther)
}

// ServeStaffPhoto serves uploaded therapist photos (public, traversal-safe).
func (s *Server) ServeStaffPhoto(w http.ResponseWriter, r *http.Request) {
	file := chi.URLParam(r, "file")
	if file == "" || strings.Contains(file, "/") || strings.Contains(file, "\\") {
		http.NotFound(w, r)
		return
	}
	http.ServeFile(w, r, filepath.Join(s.cfg.UploadDir, "staff", filepath.Clean(file)))
}

func (s *Server) PortalDeleteStaff(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if err := s.catalog.DeleteStaff(r.Context(), id); err != nil {
		if htmx.IsRequest(r) {
			s.toast(w, "error", err.Error())
			w.WriteHeader(http.StatusUnprocessableEntity)
			return
		}
		http.Redirect(w, r, "/portal/schedule?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	if htmx.IsRequest(r) {
		s.toast(w, "success", "Therapist removed")
		htmx.Refresh(w)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, "/portal/schedule?toast="+url.QueryEscape("Therapist removed")+"&kind=success", http.StatusSeeOther)
}

func (s *Server) PortalStaffHoursForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	st, err := s.catalog.GetStaff(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	hours, _ := s.catalog.StaffHours(r.Context(), id)
	rows := make([]weekdayRow, 0, 7)
	names := weekdayNames(i18n.Resolve(r))
	byDay := map[int]domain.StaffHours{}
	for _, h := range hours {
		byDay[h.Weekday] = h
	}
	for _, d := range []int{1, 2, 3, 4, 5, 6, 0} {
		rows = append(rows, weekdayRow{Num: d, Name: names[d], From: byDay[d].StartTime, To: byDay[d].EndTime})
	}
	s.render(w, r, "portal_staff_hours.html", pageData{
		"Staff": st,
		"Hours": rows,
		"Error": r.URL.Query().Get("error"),
	})
}

func (s *Server) PortalSaveStaffHours(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	var hours []domain.StaffHours
	for d := 0; d < 7; d++ {
		from := strings.TrimSpace(r.FormValue(fmt.Sprintf("hours_%d_from", d)))
		to := strings.TrimSpace(r.FormValue(fmt.Sprintf("hours_%d_to", d)))
		if from == "" || to == "" {
			continue // closed this weekday
		}
		hours = append(hours, domain.StaffHours{Weekday: d, StartTime: from, EndTime: to})
	}
	if err := s.catalog.SaveStaffHours(r.Context(), id, hours); err != nil {
		st, _ := s.catalog.GetStaff(r.Context(), id)
		names := weekdayNames(i18n.Resolve(r))
		byDay := map[int]domain.StaffHours{}
		for _, h := range hours {
			byDay[h.Weekday] = h
		}
		rows := make([]weekdayRow, 0, 7)
		for _, d := range []int{1, 2, 3, 4, 5, 6, 0} {
			rows = append(rows, weekdayRow{Num: d, Name: names[d], From: byDay[d].StartTime, To: byDay[d].EndTime})
		}
		_ = st
		s.render(w, r, "portal_staff_hours.html", pageData{
			"Staff": st, "Hours": rows, "Error": err.Error(),
		})
		return
	}
	http.Redirect(w, r, "/portal/schedule?toast="+url.QueryEscape("Working hours saved")+"&kind=success", http.StatusSeeOther)
}

// Admin CS & Password Handlers
func (s *Server) AdminCSList(w http.ResponseWriter, r *http.Request) {
	csUsers, err := s.auth.ListCS(r.Context())
	if err != nil {
		http.Error(w, "failed to load CS accounts", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_cs.html", pageData{
		"CSUsers": csUsers,
		"Success": r.URL.Query().Get("success"),
		"Error":   r.URL.Query().Get("error"),
	})
}

func (s *Server) AdminCreateCS(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	name := r.FormValue("full_name")
	email := r.FormValue("email")
	phone := r.FormValue("phone")
	pass := r.FormValue("password")
	_, err := s.auth.CreateCS(r.Context(), email, pass, name, phone)
	if err != nil {
		http.Redirect(w, r, "/admin/cs?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/cs?success="+url.QueryEscape("CS account created for "+name), http.StatusSeeOther)
}

func (s *Server) AdminToggleCSActive(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	user, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	newActive := !user.IsActive
	_ = s.auth.SetUserActive(r.Context(), id, newActive)
	msg := "CS account reactivated"
	if !newActive {
		msg = "CS account deactivated and active sessions terminated"
	}
	http.Redirect(w, r, "/admin/cs?success="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) AdminResetCSPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pass := r.FormValue("password")
	if err := s.auth.ResetPassword(r.Context(), id, pass); err != nil {
		http.Redirect(w, r, "/admin/cs?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/admin/cs?success="+url.QueryEscape("Password reset successfully"), http.StatusSeeOther)
}

func (s *Server) AdminCustomerResetForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	cust, err := s.auth.GetUser(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "admin_customer_reset.html", pageData{
		"Customer": cust,
		"Success":  r.URL.Query().Get("success"),
		"Error":    r.URL.Query().Get("error"),
	})
}

func (s *Server) AdminPasswordForm(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login?next=/admin/password", http.StatusSeeOther)
		return
	}
	s.render(w, r, "admin_password.html", pageData{
		"Email":   u.Email,
		"Success": r.URL.Query().Get("success"),
		"Error":   r.URL.Query().Get("error"),
	})
}

func (s *Server) AdminChangePassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Redirect(w, r, "/login?next=/admin/password", http.StatusSeeOther)
		return
	}
	fail := func(msg string) {
		http.Redirect(w, r, "/admin/password?error="+url.QueryEscape(msg), http.StatusSeeOther)
	}
	if !auth.CheckPassword(u.PasswordHash, r.FormValue("current_password")) {
		fail("Current password is incorrect")
		return
	}
	newPass := r.FormValue("new_password")
	if newPass != r.FormValue("confirm_password") {
		fail("New passwords do not match")
		return
	}
	if err := s.auth.ResetPassword(r.Context(), u.ID, newPass); err != nil {
		fail(err.Error())
		return
	}
	http.Redirect(w, r, "/login?success="+url.QueryEscape("Password changed. Please log in again."), http.StatusSeeOther)
}

func (s *Server) AdminResetCustomerPassword(w http.ResponseWriter, r *http.Request) {
	if err := r.ParseForm(); err != nil || !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	pass := r.FormValue("password")
	if err := s.auth.ResetPassword(r.Context(), id, pass); err != nil {
		http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s/reset-password?error=%s", id, url.QueryEscape(err.Error())), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, fmt.Sprintf("/admin/customers/%s/reset-password?success=%s", id, url.QueryEscape("Customer password updated successfully")), http.StatusSeeOther)
}
