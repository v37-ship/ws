package service

import (
	"context"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"strings"
	"unicode/utf8"

	"spa-booking/internal/domain"
	"spa-booking/internal/repo"

	"github.com/google/uuid"
)

type GalleryService struct {
	repo *repo.GalleryRepo
}

func NewGalleryService(r *repo.GalleryRepo) *GalleryService { return &GalleryService{repo: r} }

func (s *GalleryService) ListActive(ctx context.Context) ([]domain.GalleryItem, error) {
	return s.repo.ListActive(ctx)
}

func (s *GalleryService) ListAll(ctx context.Context) ([]domain.GalleryItem, error) {
	return s.repo.ListAll(ctx)
}

func (s *GalleryService) ByID(ctx context.Context, id uuid.UUID) (*domain.GalleryItem, error) {
	return s.repo.ByID(ctx, id)
}

// Save validates and inserts or updates a gallery row. Error strings stay
// English like the other service .Error messages.
func (s *GalleryService) Save(ctx context.Context, g *domain.GalleryItem) error {
	if err := ValidateGalleryItem(g); err != nil {
		return err
	}
	if g.ID == uuid.Nil {
		g.Active = true
		if sort, err := s.repo.NextSort(ctx); err == nil {
			g.Sort = sort
		}
		return s.repo.Create(ctx, g)
	}
	return s.repo.Update(ctx, g)
}

func (s *GalleryService) ToggleActive(ctx context.Context, id uuid.UUID) error {
	return s.repo.ToggleActive(ctx, id)
}

func (s *GalleryService) Delete(ctx context.Context, id uuid.UUID) error {
	return s.repo.Delete(ctx, id)
}

func (s *GalleryService) SetSort(ctx context.Context, id uuid.UUID, sort int) error {
	return s.repo.SetSort(ctx, id, sort)
}

func ValidateGalleryItem(g *domain.GalleryItem) error {
	if g.Kind != domain.GalleryPhoto && g.Kind != domain.GalleryVideo {
		return fmt.Errorf("%w: kind must be photo or video", ErrValidation)
	}
	title := strings.TrimSpace(g.Title)
	if utf8.RuneCountInString(title) < 2 || utf8.RuneCountInString(title) > 100 {
		return fmt.Errorf("%w: title must be 2-100 characters", ErrValidation)
	}
	g.Title = title
	g.Caption = strings.TrimSpace(g.Caption)
	if utf8.RuneCountInString(g.Caption) > 500 {
		return fmt.Errorf("%w: caption must be under 500 characters", ErrValidation)
	}
	g.ImagePath = strings.TrimSpace(g.ImagePath)
	if err := ValidateImageURL(g.ImageURL); err != nil {
		return err
	}
	g.ImageURL = strings.TrimSpace(g.ImageURL)
	g.ThumbPath = strings.TrimSpace(g.ThumbPath)
	if err := ValidateImageURL(g.ThumbURL); err != nil {
		return err
	}
	g.ThumbURL = strings.TrimSpace(g.ThumbURL)
	g.VideoURL = strings.TrimSpace(g.VideoURL)
	if g.IsVideo() {
		// Videos are links only: clear any photo fields, require a good link.
		g.ImagePath, g.ImageURL = "", ""
		if err := ValidateVideoURL(g.VideoURL); err != nil {
			return err
		}
	} else {
		// Photos ignore video fields; one photo source is required.
		g.VideoURL = ""
		if g.ImagePath == "" && g.ImageURL == "" {
			return fmt.Errorf("%w: upload or link a photo", ErrValidation)
		}
	}
	return nil
}

var (
	driveID   = regexp.MustCompile(`^[-\w]{10,}$`)
	youtubeID = regexp.MustCompile(`^[-\w]{11}$`)
	vimeoID   = regexp.MustCompile(`^\d+$`)
)

// ValidateVideoURL accepts Google Drive, YouTube, or Vimeo links only.
func ValidateVideoURL(raw string) error {
	if VideoEmbedURL(raw) == "" {
		if strings.TrimSpace(raw) == "" {
			return fmt.Errorf("%w: video link required", ErrValidation)
		}
		return fmt.Errorf("%w: video link must be Google Drive, YouTube or Vimeo", ErrValidation)
	}
	return nil
}

// VideoEmbedURL normalizes a share/watch link to its embed player URL, or ""
// when the host is not allowlisted. Pure (no errors) so templates can call it.
func VideoEmbedURL(raw string) string {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return ""
	}
	u, err := url.Parse(raw)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return ""
	}
	host := strings.ToLower(u.Hostname())
	seg := func() []string {
		var out []string
		for _, s := range strings.Split(strings.Trim(u.EscapedPath(), "/"), "/") {
			if s != "" {
				out = append(out, s)
			}
		}
		return out
	}
	switch host {
	case "drive.google.com":
		p := seg()
		// /file/d/<id>/... (preview, view, edit variants all work)
		for i, s := range p {
			if s == "d" && i+1 < len(p) && driveID.MatchString(p[i+1]) {
				return "https://drive.google.com/file/d/" + p[i+1] + "/preview"
			}
		}
		// /open?id=<id>, /uc?id=<id>
		if id := u.Query().Get("id"); driveID.MatchString(id) {
			return "https://drive.google.com/file/d/" + id + "/preview"
		}
		return ""
	case "www.youtube.com", "youtube.com", "m.youtube.com", "www.youtube-nocookie.com", "youtube-nocookie.com":
		p := seg()
		if len(p) >= 2 && (p[0] == "embed" || p[0] == "shorts" || p[0] == "live") && youtubeID.MatchString(p[1]) {
			return "https://www.youtube-nocookie.com/embed/" + p[1]
		}
		if id := u.Query().Get("v"); youtubeID.MatchString(id) {
			return "https://www.youtube-nocookie.com/embed/" + id
		}
		return ""
	case "youtu.be":
		if p := seg(); len(p) == 1 && youtubeID.MatchString(p[0]) {
			return "https://www.youtube-nocookie.com/embed/" + p[0]
		}
		return ""
	case "vimeo.com", "player.vimeo.com":
		// vimeo.com/<id> or player.vimeo.com/video/<id>
		if p := seg(); len(p) >= 1 && vimeoID.MatchString(p[len(p)-1]) &&
			(len(p) == 1 || p[len(p)-2] == "video") {
			return "https://player.vimeo.com/video/" + p[len(p)-1]
		}
		return ""
	default:
		return ""
	}
}

// ResolveGalleryPhoto picks the display picture: local upload first, then
// internet link, then "" (templates skip the img).
func ResolveGalleryPhoto(g domain.GalleryItem) string {
	if g.ImagePath != "" {
		return "/uploads/gallery/" + path.Base(g.ImagePath)
	}
	return strings.TrimSpace(g.ImageURL)
}

// ResolveGalleryThumb picks the video poster the same way ("" = play tile).
func ResolveGalleryThumb(g domain.GalleryItem) string {
	if g.ThumbPath != "" {
		return "/uploads/gallery/" + path.Base(g.ThumbPath)
	}
	return strings.TrimSpace(g.ThumbURL)
}
