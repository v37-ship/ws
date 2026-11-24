package service

import (
	"testing"
	"time"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// staffWindow builds one staff_hours row. Weekday follows the DB convention:
// 0 = Sunday .. 6 = Saturday (Postgres EXTRACT(DOW)).
func staffWindow(weekday int, start, end string) domain.StaffHours {
	return domain.StaffHours{Weekday: weekday, StartTime: start, EndTime: end}
}

// wibAt is a timestamptz fixture in the reporting zone; time-offs are stored as
// instants, so every range is built here rather than from UTC fields.
func utilDate(year int, month time.Month, day, hour, minute int) time.Time {
	return time.Date(year, month, day, hour, minute, 0, 0, wib)
}

func span(start, end time.Time) domain.TimeSpan {
	return domain.TimeSpan{StartAt: start, EndAt: end}
}

// TestScheduledMinutesMonthWeeks pins the calendar basis: scheduled minutes are
// the weekly staff_hours expanded over the month's real WIB days, one window
// per matching weekday. It is not "hour_per_week × 4.3": a month that starts
// or ends mid-week is clamped by the date range itself, and split shifts sum.
func TestScheduledMinutesMonthWeeks(t *testing.T) {
	cases := []struct {
		name  string
		month string
		hours []domain.StaffHours
		want  int
	}{
		{
			name:  "four Mondays in February 2026",
			month: "2026-02",
			hours: []domain.StaffHours{staffWindow(1, "09:00", "17:00")},
			want:  4 * 480,
		},
		{
			name:  "November 2026 ends on a Monday: five of them count",
			month: "2026-11",
			hours: []domain.StaffHours{staffWindow(1, "09:00", "17:00")},
			want:  5 * 480,
		},
		{
			name:  "split shift on the same weekday sums both windows",
			month: "2026-02",
			hours: []domain.StaffHours{staffWindow(1, "09:00", "12:00"), staffWindow(1, "13:00", "17:00")},
			want:  4 * (180 + 240),
		},
		{
			name:  "two weekdays",
			month: "2026-02",
			hours: []domain.StaffHours{staffWindow(1, "09:00", "17:00"), staffWindow(3, "09:00", "12:00")},
			want:  4*480 + 4*180,
		},
		{
			name:  "no schedule",
			month: "2026-02",
			hours: nil,
			want:  0,
		},
		{
			name:  "invalid month is not a schedule",
			month: "nope",
			hours: []domain.StaffHours{staffWindow(1, "09:00", "17:00")},
			want:  0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := scheduledMinutesInMonth(tc.month, tc.hours, nil); got != tc.want {
				t.Errorf("scheduledMinutesInMonth(%s) = %d, want %d", tc.month, got, tc.want)
			}
		})
	}
}

// TestScheduledMinutesTimeOff pins the subtraction: only the part of a time-off
// range that overlaps a scheduled window reduces the total, and overlapping
// time-off rows are merged first so the same minutes are never subtracted
// twice. A time-off outside working hours changes nothing.
func TestScheduledMinutesTimeOff(t *testing.T) {
	monday := []domain.StaffHours{staffWindow(1, "09:00", "17:00")}
	// February 2026 has four Mondays: 2, 9, 16 and 23.
	cases := []struct {
		name    string
		timeOff []domain.TimeSpan
		want    int
	}{
		{
			name:    "nothing booked off",
			timeOff: nil,
			want:    4 * 480,
		},
		{
			name:    "a two-hour block inside one Monday",
			timeOff: []domain.TimeSpan{span(utilDate(2026, 2, 9, 10, 0), utilDate(2026, 2, 9, 12, 0))},
			want:    4*480 - 120,
		},
		{
			name:    "a block starting before opening only loses the scheduled part",
			timeOff: []domain.TimeSpan{span(utilDate(2026, 2, 9, 8, 0), utilDate(2026, 2, 9, 10, 0))},
			want:    4*480 - 60,
		},
		{
			name:    "a block after closing changes nothing",
			timeOff: []domain.TimeSpan{span(utilDate(2026, 2, 9, 18, 0), utilDate(2026, 2, 9, 20, 0))},
			want:    4 * 480,
		},
		{
			name: "overlapping time-off rows count once",
			timeOff: []domain.TimeSpan{
				span(utilDate(2026, 2, 9, 10, 0), utilDate(2026, 2, 9, 11, 0)),
				span(utilDate(2026, 2, 9, 10, 30), utilDate(2026, 2, 9, 12, 0)),
			},
			want: 4*480 - 120,
		},
		{
			name:    "a range spanning two days clips at the window",
			timeOff: []domain.TimeSpan{span(utilDate(2026, 2, 8, 22, 0), utilDate(2026, 2, 9, 10, 0))},
			want:    4*480 - 60,
		},
		{
			name:    "a time-off with no schedule subtracts nothing",
			timeOff: []domain.TimeSpan{span(utilDate(2026, 2, 9, 10, 0), utilDate(2026, 2, 9, 12, 0))},
			want:    0,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			hours := monday
			if tc.name == "a time-off with no schedule subtracts nothing" {
				hours = nil
			}
			if got := scheduledMinutesInMonth("2026-02", hours, tc.timeOff); got != tc.want {
				t.Errorf("scheduledMinutesInMonth with %+v = %d, want %d", tc.timeOff, got, tc.want)
			}
		})
	}
}

