package whatsapp

import "testing"

func TestRenderTextFallback(t *testing.T) {
	body, err := Render("anything", Payload{"text": "  halo bunda  "})
	if err != nil {
		t.Fatalf("Render: %v", err)
	}
	if body != "halo bunda" {
		t.Fatalf("body = %q", body)
	}

	if _, err := Render("missing", Payload{}); err == nil {
		t.Fatal("expected error for unknown template without text")
	}
	if _, err := Render("missing", Payload{"text": "   "}); err == nil {
		t.Fatal("expected error for blank text")
	}
}
