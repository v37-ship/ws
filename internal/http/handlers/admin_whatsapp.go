package handlers

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/http/middleware"
	"spa-booking/internal/i18n"
	"spa-booking/internal/repo"
	"spa-booking/internal/service"
	"spa-booking/internal/whatsapp"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// waOutboxStore is the outbox surface the admin page needs. An interface so the
// page and its retry action are testable without a database.
type waOutboxStore interface {
	ListUpcoming(ctx context.Context, templateKey string, limit int) ([]repo.WaOutboxItem, error)
	ListRecent(ctx context.Context, limit int) ([]repo.WaOutboxItem, error)
	ListFailed(ctx context.Context, limit int) ([]repo.WaOutboxItem, error)
	ListByBooking(ctx context.Context, bookingID uuid.UUID, limit int) ([]repo.WaOutboxItem, error)
	RetryFailed(ctx context.Context, id uuid.UUID, at time.Time) (bool, error)
	QueueSummary(ctx context.Context) (pending, failed int, err error)
}

// waOutboxView is one row of the recent/failed table: the outbox row plus the
// page-computed retry decision, so the template never re-derives it.
type waOutboxView struct {
	repo.WaOutboxItem
	Retryable bool
}

// waRetryable mirrors WaRepo.RetryFailed's SQL predicate in Go so the page
// hides the control exactly where the repo refuses (rulings R6/R16): only a
// failed row; never a booking that is cancelled/expired/no_show; and a
// `remind:%` row only while its visit is still ahead. Keep this in lockstep
// with the WHERE clause in RetryFailed.
func waRetryable(it repo.WaOutboxItem, now time.Time) bool {
	if it.Status != "failed" {
		return false
	}
	switch it.BookingStatus {
	case "cancelled", "expired", "no_show":
		return false
	}
	if strings.HasPrefix(derefString(it.DedupeKey), "remind:") && it.BookingID != nil {
		if it.BookingStatus == "completed" || it.BookingStart.Before(now) {
			return false
		}
	}
	return true
}

func derefString(s *string) string {
	if s == nil {
		return ""
	}
	return *s
}

// waOutboxViews wraps rows with their retry decision.
func waOutboxViews(items []repo.WaOutboxItem, now time.Time) []waOutboxView {
	out := make([]waOutboxView, 0, len(items))
	for _, it := range items {
		out = append(out, waOutboxView{WaOutboxItem: it, Retryable: waRetryable(it, now)})
	}
	return out
}

// failWaRetry is the delivery-action failure; refusals are a page notice, so
// this one is only for a database error.
const failWaRetry = "Could not retry that message. Try again."

// waTemplateView is one editable message on the admin page.
type waTemplateView struct {
	Key      string
	LabelKey string
	Default  string
	Override string
	Fields   []string
	Preview  string
	Custom   bool
	// Encoding/Chars/Segments describe the editable body as typed (the
	// override when set, else the built-in copy). Placeholders expand at send
	// time, so the sample preview must not feed this counter (ruling R1).
	Encoding string
	Chars    int
	Segments int
	// Audit trail: Edited is true when a stored row exists; Editor is empty
	// when that row's editor account was deleted (ruling R4), and the
	// template falls back to the neutral label.
	Edited   bool
	Editor   string
	EditedAt time.Time
}

// waTemplateViewFor assembles one card's data. meta is nil when the template
// has no stored row (built-in copy). Kept separate from the page so the
// counter and audit wiring are testable without a database.
func waTemplateViewFor(key string, meta *domain.WaOverrideMeta) waTemplateView {
	override := ""
	if meta != nil {
		override = meta.Body
	}
	ov := strings.TrimSpace(override)
	defaultCopy := whatsapp.DefaultCopy(key)
	preview := defaultCopy
	if ov != "" {
		if p, err := whatsapp.RenderOverride(key, ov, whatsapp.SamplePayload(key)); err == nil {
			preview = p
		}
	}
	body := ov
	if body == "" {
		body = defaultCopy
	}
	encoding, chars, segments := whatsapp.Segments(body)
	view := waTemplateView{
		Key: key, LabelKey: waTemplateLabels[key], Default: defaultCopy,
		Override: ov, Fields: whatsapp.Fields(key), Preview: preview, Custom: ov != "",
		Encoding: encoding, Chars: chars, Segments: segments,
	}
	if meta != nil {
		view.Edited = true
		view.Editor = meta.UpdatedByName
		view.EditedAt = meta.UpdatedAt
	}
	return view
}

