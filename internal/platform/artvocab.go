package platform

// The art vocabulary: per-platform accent color, committed hardware-photo
// asset, and the libretro-thumbnails repository that carries the platform's
// per-title box art.
//
// This is curated DATA shipped in code, deliberately not a platforms-table
// column: a column backfilled at birth freezes the first curation into every
// existing install, and none of these values is an operator preference. It
// merges onto Row in ApplyArtVocab, which the registry's scan calls, so the
// API serves it like any other Row field.
//
// 🔴 LibretroThumbRepo values are the EXACT repository names under
// github.com/libretro-thumbnails (underscored form — they appear verbatim in
// raw.githubusercontent.com URLs). Two of them have historically been
// guessed wrong ("TurboGrafx_16" has no hyphen; "Neo_Geo_Pocket_Color" is
// three words): every value here is pinned against the committed org listing
// in testdata/libretro-thumb-repos.txt, refreshed by hand when curating a
// new platform (see artvocab_test.go).

// ArtVocab is one platform's curated art identity.
type ArtVocab struct {
	// AccentColor (#rrggbb) tints the platform's gradient, glyphs and chips.
	AccentColor string
	// ArtRef names a committed asset: "asset:<file>" resolves to
	// /assets/platforms/<file> in the frontend bundle. Filenames are
	// versioned (-v1) because that route is immutable-cached for a year —
	// re-curating a photo means a new filename, or it never reaches anyone
	// who has seen the old one. Empty = no committed photo; the render
	// ladder falls through (custom dir → glyph → cover collage → gradient).
	ArtRef string
	// LibretroThumbRepo is the thumbnails repository holding this
	// platform's Named_Boxarts art, keyed by canonical DAT name. Empty =
	// no repo exists (switch is shop-native, apple-iigs has none): title
	// art comes from the IGDB fallback instead.
	LibretroThumbRepo string
}

