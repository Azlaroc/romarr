package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"gamarr/internal/datname"
	"gamarr/internal/db"
	"gamarr/internal/romfile"
)

// ── Game detail ────────────────────────────────────────────────────────────────
//
// GET /api/library/{id} is the game-detail read: everything the screen shows
// comes from the DB — stored hashes, the active catalog, the row itself.
// Deliberately ZERO filesystem I/O: ~19K rows have no stored measurement by
// design (the scanner never pays extraction to answer a browse), and this
// endpoint honors that. "Verify now" is the explicit payment.

type detailHashes struct {
	// Domain says which family of stored hashes is being shown: "gamarr"
	// (our own measurement) or "romm" (frozen legacy sync identity). Both
	// are inner-content hashes; the distinction is provenance, not meaning.
	Domain      string               `json:"domain,omitempty"`
	CRC         string               `json:"crc,omitempty"`
	MD5         string               `json:"md5,omitempty"`
	SHA1        string               `json:"sha1,omitempty"`
	HashedAt    string               `json:"hashed_at,omitempty"`
	Unh         *db.UnheaderedHashes `json:"unh,omitempty"`
	HashSkipped string               `json:"hash_skipped,omitempty"`
}

type detailCanonical struct {
	// Outcome: "resolved" (one canonical name), "nomatch" (hashed,
	// uncatalogued), "ambiguous" (a human call), "unhashed" (no stored
	// measurement — not evidence of anything).
	Outcome  string   `json:"outcome"`
	Name     string   `json:"name,omitempty"` // canonical file name, extension included
	GameName string   `json:"game_name,omitempty"`
	Stems    []string `json:"stems,omitempty"` // the tie, when ambiguous
}

type detailDatGame struct {
	db.DatGameRow
	// IsCurrent marks the family row the stored hashes resolve to. Row ids
	// here are snapshot-scoped — drill into roms with them, never persist.
	IsCurrent bool `json:"is_current"`
	// IsOverride marks the dump the operator pinned ("That!") — the one an
	// enforced want will wait for.
	IsOverride bool `json:"is_override"`
}

type detailProfile struct {
	ID           int64  `json:"id"` // the override; 0 = platform default
	ResolvedID   int64  `json:"resolved_id"`
	ResolvedName string `json:"resolved_name"`
	ResolvedFrom string `json:"resolved_from"` // "title" | "platform"
}

type detailIGDB struct {
	Year   int      `json:"year,omitempty"`
	Genres []string `json:"genres,omitempty"`
}

func (s *Server) handleLibraryDetail(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid library id")
		return
	}
	item, err := s.mgr.Jobs().GetLibraryItem(id)
	if err != nil {
		writeError(w, 404, "Library item not found")
		return
	}

	hashes := detailHashesOf(item)
	canonical, _ := s.resolveCanonical(item)

	// The clone family when the catalog knows the game; otherwise a
	// title-text browse, which is what lets the picker FIX an absent or
	// wrong match rather than only confirm a good one.
	jobs := s.mgr.Jobs()
	var family []db.DatGameRow
	if canonical.GameName != "" {
		family = jobs.DatGameFamily(item.PlatformSlug, canonical.GameName)
	}
	if len(family) == 0 && item.Title != "" {
		family, _ = jobs.BrowseDatGames(db.DatGameQuery{
			PlatformSlug: item.PlatformSlug, Text: item.Title, Limit: 50,
		})
	}
	family = collapseFamilyHeaderTwins(jobs, family)
	overrideName := ""
	var overrideWish int64
	if ov, ok := jobs.GetWishlistOverrideForTitle(item.Title, item.PlatformSlug); ok {
		overrideName, overrideWish = ov.OverrideDumpName, ov.ID
	}
	group := make([]detailDatGame, 0, len(family))
	for _, g := range family {
		group = append(group, detailDatGame{DatGameRow: g,
			IsCurrent:  g.Name == canonical.GameName,
			IsOverride: overrideName != "" && g.Name == overrideName})
	}

	resolved := jobs.ResolveProfileForItem(item.ProfileID, item.PlatformSlug)
	profile := detailProfile{ID: item.ProfileID, ResolvedFrom: "platform"}
	if resolved != nil {
		profile.ResolvedID = resolved.ID
		profile.ResolvedName = resolved.Name
		if item.ProfileID > 0 && resolved.ID == item.ProfileID {
			profile.ResolvedFrom = "title"
		}
	}

	resp := map[string]interface{}{
		"item":      item,
		"hashes":    hashes,
		"canonical": canonical,
		"dat_group": group,
		"profile":   profile,
		"set":       nil,
		"igdb":      nil,
		"override":  nil,
	}
	if overrideName != "" {
		resp["override"] = map[string]interface{}{
			"dump_name": overrideName, "wishlist_id": overrideWish,
		}
	}
	if mk, ok := db.ParseSetMarker(item.Metadata); ok {
		resp["set"] = mk
	}
	if ig := parseIGDBMeta(item.Metadata); ig != nil {
		resp["igdb"] = ig
	}
	writeJSON(w, 200, resp)
}

