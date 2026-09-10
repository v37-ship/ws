package service

import (
	"context"
	"fmt"
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
	if svc.ID == uuid.Nil {
		svc.Active = true
		return s.catalog.CreateService(ctx, svc)
	}
	return s.catalog.UpdateService(ctx, svc)
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
		Address:  "Jl. Kayu Putih II No. 32, Pulo Gadung",
		City:     "Jakarta Timur",
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
	var slots []domain.Slot
	duration := time.Duration(svc.DurationMin) * time.Minute
	now := time.Now().In(tz)
	for _, st := range staffList {
		hours, err := s.catalog.ListStaffHours(ctx, st.ID)
		if err != nil {
			return nil, err
		}
		busy, err := s.catalog.BusyIntervals(ctx, st.ID, dayLocal, dayLocal.Add(24*time.Hour))
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
			for !cursor.Add(duration).After(windowEnd) {
				slotEnd := cursor.Add(duration)
				if !cursor.Before(now.Add(30 * time.Minute)) && !overlapsAny(cursor, slotEnd, busy) {
					slots = append(slots, domain.Slot{StartAt: cursor.UTC(), EndAt: slotEnd.UTC(), StaffID: st.ID})
				}
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

func overlapsAny(start, end time.Time, busy [][2]time.Time) bool {
	for _, b := range busy {
		if start.Before(b[1]) && end.After(b[0]) {
			return true
		}
	}
	return false
}
