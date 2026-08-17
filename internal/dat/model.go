// Package dat parses preservation-community DAT catalogs — the authoritative
// record of which dumps exist for a platform, published by No-Intro (carts),
// Redump (discs), MAME (arcade) and TOSEC (home computers).
//
// Two on-disk formats cover every authority we consume, so both parsers are
// required rather than speculative: Redump publishes logiqx XML, while the
// libretro-database mirror republishes No-Intro in the older clrmamepro
// parenthesised syntax. Parse sniffs which one it holds.
//
// The parsed catalog is scoring and cataloguing input only. Nothing here is a
// trust signal: a candidate download is trusted solely by the post-extract
// hash gate.
package dat

import (
	"sort"
	"strings"
)

// Rom is one file belonging to a dumped game. A cartridge game has exactly
// one; a disc game has a .cue plus one .bin per track, which is why per-game
// size is a sum rather than a single field.
//
// Hashes are normalised to lowercase hex on parse — authorities disagree on
// case (libretro emits uppercase, Redump lowercase) and these columns are
// join keys for later stories.
type Rom struct {
	Name   string `json:"name"`
	Size   int64  `json:"size"`
	CRC    string `json:"crc,omitempty"`
	MD5    string `json:"md5,omitempty"`
	SHA1   string `json:"sha1,omitempty"`
	Serial string `json:"serial,omitempty"`
}

// Game is one catalogued dump: the canonical DAT name plus the attributes
// that name encodes. Region/Languages/Revision/Flags are derived by the
// shared release-name parser so the DAT layer and the selection engine agree
// on one vocabulary; an explicit region attribute in the DAT wins when present.
type Game struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
	CloneOf     string `json:"clone_of,omitempty"`
	Region      string `json:"region,omitempty"`
	Languages   string `json:"languages,omitempty"`
	Revision    int    `json:"revision,omitempty"`
	Flags       string `json:"flags,omitempty"`
	BareTitle   string `json:"bare_title,omitempty"`
	Roms        []Rom  `json:"roms,omitempty"`
}

// TotalSize is the sum of the game's rom sizes — the true dump size, and the
// value the per-platform size bands are derived from.
func (g Game) TotalSize() int64 {
	var n int64
	for _, r := range g.Roms {
		n += r.Size
	}
	return n
}

// Header carries the DAT's self-declared identity. Version is the snapshot
// stamp we pin on: Redump emits "2026-08-16 18-16-58", libretro "2026.08.01".
type Header struct {
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Version     string `json:"version,omitempty"`
	Date        string `json:"date,omitempty"`
}

// File is a fully parsed DAT.
type File struct {
	Header Header `json:"header"`
	Games  []Game `json:"games"`
}

// RomCount totals the rom entries across every game.
func (f *File) RomCount() int {
	n := 0
	for _, g := range f.Games {
		n += len(g.Roms)
	}
	return n
}

// SizeStats returns the per-game total-size distribution used to build the
// selector's plausibility band: the 1st and 99th percentiles, the median, and
// the observed extremes. Games with no size information are ignored so a
// partially-sized DAT cannot drag the floor to zero. ok is false when nothing
// in the file carries a size.
func (f *File) SizeStats() (stats SizeStats, ok bool) {
	sizes := make([]int64, 0, len(f.Games))
	for _, g := range f.Games {
		if n := g.TotalSize(); n > 0 {
			sizes = append(sizes, n)
		}
	}
	if len(sizes) == 0 {
		return SizeStats{}, false
	}
	sort.Slice(sizes, func(i, j int) bool { return sizes[i] < sizes[j] })
	return SizeStats{
		Min: sizes[0],
		Max: sizes[len(sizes)-1],
		P01: percentile(sizes, 0.01),
		P50: percentile(sizes, 0.50),
		P99: percentile(sizes, 0.99),
	}, true
}

// SizeStats is the per-platform size distribution derived from a snapshot.
type SizeStats struct {
	Min int64 `json:"size_min"`
	Max int64 `json:"size_max"`
	P01 int64 `json:"size_p01"`
	P50 int64 `json:"size_p50"`
	P99 int64 `json:"size_p99"`
}

// percentile indexes into an ascending slice using nearest-rank.
func percentile(sorted []int64, q float64) int64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q * float64(len(sorted)-1))
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return sorted[idx]
}

// flagList renders the boolean release attributes as a stable comma-joined
// set, so callers can store one column and match on substrings.
func flagList(f ...string) string {
	out := f[:0:0]
	for _, s := range f {
		if s != "" {
			out = append(out, s)
		}
	}
	return strings.Join(out, ",")
}
