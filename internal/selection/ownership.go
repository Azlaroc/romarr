package selection

import "strings"

// OwnershipKeys returns the lowered comparison keys a title matches under:
// the raw title, the parsed CleanTitle (classified tags stripped), and the
// BareTitle (ALL tags stripped). Raw and clean keep exact titles exact; bare
// is what lets a wishlist "Kirby's Dream Land 2" meet a library row
// "Kirby's Dream Land 2 (USA, Europe) (SGB Enhanced)" or a Vimm release
// carrying a trailing "(GB)" system tag.
//
// It lives here rather than in the scheduler because ownership is now asked by
// three planes — the wishlist loop, the selector's owned check, and the
// collection set's weakest matching tier — and three copies of "what counts as
// the same title" is exactly the drift this package exists to prevent.
func OwnershipKeys(title string) []string {
	keys := make([]string, 0, 3)
	add := func(k string) {
		k = strings.ToLower(strings.TrimSpace(k))
		if k == "" {
			return
		}
		for _, have := range keys {
			if have == k {
				return
			}
		}
		keys = append(keys, k)
	}
	add(title)
	add(Parse(title).CleanTitle)
	add(BareTitle(title))
	return keys
}
