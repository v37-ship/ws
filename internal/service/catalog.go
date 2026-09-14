package service

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"path/filepath"
	"strings"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

type CatalogService struct {
	catalog *repo.CatalogRepo
}

func NewCatalogService(catalog *repo.CatalogRepo) *CatalogService {
	return &CatalogService{catalog: catalog}
}

func (s *CatalogService) ListLocations(ctx context.Context, activeOnly bool) ([]domain.Location, error) {
	return s.catalog.ListLocations(ctx, activeOnly)
}

func (s *CatalogService) GetLocation(ctx context.Context, id uuid.UUID) (*domain.Location, error) {
	return s.catalog.LocationByID(ctx, id)
}

func (s *CatalogService) SaveLocation(ctx context.Context, l *domain.Location) error {
	if l.Name == "" {
		return fmt.Errorf("%w: name required", ErrValidation)
	}
	if l.Timezone == "" {
		l.Timezone = "Asia/Jakarta"
	}
	if l.ID == uuid.Nil {
		l.Active = true
		return s.catalog.CreateLocation(ctx, l)
	}
	return s.catalog.UpdateLocation(ctx, l)
}

func (s *CatalogService) ListServices(ctx context.Context, locationID uuid.UUID, activeOnly bool) ([]domain.Service, error) {
	return s.catalog.ListServices(ctx, locationID, activeOnly)
}

func (s *CatalogService) GetService(ctx context.Context, id uuid.UUID) (*domain.Service, error) {
	return s.catalog.ServiceByID(ctx, id)
}

func (s *CatalogService) SaveService(ctx context.Context, svc *domain.Service) error {
	if svc.Name == "" || svc.DurationMin <= 0 || svc.PriceIDR < 0 || svc.LocationID == uuid.Nil {
		return fmt.Errorf("%w: invalid service", ErrValidation)
	}
	if err := ValidateImageURL(svc.ImageURL); err != nil {
		return err
	}
	if svc.ID == uuid.Nil {
		svc.Active = true
		return s.catalog.CreateService(ctx, svc)
	}
	return s.catalog.UpdateService(ctx, svc)
}

func (s *CatalogService) ListServiceHours(ctx context.Context, serviceID uuid.UUID) ([]domain.ServiceHours, error) {
	return s.catalog.ListServiceHours(ctx, serviceID)
}

// SaveServiceHours replaces the weekly availability windows for a service.
// An empty slice (or a weekday with no row) means closed that weekday;
// pass a row per open day with HH:MM times where end > start.
func (s *CatalogService) SaveServiceHours(ctx context.Context, serviceID uuid.UUID, hours []domain.ServiceHours) error {
	for _, h := range hours {
		if h.Weekday < 0 || h.Weekday > 6 {
			return fmt.Errorf("%w: invalid weekday", ErrValidation)
		}
		if !validHM(h.StartTime) || !validHM(h.EndTime) || h.StartTime >= h.EndTime {
			return fmt.Errorf("%w: invalid hours (use HH:MM, end after start)", ErrValidation)
		}
		h.ServiceID = serviceID
	}
	return s.catalog.ReplaceServiceHours(ctx, serviceID, hours)
}

func validHM(v string) bool {
	if len(v) != 5 || v[2] != ':' {
		return false
	}
	t, err := time.Parse("15:04", v)
	if err != nil {
		return false
	}
	return t.Format("15:04") == v
}

// MaxServicePhotoBytes caps service photo uploads at 2 MB.
const MaxServicePhotoBytes = 2 << 20

var allowedPhotoExts = map[string]bool{
	".jpg": true, ".jpeg": true, ".png": true, ".webp": true,
}

// ValidatePhotoName checks an uploaded filename and returns a safe storage name.
func ValidatePhotoName(original string, size int64) (string, error) {
	if size > MaxServicePhotoBytes {
		return "", fmt.Errorf("%w: photo must be under 2 MB", ErrValidation)
	}
	ext := strings.ToLower(filepath.Ext(original))
	if !allowedPhotoExts[ext] {
		return "", fmt.Errorf("%w: photo must be JPG, PNG or WebP", ErrValidation)
	}
	return uuid.NewString() + ext, nil
}

// ValidateImageURL allows empty (fallback illustration) or an http(s) image link.
func ValidateImageURL(raw string) error {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%w: picture link must be an http(s) URL", ErrValidation)
	}
	ext := strings.ToLower(filepath.Ext(u.Path))
	if ext != "" && !allowedPhotoExts[ext] && ext != ".gif" {
		return fmt.Errorf("%w: picture link must point to an image", ErrValidation)
	}
	return nil
}

