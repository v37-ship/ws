package whatsapp

import (
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

func TestSendText(t *testing.T) {
	var gotPath, gotKey, gotMethod string
	var gotBody map[string]any

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotMethod, gotKey = r.URL.Path, r.Method, r.Header.Get("apikey")
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &gotBody)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"key":{"id":"3EB0ABC"},"status":"PENDING"}`))
	}))
	defer srv.Close()

	c := NewClient(srv.URL+"/", "secret-key", "wangga")
	id, err := c.SendText(t.Context(), "6281234567890", "halo")
	if err != nil {
		t.Fatalf("SendText: %v", err)
	}
	if id != "3EB0ABC" {
		t.Fatalf("id = %q, want 3EB0ABC", id)
	}
	if gotPath != "/message/sendText/wangga" {
		t.Errorf("path = %q", gotPath)
	}
	if gotMethod != http.MethodPost {
		t.Errorf("method = %q", gotMethod)
	}
	if gotKey != "secret-key" {
		t.Errorf("apikey = %q", gotKey)
	}
	if gotBody["number"] != "6281234567890" || gotBody["text"] != "halo" {
		t.Errorf("body = %#v", gotBody)
	}
}

func TestSendTextErrors(t *testing.T) {
	cases := []struct {
		status    int
		permanent bool
	}{
		{http.StatusBadRequest, true},
		{http.StatusNotFound, true},
		{http.StatusTooManyRequests, false},
		{http.StatusInternalServerError, false},
		{http.StatusBadGateway, false},
	}
	for _, c := range cases {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(c.status)
			_, _ = w.Write([]byte(`{"message":"nope"}`))
		}))
		_, err := NewClient(srv.URL, "k", "wangga").SendText(t.Context(), "6281234567890", "x")
		srv.Close()

		var apiErr *APIError
		if !errors.As(err, &apiErr) {
			t.Fatalf("status %d: err = %v, want APIError", c.status, err)
		}
		if apiErr.Status != c.status {
			t.Errorf("status = %d, want %d", apiErr.Status, c.status)
		}
		if apiErr.Permanent() != c.permanent {
			t.Errorf("status %d: Permanent = %v, want %v", c.status, apiErr.Permanent(), c.permanent)
		}
	}
}

func TestConnectionState(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/instance/connectionState/wangga" {
			t.Errorf("path = %q", r.URL.Path)
		}
		_, _ = w.Write([]byte(`{"instance":{"instanceName":"wangga","state":"open"}}`))
	}))
	defer srv.Close()

	state, err := NewClient(srv.URL, "k", "wangga").ConnectionState(t.Context())
	if err != nil {
		t.Fatalf("ConnectionState: %v", err)
	}
	if state != "open" {
		t.Fatalf("state = %q, want open", state)
	}
}

func TestSetWebhook(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	err := NewClient(srv.URL, "k", "wangga").SetWebhook(t.Context(), "http://app:8080/webhooks/evolution", []string{"MESSAGES_UPSERT"})
	if err != nil {
		t.Fatalf("SetWebhook: %v", err)
	}
	wh, _ := got["webhook"].(map[string]any)
	if wh["url"] != "http://app:8080/webhooks/evolution" || wh["enabled"] != true {
		t.Errorf("webhook body = %#v", got)
	}
}
