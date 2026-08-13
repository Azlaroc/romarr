package platform

// fsSlugAliases maps Gamarr's internal platform slugs to RomM filesystem
// slugs (IGDB-derived directory names) where the two vocabularies diverge.
// Slugs not listed here are identical on both sides. The DB and search layer
// always speak the internal slug; only the on-disk directory layout and the
// RomM API speak fs_slug.
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
	if fs, ok := fsSlugAliases[slug]; ok {
		return fs
	}
	return slug
}

// FromRommFSSlug translates a RomM filesystem slug back to the internal
// platform slug. Identity for unaliased slugs.
func FromRommFSSlug(fsSlug string) string {
	if internal, ok := fsSlugReverse[fsSlug]; ok {
		return internal
	}
	return fsSlug
}
