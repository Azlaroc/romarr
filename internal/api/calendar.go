package api

import (
	"net/http"
	"strconv"
)

// calendarEntry represents a game release.
type calendarEntry struct {
	ID              int      `json:"id"`
	Name            string   `json:"name"`
	ReleaseDate     string   `json:"release_date"`
	Platforms       []string `json:"platforms"`
	BackgroundImage string   `json:"background_image,omitempty"`
	Rating          float64  `json:"rating"`
	OnWishlist      bool     `json:"on_wishlist"`
}

// The calendar has no metadata provider wired up: the RAWG backend was
// removed with the rest of the fork inheritance, and the replacement
// provider integration lands with the metadata-providers work. Both
// handlers keep their response contract and serve empty lists so the UI
// can show an honest empty state.

// handleCalendar handles GET /api/calendar — upcoming game releases.
func (s *Server) handleCalendar(w http.ResponseWriter, r *http.Request) {
	writeCalendarEmpty(w, r)
}

// handleCalendarRecent handles GET /api/calendar/recent — recently released games.
func (s *Server) handleCalendarRecent(w http.ResponseWriter, r *http.Request) {
	writeCalendarEmpty(w, r)
}

func writeCalendarEmpty(w http.ResponseWriter, r *http.Request) {
	days, _ := strconv.Atoi(r.URL.Query().Get("days"))
	if days <= 0 {
		days = 30
	}
	if days > 365 {
		days = 365
	}
	writeJSON(w, http.StatusOK, map[string]interface{}{
		"success": true,
		"entries": []calendarEntry{},
		"total":   0,
		"days":    days,
	})
}