func detailHashesOf(item *db.LibraryItem) detailHashes {
	h := detailHashes{HashSkipped: db.ParseHashSkip(item.Metadata)}
	if g, ok := db.ParseGamarrHashes(item.Metadata); ok {
		h.Domain, h.CRC, h.MD5, h.SHA1, h.HashedAt, h.Unh = "gamarr", g.CRC, g.MD5, g.SHA1, g.HashedAt, g.Unh
		return h
	}
	if crc, md5, sha1, ok := db.ParseRommContentHashes(item.Metadata); ok {
		h.Domain, h.CRC, h.MD5, h.SHA1 = "romm", crc, md5, sha1
	}
	return h
}

// resolveCanonical runs the stored-hash ladder (db.LookupDatMatchesForItem —
// shared with the retitle runner) through the catalog and the shared name
// resolver.
func (s *Server) resolveCanonical(item *db.LibraryItem) (detailCanonical, []db.DatRomMatch) {
	matches, hashed := s.mgr.Jobs().LookupDatMatchesForItem(item)

	switch {
	case !hashed:
		return detailCanonical{Outcome: "unhashed"}, nil
	case len(matches) == 0:
		return detailCanonical{Outcome: "nomatch"}, nil
	}

	cands := make([]datname.Candidate, 0, len(matches))
	for _, m := range matches {
		cands = append(cands, datname.Candidate{RomName: m.RomName, GameName: m.GameName})
	}
	res := datname.Resolve(cands)
	switch res.Outcome {
	case datname.Resolved:
		name := res.Stem + res.Ext
		if res.Ext == ".unh" {
			// .unh names a hash domain, not a file format — it must never
			// surface as a name. The stem alone IS the canonical name.
			name = res.Stem
		}
		return detailCanonical{Outcome: "resolved", Name: name, GameName: res.GameName}, matches
	case datname.Ambiguous:
		return detailCanonical{Outcome: "ambiguous", Stems: res.Stems}, matches
	}
	return detailCanonical{Outcome: "nomatch"}, matches
}

// collapseFamilyHeaderTwins drops the catalog's headered/headerless twin
// artifact from a display family: two games sharing one name where exactly
// one is a single-rom .unh entry are ONE dump, and showing both makes the
// "current" flag ambiguous (nes: every game appears twice). Same narrowness
// as collectionsvc's twin collapse — exactly two same-named rows, the .unh
// side single-rom; anything else is a real tie and stays visible.
func collapseFamilyHeaderTwins(jobs *db.JobStore, family []db.DatGameRow) []db.DatGameRow {
	byName := map[string][]int{}
	for i, g := range family {
		byName[g.Name] = append(byName[g.Name], i)
	}
	drop := map[int]bool{}
	for _, idxs := range byName {
		if len(idxs) != 2 {
			continue
		}
		unh := -1
		for _, i := range idxs {
			roms := jobs.DatGameRoms(family[i].ID)
			if len(roms) == 1 && strings.HasSuffix(strings.ToLower(roms[0].Name), ".unh") {
				if unh != -1 {
					unh = -1 // both .unh: not the twin shape, leave alone
					break
				}
				unh = i
			}
		}
		if unh >= 0 {
			drop[unh] = true
		}
	}
	if len(drop) == 0 {
		return family
	}
	out := family[:0]
	for i, g := range family {
		if !drop[i] {
			out = append(out, g)
		}
	}
	return out
}

func parseIGDBMeta(metadata string) *detailIGDB {
	var envelope struct {
		Gamarr struct {
			IGDB *detailIGDB `json:"igdb"`
		} `json:"gamarr"`
	}
	if err := json.Unmarshal([]byte(metadata), &envelope); err != nil || envelope.Gamarr.IGDB == nil {
		return nil
	}
	return envelope.Gamarr.IGDB
}

// handleUpdateLibraryItem changes one row's profile override — the same
// contract as handleUpdateWishlist: 0 clears, templates are refused.
func (s *Server) handleUpdateLibraryItem(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid library id")
		return
	}
	var req struct {
		ProfileID *int64 `json:"profile_id"`
	}
	if !decodeJSONBody(w, r, &req) {
		return
	}
	if req.ProfileID == nil {
		writeError(w, 400, "profile_id required")
		return
	}
	if *req.ProfileID > 0 {
		p, err := s.mgr.Jobs().GetQualityProfile(*req.ProfileID)
		if err != nil || p == nil || p.IsTemplate {
			writeError(w, 400, "Unknown quality profile")
			return
		}
	}
	ok, err := s.mgr.Jobs().SetLibraryItemProfile(id, *req.ProfileID)
	if err != nil {
		writeError(w, 500, err.Error())
		return
	}
	if !ok {
		writeError(w, 404, "Library item not found")
		return
	}
	writeJSON(w, 200, map[string]interface{}{"success": true, "id": id, "profile_id": *req.ProfileID})
}

