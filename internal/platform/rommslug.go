package platform

// fsSlugAliases maps internal platform slugs to RomM filesystem slugs (the
// IGDB-derived directory names) where the two vocabularies diverge. The
// registry's romm_fs_slug column is the source of truth now; this map stays
// as the fallback for a slug the registry has no row for.
//
// It is deliberately not deleted. This lookup decides which directory a ROM
// is written to: a miss here does not misdisplay a name, it misfiles a file,
// so the answer degrades to the shipped alias rather than to the bare slug.
var fsSlugAliases = map[string]string{
	"genesis": "genesis-slash-megadrive",
}

// fsSlugReverse is the inverted alias map, built at init.
var fsSlugReverse = func() map[string]string {
	m := make(map[string]string, len(fsSlugAliases))
	for internal, fs := range fsSlugAliases {
		m[fs] = internal
	}
	return m
}()

// ToRommFSSlug translates an internal platform slug to the RomM filesystem
// slug (the directory name under the ROM library root). Identity for
// unaliased slugs.
func ToRommFSSlug(slug string) string {
	if fs := rommFSFor(slug); fs != "" {
		return fs
	}
	if fs, ok := fsSlugAliases[slug]; ok {
		return fs
	}
	return slug
}

// FromRommFSSlug translates a RomM filesystem slug back to the internal
// platform slug. Identity for unaliased slugs.
func FromRommFSSlug(fsSlug string) string {
	if slug := slugForRommFS(fsSlug); slug != "" {
		return slug
	}
	if internal, ok := fsSlugReverse[fsSlug]; ok {
		return internal
	}
	return fsSlug
}
