package service

import (
	"strings"
	"testing"

	"spa-booking/internal/domain"
)

func TestVideoEmbedURL(t *testing.T) {
	cases := []struct {
		in, want string
	}{
		// Google Drive variants
		{"https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/view?usp=sharing", "https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/preview"},
		{"https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/preview", "https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/preview"},
		{"https://drive.google.com/open?id=1aB2cD3eF4gH5iJ6kL7mN8oP", "https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/preview"},
		{"https://drive.google.com/uc?id=1aB2cD3eF4gH5iJ6kL7mN8oP&export=download", "https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/preview"},
		// YouTube variants (all collapse to nocookie embed)
		{"https://www.youtube.com/watch?v=dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtube.com/watch?v=dQw4w9WgXcQ&t=42s", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://youtu.be/dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube.com/embed/dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube.com/shorts/dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		{"https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ", "https://www.youtube-nocookie.com/embed/dQw4w9WgXcQ"},
		// Vimeo variants
		{"https://vimeo.com/123456789", "https://player.vimeo.com/video/123456789"},
		{"https://player.vimeo.com/video/123456789", "https://player.vimeo.com/video/123456789"},
		// Rejections
		{"", ""},
		{"not a url", ""},
		{"ftp://example.com/video.mp4", ""},
		{"javascript:alert(1)", ""},
		{"https://evil.com/video.mp4", ""},
		{"https://example.com/watch?v=dQw4w9WgXcQ", ""},
		{"https://www.youtube.com/watch?v=short", ""},
		{"https://drive.google.com/file/d/short/view", ""},
		{"https://vimeo.com/abc", ""},
		{"https://youtu.be/", ""},
	}
	for _, c := range cases {
		if got := VideoEmbedURL(c.in); got != c.want {
			t.Errorf("VideoEmbedURL(%q) = %q, want %q", c.in, got, c.want)
		}
	}
}

func TestValidateGalleryItem(t *testing.T) {
	good := func() *domain.GalleryItem {
		return &domain.GalleryItem{Kind: domain.GalleryPhoto, Title: "Baby Spa", ImageURL: "https://example.com/a.jpg"}
	}
	if err := ValidateGalleryItem(good()); err != nil {
		t.Errorf("valid photo: %v", err)
	}
	for name, mutate := range map[string]func(*domain.GalleryItem){
		"bad kind":        func(g *domain.GalleryItem) { g.Kind = "gif" },
		"short title":     func(g *domain.GalleryItem) { g.Title = "x" },
		"photo no media":  func(g *domain.GalleryItem) { g.ImageURL = "" },
		"photo bad link":  func(g *domain.GalleryItem) { g.ImageURL = "notaurl" },
		"video needs url": func(g *domain.GalleryItem) { g.Kind = domain.GalleryVideo },
		"video bad host":  func(g *domain.GalleryItem) { g.Kind = domain.GalleryVideo; g.VideoURL = "https://evil.com/v.mp4" },
	} {
		g := good()
		mutate(g)
		if err := ValidateGalleryItem(g); err == nil {
			t.Errorf("%s: expected error, got nil", name)
		}
	}
	// Video clears photo fields; photo clears video fields.
	v := good()
	v.Kind = domain.GalleryVideo
	v.VideoURL = "https://youtu.be/dQw4w9WgXcQ"
	if err := ValidateGalleryItem(v); err != nil {
		t.Fatalf("valid video: %v", err)
	}
	if v.ImageURL != "" {
		t.Errorf("video kept photo fields: %q", v.ImageURL)
	}
	p := good()
	p.VideoURL = "https://youtu.be/dQw4w9WgXcQ"
	if err := ValidateGalleryItem(p); err != nil {
		t.Fatalf("photo with stray video url: %v", err)
	}
	if p.VideoURL != "" {
		t.Errorf("photo kept video fields: %q", p.VideoURL)
	}
	if !strings.HasPrefix(VideoEmbedURL("https://drive.google.com/file/d/1aB2cD3eF4gH5iJ6kL7mN8oP/view"), "https://") {
		t.Errorf("embed URL must be https")
	}
}