// ── Verify now ─────────────────────────────────────────────────────────────────

// handleLibraryVerify is the explicit payment for a verdict: measure this
// one file, bank its hashes, ask the catalog. 202 — the caller polls the
// detail read for the verdict landing.
func (s *Server) handleLibraryVerify(w http.ResponseWriter, r *http.Request) {
	id, err := strconv.ParseInt(chi.URLParam(r, "id"), 10, 64)
	if err != nil || id <= 0 {
		writeError(w, 400, "Invalid library id")
		return
	}
	item, err := s.mgr.Jobs().GetLibraryItem(id)
	if err != nil {
		writeError(w, 404, "Library item not found")
		return
	}
	if skip := db.ParseHashSkip(item.Metadata); skip != "" {
		writeError(w, 409, "This entry cannot be hashed: "+skip)
		return
	}
	s.verifyMu.Lock()
	if s.verifying[id] {
		s.verifyMu.Unlock()
		writeError(w, 409, "Verification already running for this item")
		return
	}
	s.verifying[id] = true
	s.verifyMu.Unlock()

	go s.runVerify(*item)
	writeJSON(w, 202, map[string]interface{}{"started": true, "id": id})
}

func (s *Server) runVerify(item db.LibraryItem) {
	defer func() {
		s.verifyMu.Lock()
		delete(s.verifying, item.ID)
		s.verifyMu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Minute)
	defer cancel()
	jobs := s.mgr.Jobs()

	// Per-item scratch dir: concurrent verifies must not reap each other's
	// extractions the way a whole-run work root could. Dot-prefixed so
	// library scans never see it; on the roms volume so the free-space
	// check measures the right filesystem.
	workRoot := filepath.Join(s.cfg.GamesRomsPath, ".gamarr-verify-tmp", fmt.Sprintf("item-%d", item.ID))
	defer os.RemoveAll(workRoot)

	res, err := romfile.Measure(ctx, item.FilePath, workRoot)
	if err != nil {
		var multi *romfile.MultiFileError
		marker := ""
		switch {
		case errors.Is(err, romfile.ErrIsDirectory):
			marker = db.HashSkipDirectory
		case errors.As(err, &multi):
			marker = db.HashSkipMultiFile
		case errors.Is(err, romfile.ErrRarArchive):
			marker = db.HashSkipRar
		}
		if marker != "" {
			// Self-heal: the precondition check could not know until the
			// file was opened. Mark it so the next attempt is refused
			// up-front with the reason.
			if merr := jobs.MarkLibraryHashSkipped(item.ID, marker); merr != nil {
				slog.Warn("verify: mark hash skip", "library_id", item.ID, "error", merr)
			}
			jobs.LogActivity("verify", item.Title, "cannot hash: "+marker, "", &item.ID)
			return
		}
		jobs.LogActivity("verify", item.Title, "error: "+err.Error(), "", &item.ID)
		return
	}

	h := db.LibraryHashes{CRC: res.CRC, MD5: res.MD5, SHA1: res.SHA1}
	if res.Stripped {
		h.Unh = &db.UnheaderedHashes{
			CRC: res.Payload.CRC, MD5: res.Payload.MD5, SHA1: res.Payload.SHA1,
			Header: res.HeaderKind,
		}
	}
	if err := jobs.SaveLibraryHashes(item.ID, h); err != nil {
		jobs.LogActivity("verify", item.Title, "error: save failed: "+err.Error(), "", &item.ID)
		return
	}

	// The double ask the scanner uses: whole-file first, payload rescue for
	// headered formats — a payload hit upgrades, a payload miss never
	// downgrades the whole-file answer.
	name := filepath.Base(item.FilePath)
	v := jobs.MatchDatRom(item.PlatformSlug, name, res.CRC, res.MD5, res.SHA1)
	if v.Status != db.CatalogVerified && !res.Payload.Zero() {
		if pv := jobs.MatchDatRom(item.PlatformSlug, name, res.Payload.CRC, res.Payload.MD5, res.Payload.SHA1); pv.Status == db.CatalogVerified {
			v = pv
		}
	}
	jobs.SetLibraryCatalogStatusByID(item.ID, v.Status)

	detail := "catalog: " + v.Status
	if v.Status == db.CatalogMismatch && v.Expected != "" {
		detail += " (expected " + v.Expected + ", got " + v.Got + ")"
	}
	jobs.LogActivity("verify", item.Title, detail, "", &item.ID)
}