// ResolveServiceImage picks the display picture: local upload first, then
// internet link, then the built-in keyword illustration.
func ResolveServiceImage(svc domain.Service) string {
	if svc.ImagePath != "" {
		return "/uploads/services/" + path.Base(svc.ImagePath)
	}
	if strings.TrimSpace(svc.ImageURL) != "" {
		return strings.TrimSpace(svc.ImageURL)
	}
	return KeywordServiceImage(svc.Name)
}

// SummarizeServiceHours renders weekly windows compactly, e.g.
// "Mon–Fri 15:00–20:00 · Sat 09:00–12:00 · closed Sun".
func SummarizeServiceHours(hours []domain.ServiceHours) string {
	if len(hours) == 0 {
		return "All day, every day"
	}
	short := []string{"Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"}
	byDay := map[int]domain.ServiceHours{}
	for _, h := range hours {
		byDay[h.Weekday] = h
	}
	order := []int{1, 2, 3, 4, 5, 6, 0}
	type span struct {
		days   []int
		window string
	}
	var spans []span
	for _, d := range order {
		h, ok := byDay[d]
		if !ok {
			continue
		}
		w := h.StartTime + "–" + h.EndTime
		if n := len(spans); n > 0 && spans[n-1].window == w {
			spans[n-1].days = append(spans[n-1].days, d)
		} else {
			spans = append(spans, span{days: []int{d}, window: w})
		}
	}
	parts := make([]string, 0, len(spans)+1)
	for _, sp := range spans {
		label := short[sp.days[0]]
		if len(sp.days) > 1 {
			label += "–" + short[sp.days[len(sp.days)-1]]
		}
		parts = append(parts, label+" "+sp.window)
	}
	if len(byDay) < 7 {
		parts = append(parts, "closed other days")
	}
	return strings.Join(parts, " · ")
}
func KeywordServiceImage(name string) string {
	n := strings.ToLower(name)
	switch {
	case strings.Contains(n, "baby") || strings.Contains(n, "kid") || strings.Contains(n, "child"):
		return "/static/img/svc-baby.svg"
	case strings.Contains(n, "facial") || strings.Contains(n, "face") || strings.Contains(n, "glow"):
		return "/static/img/svc-facial.svg"
	case strings.Contains(n, "foot") || strings.Contains(n, "reflex") || strings.Contains(n, "pedicure") || strings.Contains(n, "kaki"):
		return "/static/img/svc-foot.svg"
	case strings.Contains(n, "mom") || strings.Contains(n, "massage") || strings.Contains(n, "recovery") ||
		strings.Contains(n, "maternity") || strings.Contains(n, "postpartum") || strings.Contains(n, "pijat"):
		return "/static/img/svc-massage.svg"
	default:
		return "/static/img/svc-default.svg"
	}
}

func (s *CatalogService) ListStaff(ctx context.Context, locationID uuid.UUID, activeOnly bool) ([]domain.Staff, error) {
	return s.catalog.ListStaff(ctx, locationID, activeOnly)
}

func (s *CatalogService) GetStaff(ctx context.Context, id uuid.UUID) (*domain.Staff, error) {
	return s.catalog.StaffByID(ctx, id)
}

func (s *CatalogService) SaveStaff(ctx context.Context, st *domain.Staff, serviceIDs []uuid.UUID) error {
	if st.Name == "" || st.LocationID == uuid.Nil {
		return fmt.Errorf("%w: invalid staff", ErrValidation)
	}
	if st.ID == uuid.Nil {
		st.Active = true
		if err := s.catalog.CreateStaff(ctx, st); err != nil {
			return err
		}
	} else if err := s.catalog.UpdateStaff(ctx, st); err != nil {
		return err
	}
	return s.catalog.SetStaffServices(ctx, st.ID, serviceIDs)
}

func (s *CatalogService) StaffHours(ctx context.Context, staffID uuid.UUID) ([]domain.StaffHours, error) {
	return s.catalog.ListStaffHours(ctx, staffID)
}

func (s *CatalogService) SaveStaffHours(ctx context.Context, staffID uuid.UUID, hours []domain.StaffHours) error {
	return s.catalog.ReplaceStaffHours(ctx, staffID, hours)
}

func (s *CatalogService) StaffForService(ctx context.Context, locationID, serviceID uuid.UUID) ([]domain.Staff, error) {
	return s.catalog.StaffForService(ctx, locationID, serviceID)
}

func (s *CatalogService) StaffServiceIDs(ctx context.Context, staffID uuid.UUID) ([]uuid.UUID, error) {
	return s.catalog.StaffServiceIDs(ctx, staffID)
}