// TestStaffUtilizationNumbers pins the two derived figures and their zero
// behaviour: integer division only (no NaN), basis points for occupancy, and a
// zero schedule yields zeros that the template renders as "—".
func TestStaffUtilizationNumbers(t *testing.T) {
	cases := []struct {
		name              string
		scheduled, booked int
		serviceGross      int64
		wantRpPerHour     int64
		wantOccupancyBp   int
	}{
		{"no schedule and no visits", 0, 0, 0, 0, 0},
		{"visits without a schedule", 0, 150, 350_000, 0, 0},
		{"150 of 2280 minutes at Rp350.000", 2280, 150, 350_000, 9_211, 658},
		{"a fully booked schedule", 1800, 1800, 0, 0, 10_000},
		{"overbooked schedule is over 100%", 1800, 2700, 0, 0, 15_000},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rph, occ := staffUtilNumbers(tc.scheduled, tc.booked, tc.serviceGross)
			if rph != tc.wantRpPerHour || occ != tc.wantOccupancyBp {
				t.Errorf("staffUtilNumbers(%d, %d, %d) = (%d, %d), want (%d, %d)",
					tc.scheduled, tc.booked, tc.serviceGross, rph, occ, tc.wantRpPerHour, tc.wantOccupancyBp)
			}
		})
	}
}

// TestMergeStaffUtilization pins how utilization meets the revenue rows: a staff
// member with paid lines keeps the payment-based Gross and Net untouched, a
// staff member with only completed visits or only a schedule is added (those
// rows are the point of the tab), and the table keeps its net ordering.
// ServiceGrossIDR is deliberately not the payment Gross: the numerator is
// completed-visit revenue, the column is paid money, and the page labels both.
func TestMergeStaffUtilization(t *testing.T) {
	staffA := uuid.New() // paid this month, so a revenue row exists
	staffB := uuid.New() // one completed visit, no schedule
	staffC := uuid.New() // schedule, no visits
	rows := []domain.StaffRow{{
		StaffID: staffA, Name: "Bidan Ayu", Sessions: 1,
		GrossIDR: 200_000, NetIDR: 200_000, AvgIDR: 200_000,
	}}
	utils := []domain.StaffUtilization{
		{
			StaffID: staffA, Name: "Bidan Ayu",
			BookedMin: 150, ServiceGrossIDR: 350_000,
			Hours:   []domain.StaffHours{staffWindow(1, "09:00", "17:00")},
			TimeOff: []domain.TimeSpan{span(utilDate(2026, 11, 9, 10, 0), utilDate(2026, 11, 9, 12, 0))},
		},
		{StaffID: staffB, Name: "Bidan Bunga", BookedMin: 60, ServiceGrossIDR: 150_000},
		{
			StaffID: staffC, Name: "Bidan Cempaka",
			Hours: []domain.StaffHours{staffWindow(1, "09:00", "17:00")},
		},
	}
	merged := mergeStaffUtilization("2026-11", rows, utils)
	if len(merged) != 3 {
		t.Fatalf("merged rows = %d, want 3", len(merged))
	}
	byID := map[uuid.UUID]domain.StaffRow{}
	for _, row := range merged {
		byID[row.StaffID] = row
	}
	a := byID[staffA]
	// November 2026 has five Mondays: 5×480 − 120 time-off = 2280 scheduled.
	if a.ScheduledMin != 2280 || a.BookedMin != 150 {
		t.Errorf("staff A minutes = scheduled %d / booked %d, want 2280 / 150", a.ScheduledMin, a.BookedMin)
	}
	if a.RpPerHour != 9_211 || a.OccupancyBp != 658 {
		t.Errorf("staff A utilization = Rp %d/hour, %d bp, want 9211 and 658", a.RpPerHour, a.OccupancyBp)
	}
	// The revenue columns are the payment-based ones and do not move.
	if a.GrossIDR != 200_000 || a.NetIDR != 200_000 || a.Sessions != 1 {
		t.Errorf("staff A revenue changed: %+v", a)
	}
	b := byID[staffB]
	if b.ScheduledMin != 0 || b.BookedMin != 60 || b.RpPerHour != 0 || b.OccupancyBp != 0 {
		t.Errorf("staff B = %+v, want booked 60 with no schedule and zero ratios", b)
	}
	c := byID[staffC]
	if c.ScheduledMin != 2400 || c.BookedMin != 0 || c.OccupancyBp != 0 {
		t.Errorf("staff C = %+v, want 2400 scheduled and no booked minutes", c)
	}
	if merged[0].StaffID != staffA {
		t.Errorf("first row = %s, want the only positive-net staff row (%s)", merged[0].StaffID, staffA)
	}

	// With no utilization rows the revenue table is returned exactly as it came
	// in: the staff tab must not re-shape months that have no schedule data.
	same := mergeStaffUtilization("2026-11", []domain.StaffRow{{StaffID: staffA, Name: "Bidan Ayu", NetIDR: 1}}, nil)
	if len(same) != 1 || same[0].StaffID != staffA || same[0].NetIDR != 1 {
		t.Errorf("merge with no utilization = %+v, want the input row", same)
	}
}
