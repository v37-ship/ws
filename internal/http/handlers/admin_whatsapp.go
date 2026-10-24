package handlers

import (
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/whatsapp"
)

// waTemplateView is one editable message on the admin page.
type waTemplateView struct {
	Key      string
	LabelKey string
	Default  string
	Override string
	Fields   []string
	Preview  string
	Custom   bool
}

// waTemplateLabels maps a template key to its short i18n label key.
var waTemplateLabels = map[string]string{
	"booking_confirm":     "a.wt.confirm",
	"payment_receipt":     "a.wt.receipt",
	"claim_rejected":      "a.wt.reject",
	"booking_remind":      "a.wt.remind",
	"booking_cancelled":   "a.wt.cancel",
	"booking_rescheduled": "a.wt.resched",
	"booking_expired":     "a.wt.expired",
	"review_request":      "a.wt.review",
}

// AdminWhatsApp is the admin-only WhatsApp configuration page.
func (s *Server) AdminWhatsApp(w http.ResponseWriter, r *http.Request) {
	s.renderAdminWhatsApp(w, r, r.URL.Query().Get("success"), r.URL.Query().Get("error"))
}

func (s *Server) renderAdminWhatsApp(w http.ResponseWriter, r *http.Request, success, errMsg string) {
	ctx := r.Context()
	cfg := s.waSettings.Effective(ctx)
	raw, err := s.waSettings.Raw(ctx)
	if err != nil {
		s.log.Error("wa settings", "err", err)
		http.Error(w, "Could not load WhatsApp settings", http.StatusInternalServerError)
		return
	}
	overrides, err := s.waSettings.Overrides(ctx)
	if err != nil {
		s.log.Error("wa overrides", "err", err)
		http.Error(w, "Could not load WhatsApp settings", http.StatusInternalServerError)
		return
	}
	conn := ""
	if s.wa != nil {
		if c, err := s.wa.ConnectionState(ctx); err == nil {
			conn = c
		}
	}
	next, _ := s.waOutbox.ListUpcoming(ctx, "booking_remind", 20)
	recent, _ := s.waOutbox.ListRecent(ctx, 20)

	templates := make([]waTemplateView, 0, 8)
	for _, key := range whatsapp.EditableTemplates() {
		ov := strings.TrimSpace(overrides[key])
		preview := whatsapp.DefaultCopy(key)
		if ov != "" {
			if p, err := whatsapp.RenderOverride(key, ov, whatsapp.SamplePayload(key)); err == nil {
				preview = p
			}
		}
		templates = append(templates, waTemplateView{
			Key: key, LabelKey: waTemplateLabels[key], Default: whatsapp.DefaultCopy(key),
			Override: ov, Fields: whatsapp.Fields(key), Preview: preview, Custom: ov != "",
		})
	}

	s.render(w, r, "admin_whatsapp.html", pageData{
		"Settings":  cfg,
		"Raw":       raw,
		"Defaults":  s.waSettings.Defaults(),
		"Templates": templates,
		"Conn":      conn,
		"Next":      next,
		"Recent":    recent,
		"Success":   success,
		"Error":     errMsg,
	})
}

func (s *Server) AdminSaveWhatsApp(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	row, err := parseWaSettingsForm(r)
	if err != nil {
		s.renderAdminWhatsApp(w, r, "", err.Error())
		return
	}
	if err := s.waSettings.Save(r.Context(), row, u.ID); err != nil {
		s.renderAdminWhatsApp(w, r, "", err.Error())
		return
	}
	msg := "WhatsApp settings saved"
	if n, err := s.book.ReschedulePendingReminders(r.Context()); err == nil && n > 0 {
		msg = fmt.Sprintf("%s · %d upcoming reminders re-scheduled", msg, n)
	}
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) AdminResetWhatsApp(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.waSettings.ResetAll(r.Context(), u.ID); err != nil {
		s.renderAdminWhatsApp(w, r, "", "Could not reset the settings")
		return
	}
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape("All settings reset to the environment defaults"), http.StatusSeeOther)
}

func (s *Server) AdminTestWhatsApp(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	target := strings.TrimSpace(r.FormValue("target"))
	if target == "" {
		s.renderAdminWhatsApp(w, r, "", "Enter a phone number or a group id to send the test to")
		return
	}
	text := strings.TrimSpace(r.FormValue("message"))
	if text == "" {
		text = "Tes pesan Wangga Spa ✅ — konfigurasi WhatsApp aktif."
	}
	dedupe := fmt.Sprintf("test:%d", time.Now().UnixNano())
	if _, err := s.wa.Enqueue(r.Context(), target, "test", map[string]any{"text": text}, nil, dedupe, time.Time{}); err != nil {
		s.log.Error("wa test send", "err", err)
		s.renderAdminWhatsApp(w, r, "", "Could not queue the test message")
		return
	}
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape("Test message queued — check the outbox below"), http.StatusSeeOther)
}

func (s *Server) AdminSaveWaTemplate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	key := r.FormValue("key")
	if err := s.waSettings.SetOverride(r.Context(), key, r.FormValue("body"), u.ID); err != nil {
		s.renderAdminWhatsApp(w, r, "", err.Error())
		return
	}
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape("Message copy saved"), http.StatusSeeOther)
}

func (s *Server) AdminResetWaTemplate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid request", http.StatusBadRequest)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		http.Error(w, "forbidden", http.StatusForbidden)
		return
	}
	if err := s.waSettings.ResetOverride(r.Context(), r.FormValue("key"), u.ID); err != nil {
		s.renderAdminWhatsApp(w, r, "", "Could not reset the message copy")
		return
	}
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape("Default message copy restored"), http.StatusSeeOther)
}

// parseWaSettingsForm reads the tri-state form: an empty value means "use the
// env default" (a nil field), so an admin can always hand a knob back to env.
func parseWaSettingsForm(r *http.Request) (domain.WaSettings, error) {
	var row domain.WaSettings
	row.CustomerEnabled = triBool(r.FormValue("customer_enabled"))
	row.ReminderEnabled = triBool(r.FormValue("reminder_enabled"))
	row.ReviewEnabled = triBool(r.FormValue("review_enabled"))
	row.DigestEnabled = triBool(r.FormValue("digest_enabled"))
	row.OutboundEnabled = triBool(r.FormValue("outbound_enabled"))
	var err error
	if row.RemindHour, err = triInt(r.FormValue("remind_hour")); err != nil {
		return row, fmt.Errorf("reminder hour must be a number between 0 and 23")
	}
	if row.DigestHour, err = triInt(r.FormValue("digest_hour")); err != nil {
		return row, fmt.Errorf("digest hour must be a number between 0 and 23")
	}
	row.ReviewURL = triStr(r.FormValue("review_url"))
	row.StaffGroupJID = triStr(r.FormValue("staff_group_jid"))
	row.GroupMode = triStr(r.FormValue("group_mode"))
	return row, nil
}

func triBool(v string) *bool {
	switch strings.TrimSpace(v) {
	case "on":
		b := true
		return &b
	case "off":
		b := false
		return &b
	}
	return nil
}

func triInt(v string) (*int, error) {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil, nil
	}
	n, err := strconv.Atoi(v)
	if err != nil {
		return nil, err
	}
	return &n, nil
}

func triStr(v string) *string {
	v = strings.TrimSpace(v)
	if v == "" {
		return nil
	}
	return &v
}
