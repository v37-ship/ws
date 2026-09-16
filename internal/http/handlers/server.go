package handlers

import (
	"bytes"
	"crypto/sha256"
	"embed"
	"encoding/hex"
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
	"spa-booking/internal/i18n"
	"spa-booking/internal/service"

	"github.com/go-chi/chi/v5"
	chimw "github.com/go-chi/chi/v5/middleware"
	"github.com/google/uuid"
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
	reports    *service.ReportService
	career     *service.CareerService
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
	reports *service.ReportService,
	career *service.CareerService,
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
		"waInquire": func(serviceName, locName string) string {
			msg := fmt.Sprintf("Halo Wangga: Baby & Mom Spa! Saya ingin bertanya tentang layanan %s di %s. Apakah masih tersedia?", serviceName, locName)
			return "https://wa.me/?text=" + url.QueryEscape(msg)
		},
		"waLink": func(phone, msg string) string {
			digits := strings.Map(func(r rune) rune {
				if r >= '0' && r <= '9' {
					return r
				}
				return -1
			}, phone)
			return "https://wa.me/" + digits + "?text=" + url.QueryEscape(msg)
		},
		"serviceImg": func(name string) string {
			return service.KeywordServiceImage(name)
		},
		"servicePhoto": func(svc domain.Service) string {
			return service.ResolveServiceImage(svc)
		},
		"static": func(path string) string {
			return "/static/" + strings.TrimPrefix(path, "/") + "?v=" + staticVersion
		},
		"t": func(lang any, key string) string {
			return i18n.T(fmt.Sprintf("%v", lang), key)
		},
		"staffPhoto": func(st domain.Staff) string {
			return service.ResolveStaffPhoto(st)
		},
		"navParts": func(s string) []string {
			if i := strings.Index(s, " "); i > 0 {
				return []string{s[:i], strings.TrimSpace(s[i+1:])}
			}
			return []string{"", s}
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
		cfg: cfg, log: log, auth: authSvc, catalog: catalog, avail: avail, book: book, reports: reports, career: career,
		tpl: tpl, csrfSecure: cfg.CSRFSecure,
	}, nil
}

type pageData map[string]any

