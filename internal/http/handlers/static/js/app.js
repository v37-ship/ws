// Global HTMX + Alpine helpers.
(function () {
  // Theme button visuals sync. The cycle logic lives in the inline head
  // script (window.__wanggaTheme) so the toggle works even with a stale
  // cached app.js; this just repaints the label after load.
  try {
    if (window.__wanggaTheme) {
      window.__wanggaTheme.paint(window.__wanggaTheme.get());
    }
  } catch (e) { /* ignore */ }
  // Toasts via HX-Trigger: {"toast": {"kind": "success", "message": "..."}}
  document.body.addEventListener("toast", function (e) {
    var d = e.detail || {};
    if (typeof d.value !== "undefined") d = d.value;
    showToast(d.kind || "info", d.message || "");
  });

  // Fallback: ?toast=msg&kind=success query flash -> toast + clean URL.
  try {
    var p = new URLSearchParams(location.search);
    if (p.get("toast")) {
      showToast(p.get("kind") || "info", p.get("toast"));
      p.delete("toast"); p.delete("kind");
      var q = p.toString();
      history.replaceState(null, "", location.pathname + (q ? "?" + q : ""));
    }
  } catch (e) { /* ignore */ }

  function showToast(kind, message) {
    if (!message) return;
    var region = document.getElementById("toasts");
    if (!region) return;
    var el = document.createElement("div");
    el.className = "toast toast-" + kind;
    el.setAttribute("role", "status");
    var span = document.createElement("span");
    span.textContent = message;
    var btn = document.createElement("button");
    btn.type = "button"; btn.className = "toast-close";
    btn.setAttribute("aria-label", "Dismiss");
    btn.textContent = "×";
    btn.addEventListener("click", function () { el.remove(); });
    el.appendChild(span); el.appendChild(btn);
    region.appendChild(el);
    setTimeout(function () { el.remove(); }, 5000);
  }

  // Slot picker: Alpine sets hidden inputs; this is the non-Alpine fallback
  // kept for existing book.html inline script migration.
  document.body.addEventListener("click", function (e) {
    var btn = e.target && e.target.closest ? e.target.closest(".slot[data-start]") : null;
    // Wizard (#book-form) uses Alpine pick(); legacy forms use this fallback.
    // If Alpine failed to load, fall back here too so slots stay clickable.
    if (!btn || (typeof window.Alpine !== "undefined" && btn.closest("#book-form"))) return;
    var start = document.getElementById("start_at");
    var staff = document.getElementById("staff_id_selected");
    if (start) start.value = btn.dataset.start || "";
    if (staff) staff.value = btn.dataset.staff || "";
    document.querySelectorAll(".slot").forEach(function (s) { s.setAttribute("aria-pressed", "false"); });
    btn.setAttribute("aria-pressed", "true");
  });
})();