func (s *CatalogService) ListStaffTimeOff(ctx context.Context, staffID uuid.UUID) ([]domain.StaffTimeOff, error) {
	return s.catalog.ListStaffTimeOff(ctx, staffID)
}

func (s *CatalogService) CreateStaffTimeOff(ctx context.Context, to *domain.StaffTimeOff) error {
	if to.StaffID == uuid.Nil || to.StartAt.IsZero() || to.EndAt.IsZero() || !to.EndAt.After(to.StartAt) {
		return fmt.Errorf("%w: invalid time-off window", ErrValidation)
	}
	return s.catalog.CreateStaffTimeOff(ctx, to)
}

func (s *CatalogService) DeleteStaffTimeOff(ctx context.Context, id uuid.UUID) error {
	return s.catalog.DeleteStaffTimeOff(ctx, id)
}

func (s *CatalogService) ToggleServiceActive(ctx context.Context, id uuid.UUID) error {
	return s.catalog.ToggleServiceActive(ctx, id)
}

func (s *CatalogService) ToggleStaffActive(ctx context.Context, id uuid.UUID) error {
	return s.catalog.ToggleStaffActive(ctx, id)
}

func (s *CatalogService) SeedDemo(ctx context.Context) error {
	n, err := s.catalog.CountSeed(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	loc := &domain.Location{
		Name:     "Wangga Kayu Putih",
		Address:  "Studio kayu putih No.32, Kayu Putih, Pulo Gadung",
		City:     "Jakarta Timur 13260",
		Timezone: "Asia/Jakarta",
		Active:   true,
	}
	if err := s.catalog.CreateLocation(ctx, loc); err != nil {
		return err
	}
	svcMassage := &domain.Service{
		LocationID: loc.ID, Name: "Baby Spa Soft", Description: "Warm hydroplay and gentle baby spa care",
		DurationMin: 60, PriceIDR: 250000, Active: true,
	}
	svcFacial := &domain.Service{
		LocationID: loc.ID, Name: "Mom Recovery Massage", Description: "Relaxing massage for maternity and postpartum moms",
		DurationMin: 60, PriceIDR: 300000, Active: true,
	}
	if err := s.catalog.CreateService(ctx, svcMassage); err != nil {
		return err
	}
	if err := s.catalog.CreateService(ctx, svcFacial); err != nil {
		return err
	}
	staffA := &domain.Staff{LocationID: loc.ID, Name: "Bidan Ayu", Bio: "Certified baby spa therapist", Active: true}
	staffB := &domain.Staff{LocationID: loc.ID, Name: "Terapis Rina", Bio: "Mom massage specialist", Active: true}
	if err := s.catalog.CreateStaff(ctx, staffA); err != nil {
		return err
	}
	if err := s.catalog.CreateStaff(ctx, staffB); err != nil {
		return err
	}
	if err := s.catalog.SetStaffServices(ctx, staffA.ID, []uuid.UUID{svcMassage.ID, svcFacial.ID}); err != nil {
		return err
	}
	if err := s.catalog.SetStaffServices(ctx, staffB.ID, []uuid.UUID{svcFacial.ID}); err != nil {
		return err
	}
	var hoursA, hoursB []domain.StaffHours
	for _, weekday := range []int{1, 2, 3, 4, 5, 6} {
		hoursA = append(hoursA, domain.StaffHours{Weekday: weekday, StartTime: "10:00", EndTime: "18:00"})
		hoursB = append(hoursB, domain.StaffHours{Weekday: weekday, StartTime: "11:00", EndTime: "19:00"})
	}
	if err := s.catalog.ReplaceStaffHours(ctx, staffA.ID, hoursA); err != nil {
		return err
	}
	return s.catalog.ReplaceStaffHours(ctx, staffB.ID, hoursB)
}

type AvailabilityService struct {
	catalog *repo.CatalogRepo
}

func NewAvailabilityService(catalog *repo.CatalogRepo) *AvailabilityService {
	return &AvailabilityService{catalog: catalog}
}

func (s *AvailabilityService) Slots(ctx context.Context, locationID, serviceID, staffID uuid.UUID, day time.Time) ([]domain.Slot, error) {
	detailed, err := s.SlotsDetailed(ctx, locationID, serviceID, staffID, day)
	if err != nil {
		return nil, err
	}
	out := make([]domain.Slot, 0, len(detailed))
	for _, d := range detailed {
		if d.Available {
			out = append(out, d.Slot)
		}
	}
	return out, nil
}

// PrepBuffer is turnover time reserved after each service (room reset).
// It blocks new bookings from overlapping it but may run past closing time.
const PrepBuffer = 10 * time.Minute

// SlotInfo is one bookable grid position plus its availability state,
// so the UI can show taken blocks dimmed instead of hiding them.
type SlotInfo struct {
	Slot      domain.Slot
	Available bool
	Reason    string // "" when available, else "booked" or "off"
}

func (s *AvailabilityService) SlotsDetailed(ctx context.Context, locationID, serviceID, staffID uuid.UUID, day time.Time) ([]SlotInfo, error) {
	loc, err := s.catalog.LocationByID(ctx, locationID)
	if err != nil {
		return nil, err
	}
	svc, err := s.catalog.ServiceByID(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	if svc.LocationID != locationID {
		return nil, fmt.Errorf("%w: service not at location", ErrValidation)
	}
	tz, err := time.LoadLocation(loc.Timezone)
	if err != nil {
		tz = time.FixedZone("WIB", 7*3600)
	}
	dayLocal := time.Date(day.Year(), day.Month(), day.Day(), 0, 0, 0, 0, tz)
	staffList, err := s.catalog.StaffForService(ctx, locationID, serviceID)
	if err != nil {
		return nil, err
	}
	if staffID != uuid.Nil {
		filtered := make([]domain.Staff, 0, 1)
		for _, st := range staffList {
			if st.ID == staffID {
				filtered = append(filtered, st)
			}
		}
		staffList = filtered
	}
	var slots []SlotInfo
	duration := time.Duration(svc.DurationMin) * time.Minute
	now := time.Now().In(tz)
	svcHours, err := s.catalog.ListServiceHours(ctx, serviceID)
	if err != nil {
		return nil, err
	}
	svcWindow := serviceWindowFor(svcHours, int(dayLocal.Weekday()), dayLocal)
	if len(svcHours) > 0 && svcWindow == nil {
		// Service has configured hours but is closed this weekday.
		return nil, nil
	}
	for _, st := range staffList {
		hours, err := s.catalog.ListStaffHours(ctx, st.ID)
		if err != nil {
			return nil, err
		}
		booked, off, err := s.catalog.BusyDetails(ctx, st.ID, dayLocal, dayLocal.Add(24*time.Hour))
		if err != nil {
			return nil, err
		}
		weekday := int(dayLocal.Weekday())
		for _, h := range hours {
			if h.Weekday != weekday {
				continue
			}
			startHM, endHM := parseHM(h.StartTime), parseHM(h.EndTime)
			cursor := dayLocal.Add(startHM)
			windowEnd := dayLocal.Add(endHM)
			// Intersect staff hours with the service's own daily window.
			if svcWindow != nil {
				if svcWindow[0].After(cursor) {
					cursor = svcWindow[0]
				}
				if svcWindow[1].Before(windowEnd) {
					windowEnd = svcWindow[1]
				}
			}
			for !cursor.Add(duration).After(windowEnd) {
				slotEnd := cursor.Add(duration)
				if cursor.Before(now.Add(30 * time.Minute)) {
					cursor = cursor.Add(15 * time.Minute)
					continue
				}
				// The occupied block is service + prep buffer; a candidate
				// is free only if the whole block avoids busy intervals.
				blockEnd := slotEnd.Add(PrepBuffer)
				info := SlotInfo{Slot: domain.Slot{StartAt: cursor.UTC(), EndAt: slotEnd.UTC(), StaffID: st.ID}, Available: true}
				switch {
				case overlapsAny(cursor, blockEnd, booked):
					info.Available, info.Reason = false, "booked"
				case overlapsAny(cursor, blockEnd, off):
					info.Available, info.Reason = false, "off"
				}
				slots = append(slots, info)
				cursor = cursor.Add(15 * time.Minute)
			}
		}
	}
	return slots, nil
}

func parseHM(s string) time.Duration {
	var hh, mm int
	_, _ = fmt.Sscanf(s, "%d:%d", &hh, &mm)
	return time.Duration(hh)*time.Hour + time.Duration(mm)*time.Minute
}

// serviceWindowFor returns the service's [start, end] window for a weekday,
// or nil when the service has no hours configured (available all day).
func serviceWindowFor(hours []domain.ServiceHours, weekday int, dayLocal time.Time) *[2]time.Time {
	for _, h := range hours {
		if h.Weekday != weekday {
			continue
		}
		if !validHM(h.StartTime) || !validHM(h.EndTime) || h.StartTime >= h.EndTime {
			continue
		}
		w := [2]time.Time{dayLocal.Add(parseHM(h.StartTime)), dayLocal.Add(parseHM(h.EndTime))}
		return &w
	}
	return nil
}

func overlapsAny(start, end time.Time, busy [][2]time.Time) bool {
	for _, b := range busy {
		if start.Before(b[1]) && end.After(b[0]) {
			return true
		}
	}
	return false
}
