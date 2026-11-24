package handlers

import (
	"bytes"
	"strings"
	"testing"

	"spa-booking/internal/domain"

	"github.com/google/uuid"
)

// TestReportsStaffUtilizationColumns executes the staff panel and reads the two
// utilization columns back out of the HTML: the Rp/hour cell and the occupancy
// cell on a staff member with a schedule, and the "—" state (with no NaN or
// blank) on one without. The header carries its own basis line, because these
// ratios are not the payment-based Gross/Net columns beside them.
func TestReportsStaffUtilizationColumns(t *testing.T) {
	s := newReportTestServer(t)
	base := reportsSweepData("staff")
	sum := base["Summary"].(domain.ReportSummary)
	sum.StaffRows = []domain.StaffRow{
		{
			StaffID: uuid.MustParse("44444444-4444-4444-4444-444444444444"),
			Name:    "Bidan Terjadwal", Sessions: 1, UniqueCustomers: 1,
			GrossIDR: 200_000, NetIDR: 200_000, AvgIDR: 200_000,
			ScheduledMin: 2280, BookedMin: 1140, RpPerHour: 333_000, OccupancyBp: 5000,
		},
		{
			StaffID: uuid.MustParse("55555555-5555-5555-5555-555555555555"),
			Name:    "Bidan Tanpa Jadwal", BookedMin: 60,
		},
	}
	base["Summary"] = sum

	for _, lang := range []string{"en", "id"} {
		data := reportsSweepData("staff")
		data["Summary"] = sum
		data["Lang"] = lang
		data["Contact"] = portalPrintData(lang)["Contact"]
		var buf bytes.Buffer
		if err := s.tpl.ExecuteTemplate(&buf, "portal_reports.html", data); err != nil {
			t.Fatalf("execute portal_reports.html (%s): %v", lang, err)
		}
		out := buf.String()
		// The header states the basis of the two ratios, in both languages.
		wantHeader := map[string][]string{
			"en": {"Rp/hour", "service gross, completed visits", "Occupancy", "of scheduled hours"},
			"id": {"Rp/jam", "Okupansi", "jam terjadwal"},
		}[lang]
		for _, want := range wantHeader {
			if !strings.Contains(out, want) {
				t.Errorf("%s: utilization header missing %q", lang, want)
			}
		}
		if !strings.Contains(out, "Rp 333.000") {
			t.Errorf("%s: scheduled staff row missing its Rp/hour value", lang)
		}
		if !strings.Contains(out, "50,0%") && !strings.Contains(out, "50.0%") {
			t.Errorf("%s: scheduled staff row missing its occupancy percent", lang)
		}
		if strings.Contains(out, "NaN") || strings.Contains(out, "Inf") {
			t.Errorf("%s: zero schedule leaked a float artefact into the page", lang)
		}
		// Exactly the unscheduled row renders the em dash in both cells.
		if got := strings.Count(out, `data-scheduled="0"`); got != 1 {
			t.Errorf("%s: unscheduled rows rendered = %d, want 1", lang, got)
		}
		if !strings.Contains(out, ">—<") {
			t.Errorf("%s: zero schedule must render an em dash, not a zero rate", lang)
		}
	}
}