var artVocab = map[string]ArtVocab{
	"nes":                  {AccentColor: "#d0312d", ArtRef: "asset:nes-v1.png", LibretroThumbRepo: "Nintendo_-_Nintendo_Entertainment_System"},
	"snes":                 {AccentColor: "#7b6bb5", ArtRef: "asset:snes-v1.png", LibretroThumbRepo: "Nintendo_-_Super_Nintendo_Entertainment_System"},
	"n64":                  {AccentColor: "#1f9d44", ArtRef: "asset:n64-v1.png", LibretroThumbRepo: "Nintendo_-_Nintendo_64"},
	"ngc":                  {AccentColor: "#6a5acd", ArtRef: "asset:ngc-v1.png", LibretroThumbRepo: "Nintendo_-_GameCube"},
	"wii":                  {AccentColor: "#35a8e0", ArtRef: "asset:wii-v1.png", LibretroThumbRepo: "Nintendo_-_Wii"},
	"wiiu":                 {AccentColor: "#0f8ec7", ArtRef: "asset:wiiu-v1.png", LibretroThumbRepo: "Nintendo_-_Wii_U"},
	"switch":               {AccentColor: "#e60012", ArtRef: "asset:switch-v1.png"},
	"gb":                   {AccentColor: "#8b956d", ArtRef: "asset:gb-v1.png", LibretroThumbRepo: "Nintendo_-_Game_Boy"},
	"gbc":                  {AccentColor: "#7a3fbf", ArtRef: "asset:gbc-v1.png", LibretroThumbRepo: "Nintendo_-_Game_Boy_Color"},
	"gba":                  {AccentColor: "#4b3f9e", ArtRef: "asset:gba-v1.png", LibretroThumbRepo: "Nintendo_-_Game_Boy_Advance"},
	"nds":                  {AccentColor: "#8f9aa6", ArtRef: "asset:nds-v1.png", LibretroThumbRepo: "Nintendo_-_Nintendo_DS"},
	"3ds":                  {AccentColor: "#00a3a3", ArtRef: "asset:3ds-v1.png", LibretroThumbRepo: "Nintendo_-_Nintendo_3DS"},
	"virtualboy":           {AccentColor: "#c8102e", ArtRef: "asset:virtualboy-v1.png", LibretroThumbRepo: "Nintendo_-_Virtual_Boy"},
	"psx":                  {AccentColor: "#8f96a3", ArtRef: "asset:psx-v1.png", LibretroThumbRepo: "Sony_-_PlayStation"},
	"ps2":                  {AccentColor: "#2b3990", ArtRef: "asset:ps2-v1.png", LibretroThumbRepo: "Sony_-_PlayStation_2"},
	"ps3":                  {AccentColor: "#3d6eb4", ArtRef: "asset:ps3-v1.png", LibretroThumbRepo: "Sony_-_PlayStation_3"},
	"ps4":                  {AccentColor: "#003791", ArtRef: "asset:ps4-v1.png", LibretroThumbRepo: "Sony_-_PlayStation_4"},
	"psp":                  {AccentColor: "#4a4e69", ArtRef: "asset:psp-v1.png", LibretroThumbRepo: "Sony_-_PlayStation_Portable"},
	"psvita":               {AccentColor: "#0059b2", ArtRef: "asset:psvita-v1.png", LibretroThumbRepo: "Sony_-_PlayStation_Vita"},
	"xbox":                 {AccentColor: "#107c10", ArtRef: "asset:xbox-v1.png", LibretroThumbRepo: "Microsoft_-_Xbox"},
	"xbox360":              {AccentColor: "#9bc848", ArtRef: "asset:xbox360-v1.png", LibretroThumbRepo: "Microsoft_-_Xbox_360"},
	"genesis":              {AccentColor: "#d4453a", ArtRef: "asset:genesis-v1.png", LibretroThumbRepo: "Sega_-_Mega_Drive_-_Genesis"},
	"saturn":               {AccentColor: "#2456a8", ArtRef: "asset:saturn-v1.png", LibretroThumbRepo: "Sega_-_Saturn"},
	"dc":                   {AccentColor: "#f68b1f", ArtRef: "asset:dc-v1.png", LibretroThumbRepo: "Sega_-_Dreamcast"},
	"sms":                  {AccentColor: "#005baa", ArtRef: "asset:sms-v1.png", LibretroThumbRepo: "Sega_-_Master_System_-_Mark_III"},
	"gamegear":             {AccentColor: "#3066be", ArtRef: "asset:gamegear-v1.png", LibretroThumbRepo: "Sega_-_Game_Gear"},
	"sega32":               {AccentColor: "#f2b705", ArtRef: "asset:sega32-v1.png", LibretroThumbRepo: "Sega_-_32X"},
	"atari2600":            {AccentColor: "#a85b1e", ArtRef: "asset:atari2600-v1.png", LibretroThumbRepo: "Atari_-_2600"},
	"atari7800":            {AccentColor: "#8c5a2b", ArtRef: "asset:atari7800-v1.png", LibretroThumbRepo: "Atari_-_7800"},
	"lynx":                 {AccentColor: "#e0a526", ArtRef: "asset:lynx-v1.png", LibretroThumbRepo: "Atari_-_Lynx"},
	"tg16":                 {AccentColor: "#f26522", ArtRef: "asset:tg16-v1.png", LibretroThumbRepo: "NEC_-_PC_Engine_-_TurboGrafx_16"},
	"colecovision":         {AccentColor: "#5e35b1", ArtRef: "asset:colecovision-v1.png", LibretroThumbRepo: "Coleco_-_ColecoVision"},
	"wonderswan-color":     {AccentColor: "#00a0b0", ArtRef: "asset:wonderswan-color-v1.png", LibretroThumbRepo: "Bandai_-_WonderSwan_Color"},
	"neo-geo-pocket-color": {AccentColor: "#1d6fb8", ArtRef: "asset:neo-geo-pocket-color-v1.png", LibretroThumbRepo: "SNK_-_Neo_Geo_Pocket_Color"},
	"apple-iigs":           {AccentColor: "#9aa7b8", ArtRef: "asset:apple-iigs-v1.png"},
	"arcade":               {AccentColor: "#d4145a", LibretroThumbRepo: "MAME"},
	"pc":                   {AccentColor: "#4a5568"},
}

// ApplyArtVocab merges the curated art identity onto a registry row. Rows
// outside the curated set (library-only slugs, system rows) keep empty
// values — the render ladder treats absence honestly.
func ApplyArtVocab(p *Row) {
	if v, ok := artVocab[p.Slug]; ok {
		p.AccentColor = v.AccentColor
		p.ArtRef = v.ArtRef
		p.LibretroThumbRepo = v.LibretroThumbRepo
	}
}

// ThumbRepoFor answers the art fetcher without a full row load.
func ThumbRepoFor(slug string) string {
	return artVocab[slug].LibretroThumbRepo
}

// AccentFor answers render fallbacks without a full row load.
func AccentFor(slug string) string {
	return artVocab[slug].AccentColor
}
