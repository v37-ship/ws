package service

import (
	"errors"
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

func TestResolveDistance(t *testing.T) {
	zones := posterZones()
	// Give every band a stable id so we can address it like a posted form value.
	ids := make([]uuid.UUID, len(zones))
	for i := range zones {
		ids[i] = uuid.New()
		zones[i].ID = ids[i]
	}

	t.Run("onsite charges nothing and ignores a band", func(t *testing.T) {
		fee, km, err := resolveDistance(domain.PlaceOnsite, &ids[1], zones)
		if err != nil || fee != 0 || km != nil {
			t.Fatalf("fee=%d km=%v err=%v", fee, km, err)
		}
	})

	t.Run("empty table turns the feature off", func(t *testing.T) {
		fee, km, err := resolveDistance(domain.PlaceHomecare, nil, nil)
		if err != nil || fee != 0 || km != nil {
			t.Fatalf("fee=%d km=%v err=%v", fee, km, err)
		}
	})

	t.Run("homecare without a band is rejected", func(t *testing.T) {
		if _, _, err := resolveDistance(domain.PlaceHomecare, nil, zones); !errors.Is(err, ErrValidation) {
			t.Fatalf("err=%v want ErrValidation", err)
		}
	})

	t.Run("unknown band is rejected", func(t *testing.T) {
		other := uuid.New()
		if _, _, err := resolveDistance(domain.PlaceHomecare, &other, zones); !errors.Is(err, ErrValidation) {
			t.Fatalf("err=%v want ErrValidation", err)
		}
	})

	t.Run("inactive band is rejected", func(t *testing.T) {
		paused := zones
		paused[2].Active = false
		if _, _, err := resolveDistance(domain.PlaceHomecare, &ids[2], paused); !errors.Is(err, ErrValidation) {
			t.Fatalf("err=%v want ErrValidation", err)
		}
	})

	t.Run("band fee and upper bound are snapshotted", func(t *testing.T) {
		fee, km, err := resolveDistance(domain.PlaceHomecare, &ids[3], zones)
		if err != nil {
			t.Fatal(err)
		}
		if fee != 25000 || km == nil || *km != 7 {
			t.Fatalf("fee=%d km=%v want 25000 / 7", fee, km)
		}
	})
}

func TestPosterZonesAreContiguousAndPriced(t *testing.T) {
	zones := posterZones()
	if len(zones) != 6 {
		t.Fatalf("bands = %d want 6", len(zones))
	}
	want := map[int64]int64{1: 5000, 3: 10000, 5: 20000, 7: 25000, 9: 30000, 12: 50000}
	for _, z := range zones {
		if want[int64(z.MaxKM)] != z.FeeIDR {
			t.Errorf("band max %v fee = %d want %d", z.MaxKM, z.FeeIDR, want[int64(z.MaxKM)])
		}
		if !z.Active {
			t.Errorf("band %s should be active at launch", z.Label())
		}
	}
}

func TestSameKm(t *testing.T) {
	a, b := 7.0, 7.0
	if !sameKm(&a, &b) {
		t.Error("equal distances should match")
	}
	c := 9.0
	if sameKm(&a, &c) {
		t.Error("different distances should not match")
	}
	if !sameKm(nil, nil) || sameKm(&a, nil) {
		t.Error("nil handling is wrong")
	}
}
