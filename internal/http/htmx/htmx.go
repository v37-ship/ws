package htmx

import (
	"encoding/json"
	"net/http"
)

// IsRequest reports whether r is an HTMX request.
func IsRequest(r *http.Request) bool {
	return r.Header.Get("HX-Request") == "true"
}

// Trigger sets HX-Trigger so client-side listeners (toast, refresh)
// can react without a full reload. Events are merged if already set.
func Trigger(w http.ResponseWriter, events map[string]any) {
	if len(events) == 0 {
		return
	}
	b, err := json.Marshal(events)
	if err != nil {
		return
	}
	if prev := w.Header().Get("HX-Trigger"); prev != "" {
		w.Header().Set("HX-Trigger", prev+","+string(b))
		return
	}
	w.Header().Set("HX-Trigger", string(b))
}

// Toast emits a toast event consumed by the toast region in app.js.
// kind is one of: success, error, info.
func Toast(w http.ResponseWriter, kind, message string) {
	Trigger(w, map[string]any{"toast": map[string]string{"kind": kind, "message": message}})
}

// Redirect tells HTMX to do a client-side redirect.
func Redirect(w http.ResponseWriter, to string) {
	w.Header().Set("HX-Redirect", to)
}

// Refresh tells HTMX to reload the page.
func Refresh(w http.ResponseWriter) {
	w.Header().Set("HX-Refresh", "true")
}

// Retarget overrides the swap target for this response.
func Retarget(w http.ResponseWriter, selector string) {
	w.Header().Set("HX-Retarget", selector)
}

// Reswap overrides the swap strategy (e.g. "none", "outerHTML", "beforeend").
func Reswap(w http.ResponseWriter, swap string) {
	w.Header().Set("HX-Reswap", swap)
}
