package handlers

import (
	"net/http"
	"net/url"
	"strconv"
	"strings"

	"spa-booking/internal/domain"

	"github.com/go-chi/chi/v5"
	"github.com/google/uuid"
)

// FoodPage renders the public Bu Nah menu, grouped into food + beverages.
func (s *Server) FoodPage(w http.ResponseWriter, r *http.Request) {
	items, err := s.food.ListActive(r.Context())
	if err != nil {
		http.Error(w, "failed to load menu", http.StatusInternalServerError)
		return
	}
	var foods, beverages []domain.FoodItem
	for _, f := range items {
		if f.Category == domain.FoodCategoryBeverage {
			beverages = append(beverages, f)
		} else {
			foods = append(foods, f)
		}
	}
	s.render(w, r, "food.html", pageData{
		"Foods": foods, "Beverages": beverages,
		"Error": r.URL.Query().Get("error"),
	})
}

func parseFoodForm(r *http.Request) (domain.FoodItem, string) {
	var f domain.FoodItem
	if err := r.ParseMultipartForm(10 << 20); err != nil {
		if err := r.ParseForm(); err != nil {
			return f, "Could not read form"
		}
	}
	f.Category = domain.FoodCategory(strings.TrimSpace(r.FormValue("category")))
	f.Name = r.FormValue("name")
	f.Description = r.FormValue("description")
	if v := strings.TrimSpace(r.FormValue("price_m")); v != "" {
		if p, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.PriceM = &p
		} else {
			return f, "Price M must be a number"
		}
	}
	if v := strings.TrimSpace(r.FormValue("price_l")); v != "" {
		if p, err := strconv.ParseInt(v, 10, 64); err == nil {
			f.PriceL = &p
		} else {
			return f, "Price L must be a number"
		}
	}
	f.Variants = r.FormValue("variants")
	f.PreOrder = r.FormValue("pre_order") == "1"
	if v := strings.TrimSpace(r.FormValue("sort")); v != "" {
		if n, err := strconv.Atoi(v); err == nil {
			f.Sort = n
		}
	}
	f.ImageURL = strings.TrimSpace(r.FormValue("image_url"))
	return f, ""
}

// PortalFood lists every menu row for CS/admin management.
func (s *Server) PortalFood(w http.ResponseWriter, r *http.Request) {
	items, _ := s.food.ListAll(r.Context())
	s.render(w, r, "portal_food.html", pageData{
		"Items": items,
		"Error": r.URL.Query().Get("error"),
	})
}

// PortalCreateFood adds a menu row (photo upload or link).
func (s *Server) PortalCreateFood(w http.ResponseWriter, r *http.Request) {
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	f, errMsg := parseFoodForm(r)
	if errMsg != "" {
		http.Redirect(w, r, "/portal/food?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "food")
		if msg != "" {
			http.Redirect(w, r, "/portal/food?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		f.ImagePath = rel
	}
	if err := s.food.Save(r.Context(), &f); err != nil {
		http.Redirect(w, r, "/portal/food?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/food?toast="+url.QueryEscape("Menu item added")+"&kind=success", http.StatusSeeOther)
}

// PortalEditFoodForm renders the menu-row editor.
func (s *Server) PortalEditFoodForm(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	f, err := s.food.ByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	s.render(w, r, "portal_food_edit.html", pageData{
		"Item":  f,
		"Error": r.URL.Query().Get("error"),
	})
}

// PortalUpdateFood saves menu-row edits.
func (s *Server) PortalUpdateFood(w http.ResponseWriter, r *http.Request) {
	id, err := uuid.Parse(chi.URLParam(r, "id"))
	if err != nil {
		http.NotFound(w, r)
		return
	}
	existing, err := s.food.ByID(r.Context(), id)
	if err != nil {
		http.NotFound(w, r)
		return
	}
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	f, errMsg := parseFoodForm(r)
	if errMsg != "" {
		http.Redirect(w, r, "/portal/food/"+id.String()+"/edit?error="+url.QueryEscape(errMsg), http.StatusSeeOther)
		return
	}
	f.ID = id
	f.Active = existing.Active
	f.ImagePath = existing.ImagePath
	if file, header, ferr := r.FormFile("photo"); ferr == nil && header.Size > 0 {
		rel, msg := s.storeUpload(file, file, header.Filename, header.Size, "food")
		if msg != "" {
			http.Redirect(w, r, "/portal/food/"+id.String()+"/edit?error="+url.QueryEscape(msg), http.StatusSeeOther)
			return
		}
		f.ImagePath = rel
	}
	if err := s.food.Save(r.Context(), &f); err != nil {
		http.Redirect(w, r, "/portal/food/"+id.String()+"/edit?error="+url.QueryEscape(err.Error()), http.StatusSeeOther)
		return
	}
	http.Redirect(w, r, "/portal/food?toast="+url.QueryEscape("Menu item updated")+"&kind=success", http.StatusSeeOther)
}

// PortalToggleFood flips a row's visibility on the public page.
func (s *Server) PortalToggleFood(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.food.ToggleActive(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/food?toast="+url.QueryEscape("Menu updated")+"&kind=success", http.StatusSeeOther)
}

// PortalDeleteFood removes a menu row permanently.
func (s *Server) PortalDeleteFood(w http.ResponseWriter, r *http.Request) {
	_ = r.ParseForm()
	if !s.requireCSRF(r) {
		http.Error(w, "invalid", http.StatusBadRequest)
		return
	}
	if id, err := uuid.Parse(chi.URLParam(r, "id")); err == nil {
		_ = s.food.Delete(r.Context(), id)
	}
	http.Redirect(w, r, "/portal/food?toast="+url.QueryEscape("Menu item removed")+"&kind=success", http.StatusSeeOther)
}

// ServeFoodPhoto serves uploaded menu photos (public, traversal-safe).
func (s *Server) ServeFoodPhoto(w http.ResponseWriter, r *http.Request) {
	s.serveUploadDir("food")(w, r)
}
