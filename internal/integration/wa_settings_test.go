//go:build integration

package integration

import (
	"context"
	"testing"
	"time"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"
)

// The copy-audit line needs the editor's display name, which lives on users.
// One read must join it in, and a deleted editor (FK SET NULL) must still come
// back as an edited row with an empty name so the page can show the neutral
// fallback (ruling R4) instead of a blank line.

func TestWaOverrideMetaJoinsEditor(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	wa := repo.NewWaSettingsRepo(pool)
	ctx := context.Background()

	editor := insertUser(t, pool, "editor@example.com", string(domain.RoleAdmin))
	const body = "Halo {name}, sampai jumpa besok ya!"
	before := time.Now()
	if err := wa.SetOverride(ctx, "booking_remind", body, editor); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}

	meta, err := wa.ListOverrideMeta(ctx)
	if err != nil {
		t.Fatalf("ListOverrideMeta: %v", err)
	}
	got, ok := meta["booking_remind"]
	if !ok {
		t.Fatal("booking_remind override missing from the metadata read")
	}
	if got.Body != body {
		t.Errorf("body = %q, want %q", got.Body, body)
	}
	if got.UpdatedBy == nil || *got.UpdatedBy != editor {
		t.Errorf("UpdatedBy = %v, want the editor", got.UpdatedBy)
	}
	if want := "Fixture editor@example.com"; got.UpdatedByName != want {
		t.Errorf("UpdatedByName = %q, want %q", got.UpdatedByName, want)
	}
	if got.UpdatedAt.Before(before.Add(-time.Minute)) || got.UpdatedAt.After(time.Now().Add(time.Minute)) {
		t.Errorf("UpdatedAt = %v, want a fresh stamp (around %v)", got.UpdatedAt, before)
	}
	// A template with no row is built-in, not an edited row with empty fields.
	if _, ok := meta["booking_confirm"]; ok {
		t.Error("a never-edited template must not appear in the metadata map")
	}

	// The plain list stays for the callers that only need bodies.
	bodies, err := wa.ListOverrides(ctx)
	if err != nil {
		t.Fatalf("ListOverrides: %v", err)
	}
	if bodies["booking_remind"] != body {
		t.Errorf("ListOverrides lost the body: %q", bodies["booking_remind"])
	}
}

func TestWaOverrideMetaDeletedEditorFallsBack(t *testing.T) {
	pool := OpenTestDB(t)
	TruncateAll(t, pool)
	wa := repo.NewWaSettingsRepo(pool)
	ctx := context.Background()

	editor := insertUser(t, pool, "gone@example.com", string(domain.RoleAdmin))
	if err := wa.SetOverride(ctx, "booking_confirm", "Halo {name}", editor); err != nil {
		t.Fatalf("SetOverride: %v", err)
	}
	// wa_template_overrides.updated_by is ON DELETE SET NULL, so removing the
	// account must leave the row in place and only blank the attribution.
	if _, err := pool.Exec(ctx, `DELETE FROM users WHERE id = $1`, editor); err != nil {
		t.Fatalf("delete editor: %v", err)
	}

	meta, err := wa.ListOverrideMeta(ctx)
	if err != nil {
		t.Fatalf("ListOverrideMeta: %v", err)
	}
	got, ok := meta["booking_confirm"]
	if !ok {
		t.Fatal("the override row must survive its editor's account")
	}
	if got.UpdatedBy != nil {
		t.Errorf("UpdatedBy = %v, want nil after the editor was deleted (R4 fallback)", got.UpdatedBy)
	}
	if got.UpdatedByName != "" {
		t.Errorf("UpdatedByName = %q, want empty after the editor was deleted (R4 fallback)", got.UpdatedByName)
	}
	if got.Body != "Halo {name}" {
		t.Errorf("body = %q, the copy must survive the account deletion", got.Body)
	}
	if got.UpdatedAt.IsZero() {
		t.Error("UpdatedAt must survive the account deletion so the date still renders")
	}
}
