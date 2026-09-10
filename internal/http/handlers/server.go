package handlers

import (
	"bytes"
	"embed"
	"errors"
	"fmt"
	"html/template"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"spa-booking/internal/auth"
	"spa-booking/internal/config"
	"spa-booking/internal/domain"
	"spa-booking/internal/http/htmx"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
)

//go:embed all:templates
var templateFS embed.FS

type Server struct {
	cfg        config.Config
	log        *slog.Logger
	auth       *service.AuthService
	catalog    *service.CatalogService
	avail      *service.AvailabilityService
	book       *service.BookingService
	tpl        *template.Template
	csrfSecure bool
}

func New(
	cfg config.Config,
	log *slog.Logger,
	authSvc *service.AuthService,
	catalog *service.CatalogService,
	avail *service.AvailabilityService,
	book *service.BookingService,
) (*Server, error) {
	funcs := template.FuncMap{
		"formatIDR": func(n int64) string {
			s := fmt.Sprintf("%d", n)
			var b strings.Builder
			for i, c := range s {
				if i > 0 && (len(s)-i)%3 == 0 {
					b.WriteByte('.')
				}
				b.WriteRune(c)
			}
			return "Rp " + b.String()
		},
		"formatTime": func(t time.Time, layout string) string {
			if t.IsZero() {
				return "-"
			}
			return t.In(time.FixedZone("WIB", 7*3600)).Format(layout)
		},
		"csrf": func() string { return "" }, // overridden per render
		"eq":   func(a, b any) bool { return fmt.Sprintf("%v", a) == fmt.Sprintf("%v", b) },
		"hasUUID": func(list []string, id string) bool {
			for _, x := range list {
				if x == id {
					return true
				}
			}
			return false
		},
		"dict": func(values ...any) (map[string]any, error) {
			if len(values)%2 != 0 {
				return nil, errors.New("invalid dict call")
			}
			dict := make(map[string]any, len(values)/2)
			for i := 0; i < len(values); i += 2 {
				key, ok := values[i].(string)
				if !ok {
					return nil, errors.New("dict keys must be strings")
				}
				dict[key] = values[i+1]
			}
			return dict, nil
		},
		"waURL": func(phone, customerName, serviceName, staffName, whenStr, locName string) string {
			cleanPhone := strings.TrimSpace(phone)
			cleanPhone = strings.ReplaceAll(cleanPhone, "-", "")
			cleanPhone = strings.ReplaceAll(cleanPhone, " ", "")
			cleanPhone = strings.ReplaceAll(cleanPhone, "+", "")
			if strings.HasPrefix(cleanPhone, "0") {
				cleanPhone = "62" + cleanPhone[1:]
			}
			msg := fmt.Sprintf("Halo Bunda %s! Konfirmasi booking di Wangga: Baby & Mom Spa:\n\n✨ Layanan: %s\n👩‍⚕️ Bidan/Terapis: %s\n📅 Waktu: %s\n📍 Lokasi: %s\n\nSampai jumpa di Wangga Spa! Ada yang ingin ditanyakan sebelumnya, Bunda? 🥰",
				customerName, serviceName, staffName, whenStr, locName)
			return "https://wa.me/" + cleanPhone + "?text=" + url.QueryEscape(msg)
		},
	}
	root := template.New("root").Funcs(funcs)
	tpl, err := root.ParseFS(
		templateFS,
		"templates/layouts/*.html",
		"templates/pages/*.html",
		"templates/partials/*.html",
		"templates/admin/*.html",
		"templates/portal/*.html",
	)
	if err != nil {
		return nil, err
	}
	return &Server{
		cfg: cfg, log: log, auth: authSvc, catalog: catalog, avail: avail, book: book,
		tpl: tpl, csrfSecure: cfg.CSRFSecure,
	}, nil
}

type pageData map[string]any

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	if data == nil {
		data = pageData{}
	}
	data["User"] = middleware.UserFrom(r.Context())
	token := s.ensureCSRF(w, r)
	data["CSRF"] = token
	data["Request"] = r
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write(buf.Bytes())
}

func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	if data == nil {
		data = pageData{}
	}
	data["User"] = middleware.UserFrom(r.Context())
	data["CSRF"] = s.ensureCSRF(w, r)
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("partial", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Vary", "HX-Request")
	_, _ = w.Write(buf.Bytes())
}

// renderAuto serves a fragment for HTMX requests and a full page otherwise.
// Use for filter/search endpoints that support both hx-get and full loads.
func (s *Server) renderAuto(w http.ResponseWriter, r *http.Request, partial, page string, data pageData) {
	if isHTMX(r) {
		s.renderPartial(w, r, partial, data)
		return
	}
	s.render(w, r, page, data)
}

func (s *Server) toast(w http.ResponseWriter, kind, message string) {
	htmx.Toast(w, kind, message)
}

func (s *Server) redirectHTMX(w http.ResponseWriter, r *http.Request, to string) {
	if isHTMX(r) {
		htmx.Redirect(w, to)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, to, http.StatusSeeOther)
}

func (s *Server) ensureCSRF(w http.ResponseWriter, r *http.Request) string {
	if c, err := r.Cookie("csrf"); err == nil && c.Value != "" {
		return c.Value
	}
	tok, err := auth.NewCSRFToken()
	if err != nil {
		return ""
	}
	http.SetCookie(w, &http.Cookie{
		Name: "csrf", Value: tok, Path: "/", HttpOnly: false, SameSite: http.SameSiteLaxMode,
		Secure: s.csrfSecure, MaxAge: int((24 * time.Hour).Seconds()),
	})
	return tok
}