func (s *Server) render(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	if data == nil {
		data = pageData{}
	}
	// ?lang= override wins and persists; otherwise resolve cookie/Accept-Language.
	if lang, ok := i18n.QueryOverride(r); ok {
		i18n.SetCookie(w, lang, s.csrfSecure)
		data["Lang"] = lang
	} else {
		data["Lang"] = i18n.Resolve(r)
	}
	data["User"] = middleware.UserFrom(r.Context())
	token := s.ensureCSRF(w, r)
	data["CSRF"] = token
	data["Request"] = r
	// Portal/admin pages containing portal_nav get the sticky-header hamburger.
	if _, ok := data["HasDrawer"]; !ok {
		data["HasDrawer"] = strings.HasPrefix(name, "portal_") || name == "admin_cs.html"
	}
	data["Contact"] = pageData{
		"WhatsApp": s.cfg.ContactWhatsApp,
		"Email":    s.cfg.ContactEmail,
		"Address":  s.cfg.ContactAddress,
		"Hours":    s.cfg.ContactHours,
		"Maps":     s.cfg.ContactMapsURL,
	}
	// Umami tracker config; head.html renders the snippet only when both set.
	data["Analytics"] = pageData{
		"URL":    s.cfg.AnalyticsURL,
		"SiteID": s.cfg.AnalyticsSiteID,
	}
	var buf bytes.Buffer
	if err := s.tpl.ExecuteTemplate(&buf, name, data); err != nil {
		s.log.Error("template", "name", name, "err", err)
		http.Error(w, "template error", http.StatusInternalServerError)
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(buf.Bytes())
}

// SetLanguage persists the EN/ID choice and redirects back.
func (s *Server) SetLanguage(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	lang := r.FormValue("lang")
	if lang == "" {
		lang = r.URL.Query().Get("lang")
	}
	if lang != i18n.EN && lang != i18n.ID {
		// Toggle when no explicit valid value.
		if i18n.Resolve(r) == i18n.EN {
			lang = i18n.ID
		} else {
			lang = i18n.EN
		}
	}
	i18n.SetCookie(w, lang, s.csrfSecure)
	back := r.FormValue("next")
	if back == "" {
		back = sameOriginPath(r, r.Referer())
	}
	back = stripLangParam(back)
	if back == "" || !strings.HasPrefix(back, "/") {
		back = "/"
	}
	if isHTMX(r) {
		htmx.Redirect(w, back)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	http.Redirect(w, r, back, http.StatusSeeOther)
}

// sameOriginPath returns path[?query] for a same-origin Referer, else "".
// Browsers send absolute Referers; accept them only when the host matches.
func sameOriginPath(r *http.Request, ref string) string {
	if ref == "" {
		return ""
	}
	if strings.HasPrefix(ref, "/") && !strings.HasPrefix(ref, "//") {
		return ref
	}
	u, err := url.Parse(ref)
	if err != nil || u.Host == "" {
		return ""
	}
	host := r.Host
	if host == "" {
		host = r.URL.Host
	}
	if u.Host != host {
		return ""
	}
	out := u.Path
	if out == "" {
		out = "/"
	}
	if u.RawQuery != "" {
		out += "?" + u.RawQuery
	}
	return out
}

// stripLangParam drops a stale ?lang= so it can't override the fresh cookie.
func stripLangParam(target string) string {
	i := strings.Index(target, "?")
	if i < 0 {
		return target
	}
	q, err := url.ParseQuery(target[i+1:])
	if err != nil {
		return target[:i]
	}
	q.Del("lang")
	enc := q.Encode()
	if enc == "" {
		return target[:i]
	}
	return target[:i] + "?" + enc
}

func (s *Server) renderPartial(w http.ResponseWriter, r *http.Request, name string, data pageData) {
	if data == nil {
		data = pageData{}
	}
	if _, ok := data["Lang"]; !ok {
		if lang, ok := i18n.QueryOverride(r); ok {
			i18n.SetCookie(w, lang, s.csrfSecure)
			data["Lang"] = lang
		} else {
			data["Lang"] = i18n.Resolve(r)
		}
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
	w.Header().Set("Cache-Control", "no-store")
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

// requirePortalAdmin enforces admin-only corrections inside the shared
// staff portal (all /portal routes admit CS too, so the check is in-handler).
func (s *Server) requirePortalAdmin(r *http.Request) (*uuid.UUID, bool) {
	u := middleware.UserFrom(r.Context())
	if u == nil || u.Role != domain.RoleAdmin {
		return nil, false
	}
	return &u.ID, true
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
	files := http.FileServer(http.FS(sub))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Versioned URLs (?v=content-hash) are immutable: any content
		// change produces a new URL, so browsers never run stale CSS/JS.
		if r.URL.Query().Get("v") != "" {
			w.Header().Set("Cache-Control", "public, max-age=31536000, immutable")
		}
		files.ServeHTTP(w, r)
	})
}

// staticVersion is a short hash of all embedded static assets, computed
// once at startup. Appended as ?v= to asset URLs so deploys bust caches.
var staticVersion = func() string {
	h := sha256.New()
	_ = fs.WalkDir(staticFS, ".", func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		b, err := staticFS.ReadFile(path)
		if err != nil {
			return nil
		}
		h.Write([]byte(path))
		h.Write(b)
		return nil
	})
	return hex.EncodeToString(h.Sum(nil))[:12]
}()

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
	r.Get("/uploads/services/{file}", s.ServeServicePhoto)
	r.Get("/uploads/staff/{file}", s.ServeStaffPhoto)

	loginLimiter := middleware.NewRateLimiter(20, time.Minute)
	bookLimiter := middleware.NewRateLimiter(30, time.Minute)

	r.Get("/", s.Home)
	r.Get("/services", s.ServicesPage)
	r.Post("/lang", s.SetLanguage)
	r.Get("/careers", s.CareersPage)
	r.With(middleware.RateLimit(bookLimiter, func(req *http.Request) string {
		return "apply:" + req.RemoteAddr
	})).Post("/careers/{id}/apply", s.ApplyForJob)
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
		pr.Post("/bookings/{id}/correct", s.PortalCorrectBooking)
		pr.Post("/bookings/{id}/refunds", s.PortalRecordRefund)
		pr.Post("/bookings/{id}/refunds/{rid}/void", s.PortalVoidRefund)

		pr.Get("/reports", s.PortalReports)
		pr.Get("/reports.csv", s.PortalReportsCSV)

		pr.Get("/customers", s.PortalCustomers)
		pr.Get("/customers/search", s.PortalCustomerSearch)
		pr.Get("/customers/new", s.PortalNewCustomerForm)
		pr.Post("/customers/new", s.PortalCreateCustomer)
		pr.Get("/customers/{id}", s.PortalCustomerDetail)
		pr.Get("/customers/{id}/edit", s.PortalEditCustomerForm)
		pr.Post("/customers/{id}/edit", s.PortalUpdateCustomer)
		pr.Post("/customers/{id}/delete", s.PortalDeleteCustomer)
		pr.Post("/customers/{id}/toggle-active", s.PortalToggleCustomerActive)

		pr.Get("/schedule", s.PortalSchedule)
		pr.Post("/schedule/time-off", s.PortalCreateTimeOff)
		pr.Post("/schedule/time-off/{id}/delete", s.PortalDeleteTimeOff)
		pr.Post("/staff", s.PortalCreateStaff)
		pr.Get("/staff/{id}/edit", s.PortalEditStaffForm)
		pr.Post("/staff/{id}/edit", s.PortalUpdateStaff)
		pr.Post("/staff/{id}/delete", s.PortalDeleteStaff)
		pr.Get("/staff/{id}/hours", s.PortalStaffHoursForm)
		pr.Post("/staff/{id}/hours", s.PortalSaveStaffHours)

		pr.Get("/services", s.PortalServices)
		pr.Post("/services", s.PortalCreateService)
		pr.Get("/services/{id}/edit", s.PortalEditServiceForm)
		pr.Post("/services/{id}/edit", s.PortalUpdateService)
		pr.Post("/services/{id}/toggle", s.PortalToggleService)
		pr.Post("/services/{id}/delete", s.PortalDeleteService)
		pr.Post("/staff/{id}/toggle", s.PortalToggleStaff)
	})

	// Admin Only Routes
	r.Route("/admin", func(ar chi.Router) {
		ar.Use(middleware.RequireAdmin)
		ar.Get("/", s.AdminHome)
		ar.Get("/locations", s.AdminLocations)
		ar.Post("/locations", s.AdminCreateLocation)
		// Services moved to the staff portal; keep old admin URL working.
		ar.Get("/services", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/portal/services", http.StatusSeeOther)
		})
		// Staff management moved to the portal Team page; keep old admin URL working.
		ar.Get("/staff", func(w http.ResponseWriter, r *http.Request) {
			http.Redirect(w, r, "/portal/schedule", http.StatusSeeOther)
		})
		ar.Get("/bookings", s.AdminBookings)

		// CS Accounts Management
		ar.Get("/cs", s.AdminCSList)
		ar.Post("/cs", s.AdminCreateCS)
		ar.Post("/cs/{id}/toggle-active", s.AdminToggleCSActive)
		ar.Post("/cs/{id}/reset-password", s.AdminResetCSPassword)

		// Job openings & applications
		ar.Get("/jobs", s.AdminJobs)
		ar.Post("/jobs", s.AdminCreateJob)
		ar.Post("/jobs/{id}/toggle", s.AdminToggleJob)
		ar.Get("/jobs/{id}/applications", s.AdminJobApplications)
		ar.Get("/applications/{id}/cv", s.AdminDownloadCV)

		// Customer password reset
		ar.Get("/customers/{id}/reset-password", s.AdminCustomerResetForm)
		ar.Post("/customers/{id}/reset-password", s.AdminResetCustomerPassword)

		// Own password change (self-service; sessions are revoked on save)
		ar.Get("/password", s.AdminPasswordForm)
		ar.Post("/password", s.AdminChangePassword)
	})
}

var (
	_ = domain.RoleCustomer
	_ = strings.TrimSpace
	_ = chimw.RequestID
)
