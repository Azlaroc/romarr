package collectionsvc

import (
	"path/filepath"
	"strings"

	"gamarr/internal/db"
)

// Headered and headerless twins.
//
// No-Intro publishes NES twice. "10-Yard Fight (USA, Europe).nes" carries a
// 16-byte iNES header No-Intro generates; "10-Yard Fight (USA, Europe).unh"
// is the same dump without it. Both arrive as SEPARATE catalog games with
// IDENTICAL names and one rom each — the catalog has no field saying they are
// the same thing, and neither does the parser.
//
// Left alone, a nes group carries eight members for four dumps, and the two
// halves of each dump compete for the keeper slot. Measured over 200 live
// groups, the headered twin wins 114 of 115 — it sorts first and every
// tie-break above the name is equal. That is fine while ownership is decided
// by parsed titles, which cannot tell the twins apart either.
//
// It stops being fine the moment hashes arrive. Our nes library is headered
// with different header bytes, so its files match the .unh rom and never the
// .nes one. The hash tier runs first and claims each file for the LOSING
// twin; the keeper is then left unmatched and the title tier cannot rescue it
// because the file is already claimed. Every such group flips from owned to a
// gap sitting next to a surplus row — a library that got MORE precise reading
// as one that lost 700 games.
//
// So the twins are collapsed here, at the seam where catalog rows become set
// members: one member, both roms, both hashes offered to the ownership tier.

// unheaderedExt is No-Intro's extension for the header-stripped publication.
const unheaderedExt = ".unh"

// collapseHeaderTwins merges each headered/headerless pair into one member.
//
// Deliberately narrow: a pair is exactly two same-named games with one rom
// each, sharing a rom stem, where exactly one rom is .unh. Anything else —
// three same-named games, differing stems, two .unh, a multi-rom game — is
// left alone, because the catalog offers no evidence those are one dump and
// merging them would silently drop a real one.
//
// The headered member supplies the scalar fields: its total_size is the size
// of the file convention the library actually holds.
func collapseHeaderTwins(rows []db.DatSetMember) []db.DatSetMember {
	byName := map[string][]int{}
	for i, r := range rows {
		byName[r.Name] = append(byName[r.Name], i)
	}

	merged := map[int]bool{} // indices folded into another member
	extra := map[int][]db.DatRomRow{}
	for _, idxs := range byName {
		if len(idxs) != 2 {
			continue
		}
		a, b := rows[idxs[0]], rows[idxs[1]]
		if len(a.Roms) != 1 || len(b.Roms) != 1 {
			continue
		}
		headered, headerless := idxs[0], idxs[1]
		if isUnheadered(a.Roms[0].Name) == isUnheadered(b.Roms[0].Name) {
			continue // both or neither: no evidence of a twin
		}
		if isUnheadered(a.Roms[0].Name) {
			headered, headerless = idxs[1], idxs[0]
		}
		if romStem(a.Roms[0].Name) != romStem(b.Roms[0].Name) {
			continue
		}
		merged[headerless] = true
		extra[headered] = append(extra[headered], rows[headerless].Roms...)
	}

	if len(merged) == 0 {
		return rows
	}
	out := make([]db.DatSetMember, 0, len(rows)-len(merged))
	for i, r := range rows {
		if merged[i] {
			continue
		}
		if add := extra[i]; len(add) > 0 {
			r.Roms = append(append([]db.DatRomRow{}, r.Roms...), add...)
		}
		out = append(out, r)
	}
	return out
}

func isUnheadered(romName string) bool {
	return strings.EqualFold(filepath.Ext(romName), unheaderedExt)
}

func romStem(romName string) string {
	return strings.TrimSuffix(romName, filepath.Ext(romName))
}
