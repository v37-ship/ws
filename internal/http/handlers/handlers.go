package handlers

import (
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
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
	s.render(w, r, "home.html", pageData{"Locations": locs})
}

func (s *Server) LoginForm(w http.ResponseWriter, r *http.Request) {
	s.render(w, r, "login.html", pageData{"Next": r.URL.Query().Get("next")})
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
		if u.Role == domain.RoleAdmin {
			next = "/admin"
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
	locs, err := s.catalog.ListLocations(r.Context(), true)
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	data := pageData{
		"Locations": locs,
		"LocationID": r.URL.Query().Get("location_id"),
		"Date": time.Now().In(time.FixedZone("WIB", 7*3600)).Format("2006-01-02"),
	}
	if lid, err := uuid.Parse(r.URL.Query().Get("location_id")); err == nil {
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
	slots, err := s.avail.Slots(r.Context(), lid, sid, staffID, day)
	if err != nil {
		s.renderPartial(w, r, "slots_partial.html", pageData{"SlotsError": err.Error()})
		return
	}
	s.renderPartial(w, r, "slots_partial.html", pageData{"Slots": slots})
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
	b, _, err := s.book.CreatePending(r.Context(), u.ID, lid, sid, staffID, startAt, r.FormValue("notes"))
	if err != nil {
		if errors.Is(err, service.ErrConflict) {
			http.Error(w, "That slot was just taken. Pick another time.", http.StatusConflict)
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
	s.render(w, r, "bookings.html", pageData{"Bookings": list})
}

func (s *Server) PayPage(w http.ResponseWriter, r *http.Request) {
	u := middleware.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := s.book.GetBooking(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if b.CustomerID != u.ID && u.Role != domain.RoleAdmin {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	p, _ := s.book.GetPaymentForBooking(r.Context(), id)
	data := pageData{
		"Booking":  b,
		"Payment":  p,
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
	u := middleware.UserFrom(r.Context())
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	b, err := s.book.GetBooking(r.Context(), id)
	if err != nil || (b.CustomerID != u.ID && u.Role != domain.RoleAdmin) {
		http.Error(w, "forbidden", http.StatusForbidden)
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
	if err := s.book.Cancel(r.Context(), id, u.ID, u.Role == domain.RoleAdmin); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/bookings", http.StatusSeeOther)
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
	if err := s.book.ConfirmMockPayment(r.Context(), id, u.ID); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/bookings/"+id.String()+"/pay", http.StatusSeeOther)
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

func (s *Server) AdminServices(w http.ResponseWriter, r *http.Request) {
	locs, _ := s.catalog.ListLocations(r.Context(), false)
	var all []domain.Service
	for _, l := range locs {
		svcs, _ := s.catalog.ListServices(r.Context(), l.ID, false)
		all = append(all, svcs...)
	}
	s.render(w, r, "admin_services.html", pageData{"Locations": locs, "Services": all})
}

func (s *Server) AdminCreateService(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	lid, _ := uuid.Parse(r.FormValue("location_id"))
	dur, _ := strconv.Atoi(r.FormValue("duration_min"))
	price, _ := strconv.ParseInt(r.FormValue("price_idr"), 10, 64)
	svc := &domain.Service{
		LocationID: lid, Name: r.FormValue("name"), Description: r.FormValue("description"),
		DurationMin: dur, PriceIDR: price, Active: true,
	}
	if err := s.catalog.SaveService(r.Context(), svc); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/services", http.StatusSeeOther)
}

func (s *Server) AdminStaff(w http.ResponseWriter, r *http.Request) {
	locs, _ := s.catalog.ListLocations(r.Context(), false)
	var staff []domain.Staff
	var services []domain.Service
	for _, l := range locs {
		st, _ := s.catalog.ListStaff(r.Context(), l.ID, false)
		staff = append(staff, st...)
		svcs, _ := s.catalog.ListServices(r.Context(), l.ID, true)
		services = append(services, svcs...)
	}
	s.render(w, r, "admin_staff.html", pageData{"Locations": locs, "Staff": staff, "Services": services})
}

func (s *Server) AdminCreateStaff(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
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
	st := &domain.Staff{LocationID: lid, Name: r.FormValue("name"), Bio: r.FormValue("bio"), Active: true}
	if err := s.catalog.SaveStaff(r.Context(), st, serviceIDs); err != nil {
		http.Error(w, err.Error(), http.StatusBadRequest)
		return
	}
	http.Redirect(w, r, "/admin/staff", http.StatusSeeOther)
}

func (s *Server) AdminStaffHours(w http.ResponseWriter, r *http.Request) {
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
	var b strings.Builder
	for _, h := range hours {
		b.WriteString(strconv.Itoa(h.Weekday) + " " + h.StartTime + " " + h.EndTime + "\n")
	}
	s.render(w, r, "admin_staff_hours.html", pageData{"Staff": st, "HoursText": b.String()})
}

func (s *Server) AdminSaveStaffHours(w http.ResponseWriter, r *http.Request) {
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
	for _, line := range strings.Split(r.FormValue("hours"), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		parts := strings.Fields(line)
		if len(parts) != 3 {
			continue
		}
		wd, _ := strconv.Atoi(parts[0])
		hours = append(hours, domain.StaffHours{Weekday: wd, StartTime: parts[1], EndTime: parts[2]})
	}
	if err := s.catalog.SaveStaffHours(r.Context(), id, hours); err != nil {
		st, _ := s.catalog.GetStaff(r.Context(), id)
		s.render(w, r, "admin_staff_hours.html", pageData{"Staff": st, "HoursText": r.FormValue("hours"), "Error": err.Error()})
		return
	}
	http.Redirect(w, r, "/admin/staff", http.StatusSeeOther)
}

func (s *Server) AdminBookings(w http.ResponseWriter, r *http.Request) {
	list, err := s.book.ListAdmin(r.Context())
	if err != nil {
		http.Error(w, "error", http.StatusInternalServerError)
		return
	}
	s.render(w, r, "admin_bookings.html", pageData{"Bookings": list})
}