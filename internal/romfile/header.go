package romfile

import "bytes"

// Container headers.
//
// A ROM dump is the cartridge's bytes. Some formats prepend a small
// descriptor that emulators need and the dump does not contain — the iNES
// header is the one that matters to this library. No-Intro publishes NES
// twice for exactly this reason: "<game>.nes" carries No-Intro's own header
// bytes, "<game>.unh" is the bare dump. Our NES set is a headered GoodNES-era
// set whose header bytes differ from No-Intro's, so whole-file hashes match
// nothing (measured: 0 of 25) while payload hashes match the .unh rows
// (measured: 713 of 762).
//
// Only iNES gets a rule, and that is a measurement, not an oversight: nes is
// the only platform in our catalog that ships .unh rows. Atari 7800 (.a78)
// and Lynx (.lnx) also carry container headers, but No-Intro hashes those
// WITH the header, so stripping them would break a match that works today.
const (
	// HeaderKindINES names the 16-byte iNES / NES 2.0 header.
	HeaderKindINES = "ines"

	inesHeaderLen = 16

	// headerProbeLen is how many bytes every hash pass reads up front. It
	// must be >= the longest header we recognise.
	headerProbeLen = inesHeaderLen
)

var inesMagic = []byte{'N', 'E', 'S', 0x1a}

// headerLen reports the length of the container header opening head, or 0
// when there is none. size is the whole file's size: a file that is nothing
// but a header has no payload to hash.
//
// Deliberately magic-based with no extension gate. What gets hashed here is
// usually the file extracted from a .zip/.7z, whose name is whatever the
// archive happened to store — gating on ".nes" would couple the rule to
// archive hygiene. The costs are asymmetric anyway: a false positive is one
// extra digest set that matches nothing, a false negative is a platform that
// silently never matches.
func headerLen(head []byte, size int64) (int, string) {
	if len(head) >= inesHeaderLen && size > inesHeaderLen && bytes.HasPrefix(head, inesMagic) {
		// head[4] is the PRG-ROM bank count. Every real iNES image has at
		// least one 16 KB program bank; zero means this is something else
		// that happens to start with the magic.
		if head[4] != 0 {
			return inesHeaderLen, HeaderKindINES
		}
	}
	return 0, ""
}
