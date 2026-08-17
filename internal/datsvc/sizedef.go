package datsvc

import (
	"log/slog"

	"gamarr/internal/db"
)

// Turning a catalog's size distribution into the band the selector enforces.
//
// The numbers written here are the numbers that reject, and the same numbers
// a screen displays. Nothing widens them at the point of use, which is why
// both allowances live in this one place:
//
//   - sizeFloorDivisor is the compression allowance. DAT sizes measure the
//     game inside the archive while a candidate is the archive, so a floor
//     taken straight from the catalog would reject nearly every real release.
//     Eight is generous on purpose: the floor exists to catch placeholder
//     files, and being wrong in the strict direction means a title silently
//     never appears, which is far worse than letting a small oddity through
//     to be scored on its merits.
//
//   - sizeCeilingFactor is the headroom above the 99th percentile. Anything
//     legitimately larger than a platform's biggest dumps is rare; anything
//     far larger is usually a bundle or a mislabelled release.
const (
	sizeFloorDivisor  = 8
	sizeCeilingFactor = 2
)

// DeriveSizeDefinition turns an active snapshot into a platform's size
// definition. Each end is decided independently: an end the catalog cannot
// speak to is left at zero, which means unbounded, rather than filled with a
// guess.
//
// The floor is dropped entirely when the 1st and 99th percentiles coincide.
// That happens when every entry in a catalog is the same size — every
// GameCube disc is exactly 1,459,978,240 bytes — and at that point the
// percentile is just the median repeated back, carrying no information about
// where the small end of the distribution is. Deriving a floor from it would
// reject heavily compressed images of perfectly ordinary games. The ceiling
// stays, because a flat catalog still says plenty about how large is too
// large.
func DeriveSizeDefinition(snap db.DatSnapshotRow) db.PlatformSizeRow {
	def := db.PlatformSizeRow{
		PlatformSlug:    snap.PlatformSlug,
		Source:          db.SizeSourceCatalog,
		SnapshotVersion: snap.Version,
	}
	if snap.SizeP99 > 0 {
		def.MaxSize = snap.SizeP99 * sizeCeilingFactor
	}
	if snap.SizeP01 > 0 && snap.SizeP01 != snap.SizeP99 {
		def.MinSize = snap.SizeP01 / sizeFloorDivisor
	}
	return def
}

// applySizeDefinition refreshes a platform's derived band after an import.
//
// A definition an operator set by hand is never overwritten — that is what
// makes the table an override surface rather than a cache, and it is the
// escape hatch when a catalog is wrong about a platform.
func (s *Service) applySizeDefinition(snap db.DatSnapshotRow) {
	slug := snap.PlatformSlug
	if existing, ok := s.store.GetPlatformSize(slug); ok && existing.Source == db.SizeSourceManual {
		return
	}

	def := DeriveSizeDefinition(snap)
	if def.MinSize == 0 && def.MaxSize == 0 {
		// A sizeless catalog says nothing about this platform. Drop the row
		// instead of storing zeroes so the absence of an opinion reads as an
		// absence rather than as a band that happens to permit everything.
		if err := s.store.DeletePlatformSize(slug); err != nil {
			slog.Warn("clear size definition", "platform", slug, "error", err)
		}
		return
	}
	if err := s.store.SetPlatformSize(def); err != nil {
		slog.Warn("store size definition", "platform", slug, "error", err)
	}
}
