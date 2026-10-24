package service

import (
	"context"
	"fmt"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

// DistanceService owns the homecare transport fee bands. Bookings snapshot
// the band they used, so editing the table never rewrites old money.
type DistanceService struct {
	zones *repo.DistanceRepo
}

func NewDistanceService(zones *repo.DistanceRepo) *DistanceService {
	return &DistanceService{zones: zones}
}

func (s *DistanceService) ListActive(ctx context.Context) ([]domain.DistanceZone, error) {
	return s.zones.ListActive(ctx)
}

func (s *DistanceService) ListAll(ctx context.Context) ([]domain.DistanceZone, error) {
	return s.zones.ListAll(ctx)
}

// Save validates a band and inserts (ID zero) or updates it. Bands may touch
// at the edges but active bands must not cross.
func (s *DistanceService) Save(ctx context.Context, z *domain.DistanceZone) error {
	if z.MinKM < 0 {
		return fmt.Errorf("%w: minimum distance must be >= 0", ErrValidation)
	}
	if z.MaxKM <= z.MinKM {
		return fmt.Errorf("%w: maximum distance must exceed the minimum", ErrValidation)
	}
	if z.MaxKM > 999 {
		return fmt.Errorf("%w: maximum distance is too large", ErrValidation)
	}
	if z.FeeIDR < 0 {
		return fmt.Errorf("%w: fee must be >= 0", ErrValidation)
	}
	if z.Active {
		overlap, err := s.zones.Overlaps(ctx, z.ID, z.MinKM, z.MaxKM)
		if err != nil {
			return err
		}
		if overlap {
			return fmt.Errorf("%w: this distance band overlaps another active band", ErrValidation)
		}
	}
	if z.ID == uuid.Nil {
		return s.zones.Create(ctx, z)
	}
	return s.zones.Update(ctx, z)
}

func (s *DistanceService) ByID(ctx context.Context, id uuid.UUID) (*domain.DistanceZone, error) {
	return s.zones.ByID(ctx, id)
}

func (s *DistanceService) ToggleActive(ctx context.Context, id uuid.UUID) error {
	return s.zones.ToggleActive(ctx, id)
}

func (s *DistanceService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.zones.Delete(ctx, id)
}

// resolveDistance maps a selected band onto the booking snapshot. A nil repo
// or an empty band table means the feature is off (fee 0, no band required),
// which lets the app run before any operator configures zones. Onsite always
// charges nothing and ignores any posted band.
func resolveDistance(place domain.Place, zoneID *uuid.UUID, zones []domain.DistanceZone) (int64, *float64, error) {
	if place != domain.PlaceHomecare {
		return 0, nil, nil
	}
	if len(zones) == 0 {
		return 0, nil, nil
	}
	if zoneID == nil || *zoneID == uuid.Nil {
		return 0, nil, fmt.Errorf("%w: choose the visit distance", ErrValidation)
	}
	for _, z := range zones {
		if z.ID != *zoneID {
			continue
		}
		if !z.Active {
			break
		}
		km := z.MaxKM
		return z.FeeIDR, &km, nil
	}
	return 0, nil, fmt.Errorf("%w: unknown distance band", ErrValidation)
}

// sameKm compares two optional distances (nil = none).
func sameKm(a, b *float64) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return *a == *b
}

// ResolveDistance validates the chosen band against the active zones and
// returns the fee plus the band's upper bound to snapshot on the booking.
func ResolveDistance(ctx context.Context, zones *repo.DistanceRepo, place domain.Place, zoneID *uuid.UUID) (int64, *float64, error) {
	if place != domain.PlaceHomecare || zones == nil {
		return 0, nil, nil
	}
	active, err := zones.ListActive(ctx)
	if err != nil {
		return 0, nil, err
	}
	return resolveDistance(place, zoneID, active)
}

// posterZones is the launch price list. Seeded once (seed-if-empty) so
// operators can edit or disable bands in the portal afterwards.
func posterZones() []domain.DistanceZone {
	return []domain.DistanceZone{
		{MinKM: 0, MaxKM: 1, FeeIDR: 5000, Active: true, Sort: 1},
		{MinKM: 2, MaxKM: 3, FeeIDR: 10000, Active: true, Sort: 2},
		{MinKM: 4, MaxKM: 5, FeeIDR: 20000, Active: true, Sort: 3},
		{MinKM: 6, MaxKM: 7, FeeIDR: 25000, Active: true, Sort: 4},
		{MinKM: 8, MaxKM: 9, FeeIDR: 30000, Active: true, Sort: 5},
		{MinKM: 10, MaxKM: 12, FeeIDR: 50000, Active: true, Sort: 6},
	}
}

// SeedDistanceZones installs the launch bands when the table is empty.
func (s *DistanceService) SeedDistanceZones(ctx context.Context) error {
	n, err := s.zones.Count(ctx)
	if err != nil {
		return err
	}
	if n > 0 {
		return nil
	}
	for _, z := range posterZones() {
		zone := z
		if err := s.zones.Create(ctx, &zone); err != nil {
			return err
		}
	}
	return nil
}