func (s *Server) requireCSRF(r *http.Request) bool {
	c, err := r.Cookie("csrf")
	if err != nil {
		return false
	}
	form := r.FormValue("csrf")
	return form != "" && subtleConstantTimeEq(form, c.Value)
}

func subtleConstantTimeEq(a, b string) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := 0; i < len(a); i++ {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

func (s *Server) setSessionCookie(w http.ResponseWriter, sessionID string) {
	http.SetCookie(w, &http.Cookie{
		Name: "session", Value: sessionID, Path: "/", HttpOnly: true,
		SameSite: http.SameSiteLaxMode, Secure: s.csrfSecure,
		MaxAge: int(s.cfg.SessionTTL.Seconds()),
	})
}

func (s *Server) clearSessionCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{Name: "session", Value: "", Path: "/", MaxAge: -1})
}

func isHTMX(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

func StaticHandler() http.Handler {
	sub, err := fs.Sub(staticFS, "static")
	if err != nil {
		panic(err)
	}
	return http.FileServer(http.FS(sub))
}

//go:embed all:static
var staticFS embed.FS

// Routes registers application routes on the chi router.
func (s *Server) Routes(r chi.Router) {
	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ok"))
	})
	r.Get("/readyz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("ready"))
	})
	r.Handle("/static/*", http.StripPrefix("/static/", StaticHandler()))

	loginLimiter := middleware.NewRateLimiter(20, time.Minute)
	bookLimiter := middleware.NewRateLimiter(30, time.Minute)

	r.Get("/", s.Home)
	r.Get("/login", s.LoginForm)
	r.With(middleware.RateLimit(loginLimiter, func(req *http.Request) string {
		return "login:" + req.RemoteAddr
	})).Post("/login", s.Login)
	r.Get("/register", s.RegisterForm)
	r.With(middleware.RateLimit(loginLimiter, func(req *http.Request) string {
		return "register:" + req.RemoteAddr
	})).Post("/register", s.Register)
	r.Post("/logout", s.Logout)

	r.Get("/book", s.BookPage)
	r.Get("/book/services", s.BookServices)
	r.Get("/book/staff", s.BookStaff)
	r.Get("/book/slots", s.BookSlots)
	r.With(middleware.RequireAuth, middleware.RateLimit(bookLimiter, func(req *http.Request) string {
		return "book:" + req.RemoteAddr
	})).Post("/book", s.CreateBooking)

	r.With(middleware.RequireAuth).Get("/bookings", s.MyBookings)
	r.With(middleware.RequireAuth).Get("/bookings/{id}/pay", s.PayPage)
	r.With(middleware.RequireAuth).Get("/bookings/{id}/status", s.BookingStatusPartial)
	r.With(middleware.RequireAuth).Post("/bookings/{id}/cancel", s.CancelBooking)
	r.With(middleware.RequireAuth).Post("/bookings/{id}/mock-pay", s.MockPay)

	r.Post("/webhooks/midtrans", s.MidtransWebhook)

	// Staff Portal (Accessible by CS and Admin)
	r.Route("/portal", func(pr chi.Router) {
		pr.Use(middleware.RequireStaff)
		pr.Get("/", s.PortalDashboard)
		pr.Get("/queue", s.PortalQueuePartial)
		pr.Get("/bookings", s.PortalBookings)
		pr.Get("/bookings/new", s.PortalNewBookingForm)
		pr.Post("/bookings/new", s.PortalCreateBooking)
		pr.Get("/bookings/{id}", s.PortalBookingDetail)
		pr.Post("/bookings/{id}/status", s.PortalUpdateBookingStatus)
		pr.Post("/bookings/{id}/reschedule", s.PortalRescheduleBooking)

		pr.Get("/customers", s.PortalCustomers)
		pr.Get("/customers/new", s.PortalNewCustomerForm)
		pr.Post("/customers/new", s.PortalCreateCustomer)
		pr.Get("/customers/{id}", s.PortalCustomerDetail)

		pr.Get("/schedule", s.PortalSchedule)
		pr.Post("/schedule/time-off", s.PortalCreateTimeOff)
		pr.Post("/schedule/time-off/{id}/delete", s.PortalDeleteTimeOff)

		pr.Get("/services", s.PortalServices)
		pr.Post("/services/{id}/toggle", s.PortalToggleService)
		pr.Post("/staff/{id}/toggle", s.PortalToggleStaff)
	})

	// Admin Only Routes
	r.Route("/admin", func(ar chi.Router) {
		ar.Use(middleware.RequireAdmin)
		ar.Get("/", s.AdminHome)
		ar.Get("/locations", s.AdminLocations)
		ar.Post("/locations", s.AdminCreateLocation)
		ar.Get("/services", s.AdminServices)
		ar.Post("/services", s.AdminCreateService)
		ar.Get("/staff", s.AdminStaff)
		ar.Post("/staff", s.AdminCreateStaff)
		ar.Get("/staff/{id}/hours", s.AdminStaffHours)
		ar.Post("/staff/{id}/hours", s.AdminSaveStaffHours)
		ar.Get("/bookings", s.AdminBookings)

		// CS Accounts Management
		ar.Get("/cs", s.AdminCSList)
		ar.Post("/cs", s.AdminCreateCS)
		ar.Post("/cs/{id}/toggle-active", s.AdminToggleCSActive)
		ar.Post("/cs/{id}/reset-password", s.AdminResetCSPassword)

		// Customer password reset
		ar.Get("/customers/{id}/reset-password", s.AdminCustomerResetForm)
		ar.Post("/customers/{id}/reset-password", s.AdminResetCustomerPassword)
	})
}

var (
	_ = domain.RoleCustomer
	_ = strings.TrimSpace
	_ = chimw.RequestID
)
