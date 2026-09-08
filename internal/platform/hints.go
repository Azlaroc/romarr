package platform

import "regexp"

// titleHints maps release-name vocabulary to platforms — the scene tags and
// platform names that appear in torrent/nzb titles. First match wins, so
// multi-word identities (wii u, xbox 360) sit above their prefixes.
var titleHints = []struct {
	Pattern *regexp.Regexp
	Info    PlatformInfo
}{
	{regexp.MustCompile(`(?i)\[nsp\]|\bnsp\b|\bnsw\b|switch`), PlatformInfo{Name: "Switch", Slug: "switch"}},
	{regexp.MustCompile(`(?i)\[xci\]|\bxci\b`), PlatformInfo{Name: "Switch", Slug: "switch"}},
	{regexp.MustCompile(`(?i)\bwiiu\b|wii\s*u`), PlatformInfo{Name: "Wii U", Slug: "wiiu"}},
	{regexp.MustCompile(`(?i)\bwii\b`), PlatformInfo{Name: "Wii", Slug: "wii"}},
	{regexp.MustCompile(`(?i)\bgamecube\b|\bngc\b|\bgcn\b`), PlatformInfo{Name: "GameCube", Slug: "ngc"}},
	{regexp.MustCompile(`(?i)\b3ds\b`), PlatformInfo{Name: "3DS", Slug: "3ds"}},
	{regexp.MustCompile(`(?i)\bnds\b|\bnintendo\s*ds\b`), PlatformInfo{Name: "DS", Slug: "nds"}},
	{regexp.MustCompile(`(?i)\bgba\b`), PlatformInfo{Name: "Game Boy Advance", Slug: "gba"}},
	{regexp.MustCompile(`(?i)\bps3\b|playstation\s*3`), PlatformInfo{Name: "PS3", Slug: "ps3"}},
	{regexp.MustCompile(`(?i)\bps2\b|playstation\s*2`), PlatformInfo{Name: "PS2", Slug: "ps2"}},
	{regexp.MustCompile(`(?i)\bps1\b|\bpsx\b`), PlatformInfo{Name: "PS1", Slug: "psx"}},
	{regexp.MustCompile(`(?i)\bpsp\b`), PlatformInfo{Name: "PSP", Slug: "psp"}},
	{regexp.MustCompile(`(?i)\bps4\b|playstation\s*4`), PlatformInfo{Name: "PS4", Slug: "ps4"}},
	{regexp.MustCompile(`(?i)\bps\s?vita\b`), PlatformInfo{Name: "PS Vita", Slug: "psvita"}},
	{regexp.MustCompile(`(?i)\bxbox\s*360`), PlatformInfo{Name: "Xbox 360", Slug: "xbox360"}},
	{regexp.MustCompile(`(?i)\bxbox\b`), PlatformInfo{Name: "Xbox", Slug: "xbox"}},
	{regexp.MustCompile(`(?i)\bdreamcast\b`), PlatformInfo{Name: "Dreamcast", Slug: "dc"}},
	{regexp.MustCompile(`(?i)\bn64\b|nintendo\s*64`), PlatformInfo{Name: "Nintendo 64", Slug: "n64"}},
	{regexp.MustCompile(`(?i)\bsnes\b|super\s*nintendo`), PlatformInfo{Name: "SNES", Slug: "snes"}},
	{regexp.MustCompile(`(?i)\bnes\b`), PlatformInfo{Name: "NES", Slug: "nes"}},
	{regexp.MustCompile(`(?i)\bgenesis\b|mega\s*drive`), PlatformInfo{Name: "Sega Genesis", Slug: "genesis"}},
}