// waTemplateViews pairs every editable template with its stored row, if any.
func waTemplateViews(metas map[string]domain.WaOverrideMeta) []waTemplateView {
	keys := whatsapp.EditableTemplates()
	out := make([]waTemplateView, 0, len(keys))
	for _, key := range keys {
		var meta *domain.WaOverrideMeta
		if m, ok := metas[key]; ok {
			meta = &m
		}
		out = append(out, waTemplateViewFor(key, meta))
	}
	return out
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
	overrides, err := s.waSettings.OverridesWithMeta(ctx)
	if err != nil {
		s.log.Error("wa override meta", "err", err)
		http.Error(w, "Could not load WhatsApp settings", http.StatusInternalServerError)
		return
	}
	conn := ""
	if s.wa != nil {
		if c, err := s.wa.ConnectionState(ctx); err == nil {
			conn = c
		}
	}
	var next, recent []repo.WaOutboxItem
	var pending, failed int
	queueOK := true
	failedView := r.URL.Query().Get("outbox") == "failed"
	if s.waOutbox != nil {
		next, _ = s.waOutbox.ListUpcoming(ctx, "booking_remind", 20)
		if failedView {
			recent, _ = s.waOutbox.ListFailed(ctx, 20)
		} else {
			recent, _ = s.waOutbox.ListRecent(ctx, 20)
		}
		var err error
		pending, failed, err = s.waOutbox.QueueSummary(ctx)
		queueOK = err == nil
	}

	templates := waTemplateViews(overrides)

	s.render(w, r, "admin_whatsapp.html", pageData{
		"Settings":     cfg,
		"Raw":          raw,
		"Defaults":     s.waSettings.Defaults(),
		"Templates":    templates,
		"Conn":         conn,
		"Next":         next,
		"Recent":       waOutboxViews(recent, time.Now()),
		"OutboxFailed": failedView,
		"QueuePending": pending,
		"QueueFailed":  failed,
		"QueueOK":      queueOK,
		"Success":      success,
		"Error":        errMsg,
	})
}

// AdminRetryWaOutbox re-queues one failed message (ruling R6): the same row
// flips back to pending, so the dedupe key and history stay put. The send is
// scheduled through the same 07:00-20:00 WIB window as every other customer
// message (ruling R15), so a late-evening retry waits for morning. A cancelled
// row is a deliberate withdrawal and is refused with the other non-retryable
// statuses (sent/claimed and unknown ids), as is a failed row whose booking has
// since closed or a reminder whose visit is already past (the repo owns those
// predicates). Nothing is ever silently re-sent for a visit that no longer
// holds.
func (s *Server) AdminRetryWaOutbox(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	if middleware.UserFrom(r.Context()) == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
		return
	}
	lang := i18n.Resolve(r)
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		// An unparseable id cannot name a row: same notice as any non-retryable
		// message rather than a bare 400 on a plain form.
		http.Redirect(w, r, "/admin/whatsapp?error="+url.QueryEscape(i18n.T(lang, "a.wa.rtyno")), http.StatusSeeOther)
		return
	}
	if s.waOutbox == nil {
		s.log.Error("wa outbox retry: outbox not configured", "id", id)
		s.staffFail(w, r, http.StatusInternalServerError, failWaRetry)
		return
	}
	ok, err := s.waOutbox.RetryFailed(r.Context(), id, service.ClampSendTime(time.Now()))
	if err != nil {
		s.log.Error("wa outbox retry", "id", id, "err", err)
		s.staffFail(w, r, http.StatusInternalServerError, failWaRetry)
		return
	}
	flag, key := "success", "a.wa.rtyok"
	if !ok {
		flag, key = "error", "a.wa.rtyno"
	}
	http.Redirect(w, r, "/admin/whatsapp?"+flag+"="+url.QueryEscape(i18n.T(lang, key)), http.StatusSeeOther)
}

func (s *Server) AdminSaveWhatsApp(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
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
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
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
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
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
		// Same masked target on both paths: the full number never reaches the
		// logs (ruling R5), and the message body never does either.
		s.log.Warn("wa test send failed", "admin", u.ID, "target", whatsapp.MaskTarget(target), "err", err)
		s.renderAdminWhatsApp(w, r, "", "Could not queue the test message")
		return
	}
	s.log.Info("wa test send queued", "admin", u.ID, "target", whatsapp.MaskTarget(target))
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape("Test message queued — check the outbox below"), http.StatusSeeOther)
}

// AdminRescheduleWhatsApp re-queues the H-1 reminders of future confirmed
// bookings. The scheduled row is normally written when a booking is confirmed,
// so this is the recovery action for bookings that predate the automation (or
// whose reminder was cancelled while a switch was off).
func (s *Server) AdminRescheduleWhatsApp(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
		return
	}
	cfg := s.waSettings.Effective(r.Context())
	if !cfg.ReminderEnabled || !cfg.CustomerEnabled {
		s.renderAdminWhatsApp(w, r, "", "Turn the H-1 reminder and automatic customer messages on first")
		return
	}
	n, err := s.book.ReschedulePendingReminders(r.Context())
	if err != nil {
		s.log.Error("wa reschedule reminders", "err", err)
		s.renderAdminWhatsApp(w, r, "", "Could not re-schedule the reminders")
		return
	}
	msg := "No upcoming booking needed a reminder"
	if n > 0 {
		msg = fmt.Sprintf("%d upcoming reminder(s) re-scheduled", n)
	}
	http.Redirect(w, r, "/admin/whatsapp?success="+url.QueryEscape(msg), http.StatusSeeOther)
}

func (s *Server) AdminSaveWaTemplate(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
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
		s.staffFail(w, r, http.StatusBadRequest, failSession)
		return
	}
	u := middleware.UserFrom(r.Context())
	if u == nil {
		s.staffFail(w, r, http.StatusForbidden, failAdmin)
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
