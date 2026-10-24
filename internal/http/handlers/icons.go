package handlers

import "html/template"

// Icon returns an inline SVG icon (Lucide-style: 24px grid, stroke-based,
// currentColor) for UI chrome. Icons are markup, not copy: keep emoji out of
// dict values and pass icon names from templates instead, e.g.
//
//	{{icon "chat"}} {{t .Lang "p.det.wa"}}
//
// Unknown names render nothing (see TestIconNames). Text glyphs (stars,
// checkmarks, hamburger) stay plain characters; WhatsApp message content
// keeps its emoji (it renders in WhatsApp, not in our UI).
func Icon(name string) template.HTML {
	body, ok := icons[name]
	if !ok {
		return ""
	}
	return template.HTML(`<svg class="ic" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="1.8" stroke-linecap="round" stroke-linejoin="round" aria-hidden="true">` + body + `</svg>`)
}

var icons = map[string]string{
	"plus":         `<path d="M5 12h14"/><path d="M12 5v14"/>`,
	"hospital":     `<path d="M4 21v-8.5L12 5l8 7.5V21"/><path d="M2.5 21h19"/><path d="M10 21v-3.5a2 2 0 0 1 4 0V21"/><path d="M12 7.5V10"/><path d="M10.75 8.75h2.5"/>`,
	"grid":         `<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><rect x="14" y="14" width="7" height="7" rx="1"/>`,
	"calendar":     `<rect x="3" y="4" width="18" height="18" rx="4"/><path d="M16 2v4"/><path d="M8 2v4"/><path d="M3 10h18"/>`,
	"users":        `<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M22 21v-2a4 4 0 0 0-3-3.87"/><path d="M16 3.13a4 4 0 0 1 0 7.75"/>`,
	"user":         `<path d="M19 21v-2a4 4 0 0 0-4-4H9a4 4 0 0 0-4 4v2"/><circle cx="12" cy="7" r="4"/>`,
	"user-plus":    `<path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2"/><circle cx="9" cy="7" r="4"/><path d="M19 8v6"/><path d="M22 11h-6"/>`,
	"sparkles":     `<path d="M4 20c2.5 0 5-1 8-3 3 2 5.5 3 8 3-1.2-3.5-4-6-8-7-4 1-6.8 3.5-8 7z"/><path d="M12 17V9"/><path d="M12 9c-1.5 1.5-2 3.5-2 6h4c0-2.5-.5-4.5-2-6z"/>`,
	"utensils":     `<path d="M3 2v7c0 1.1.9 2 2 2h4a2 2 0 0 0 2-2V2"/><path d="M7 2v20"/><path d="M21 15V2a5 5 0 0 0-5 5v6c0 1.1.9 2 2 2h3zm0 0v7"/>`,
	"image":        `<rect x="3" y="3" width="18" height="18" rx="4"/><circle cx="9" cy="9" r="2"/><path d="m21 15-3.1-3.1a2 2 0 0 0-2.8 0L6 21"/>`,
	"images":       `<rect x="9" y="9" width="13" height="13" rx="3"/><path d="M5 15H4a2 2 0 0 1-2-2V4a2 2 0 0 1 2-2h9a2 2 0 0 1 2 2v1"/>`,
	"ticket":       `<path d="M2 9V7a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v2a3 3 0 0 0 0 6v2a2 2 0 0 1-2 2H4a2 2 0 0 1-2-2v-2a3 3 0 0 0 0-6z"/><path d="M13 5v2"/><path d="M13 11v2"/><path d="M13 17v2"/>`,
	"crown":        `<path d="m2 8 4.5 4L12 5l5.5 7L22 8v10a1 1 0 0 1-1 1H3a1 1 0 0 1-1-1z"/>`,
	"shield":       `<path d="M20 13c0 5-3.5 7.5-7.66 8.95a1 1 0 0 1-.67-.01C7.5 20.5 4 18 4 13V6a1 1 0 0 1 1-1c2 0 4.5-1.2 6.24-2.72a1.17 1.17 0 0 1 1.52 0C14.51 3.81 17 5 19 5a1 1 0 0 1 1 1z"/>`,
	"chart":        `<path d="M3 3v16a2 2 0 0 0 2 2h16"/><path d="M7 13v4"/><path d="M12 9v8"/><path d="M17 5v12"/>`,
	"cash":         `<rect x="2" y="6" width="20" height="12" rx="4"/><circle cx="12" cy="12" r="2"/><path d="M6 12h.01"/><path d="M18 12h.01"/>`,
	"qris":         `<rect x="3" y="3" width="7" height="7" rx="1"/><rect x="14" y="3" width="7" height="7" rx="1"/><rect x="3" y="14" width="7" height="7" rx="1"/><path d="M17.5 14v7"/><path d="M14 17.5h7"/>`,
	"chat":         `<path d="M7.9 20A9 9 0 1 0 4 16.1L2 22z"/>`,
	"pin":          `<path d="M20 10c0 6-8 12-8 12s-8-6-8-12a8 8 0 0 1 16 0z"/><circle cx="12" cy="10" r="3"/>`,
	"phone":        `<path d="M22 16.92v3a2 2 0 0 1-2.18 2 19.79 19.79 0 0 1-8.63-3.07 19.5 19.5 0 0 1-6-6 19.79 19.79 0 0 1-3.07-8.67A2 2 0 0 1 4.11 2h3a2 2 0 0 1 2 1.72c.127.96.361 1.903.7 2.81a2 2 0 0 1-.45 2.11L8.09 9.91a16 16 0 0 0 6 6l1.27-1.27a2 2 0 0 1 2.11-.45c.907.339 1.85.573 2.81.7A2 2 0 0 1 22 16.92z"/>`,
	"mail":         `<rect x="2" y="4" width="20" height="16" rx="4"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/>`,
	"clock":        `<circle cx="12" cy="12" r="10"/><path d="M12 6v6l4 2"/>`,
	"instagram":    `<rect x="2" y="2" width="20" height="20" rx="5"/><circle cx="12" cy="12" r="4"/><path d="M17.5 6.5h.01"/>`,
	"tiktok":       `<path d="M9 12a4 4 0 1 0 4 4V4c.6 2.5 2.4 4.4 5 4.8"/>`,
	"facebook":     `<path d="M18 2h-3a5 5 0 0 0-5 5v3H7v4h3v8h4v-8h3l1-4h-4V7a1 1 0 0 1 1-1h3z"/>`,
	"home":         `<path d="m3 9 9-7 9 7v11a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2z"/><path d="M9 22V12h6v10"/>`,
	"building":     `<rect x="4" y="2" width="16" height="20" rx="3"/><path d="M9 22v-4h6v4"/><path d="M8 6h.01"/><path d="M16 6h.01"/><path d="M12 6h.01"/><path d="M8 10h.01"/><path d="M16 10h.01"/><path d="M12 10h.01"/><path d="M8 14h.01"/><path d="M16 14h.01"/><path d="M12 14h.01"/>`,
	"briefcase":    `<rect x="2" y="7" width="20" height="14" rx="4"/><path d="M16 21V5a2 2 0 0 0-2-2h-4a2 2 0 0 0-2 2v16"/>`,
	"play":         `<path d="m6 4 14 8-14 8z"/>`,
	"check-circle": `<circle cx="12" cy="12" r="10"/><path d="m9 12 2 2 4-4"/>`,
	"alert":        `<path d="m21.73 18-8-14a2 2 0 0 0-3.48 0l-8 14A2 2 0 0 0 4 21h16a2 2 0 0 0 1.73-3z"/><path d="M12 9v4"/><path d="M12 17h.01"/>`,
	"x-circle":     `<circle cx="12" cy="12" r="10"/><path d="m15 9-6 6"/><path d="m9 9 6 6"/>`,
	"printer":      `<path d="M6 9V2h12v7"/><path d="M6 18H4a2 2 0 0 1-2-2v-5a2 2 0 0 1 2-2h16a2 2 0 0 1 2 2v5a2 2 0 0 1-2 2h-2"/><rect x="6" y="14" width="12" height="8" rx="2"/>`,
	"download":     `<path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><path d="m7 10 5 5 5-5"/><path d="M12 15V3"/>`,
	"external":     `<path d="M15 3h6v6"/><path d="M10 14 21 3"/><path d="M18 13v6a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2V8a2 2 0 0 1 2-2h6"/>`,
	"key":          `<path d="m21 2-2 2m-7.61 7.61a5.5 5.5 0 1 1-7.778 7.778 5.5 5.5 0 0 1 7.777-7.777zm0 0L15.5 7.5m0 0 3 3L22 7l-3-3m-3.5 3.5L19 4"/>`,
	"info":         `<circle cx="12" cy="12" r="10"/><path d="M12 16v-4"/><path d="M12 8h.01"/>`,
	"smile":        `<circle cx="12" cy="12" r="10"/><path d="M8 14s1.5 2 4 2 4-2 4-2"/><path d="M9 9h.01"/><path d="M15 9h.01"/>`,
	"heart":        `<path d="M19 14c1.49-1.46 3-3.21 3-5.5A5.5 5.5 0 0 0 16.5 3c-1.76 0-3 .5-4.5 2-1.5-1.5-2.74-2-4.5-2A5.5 5.5 0 0 0 2 8.5c0 2.3 1.5 4.05 3 5.5l7 7z"/>`,
	"send":         `<path d="m22 2-7 20-4-9-9-4z"/><path d="M22 2 11 13"/>`,
}
