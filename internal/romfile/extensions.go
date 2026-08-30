package romfile

import (
	"path/filepath"
	"strings"
)

// The game-file extension vocabulary. Two planes grew their own copies (the
// manual-import scanner and the legacy library scan) and they had drifted —
// one knew .chd and .cue, the other .rpx and .pkg. This is the union, in the
// package every plane may import, so "is this file a game?" has one answer.
//
// It is a recognition vocabulary, not a gate: the library scanner enumerates
// by sidecar-exclusion precisely because this list can never enumerate every
// cartridge extension a DAT-canonical name may carry (.a26, .lnx, .ws, ...).
var gameExtensions = map[string]bool{
	".nsp": true, ".xci": true, ".nsz": true, // Switch
	".nes": true, ".sfc": true, ".smc": true, // NES/SNES
	".gba": true, ".gb": true, ".gbc": true, // Game Boy
	".nds": true, ".3ds": true, ".cia": true, // DS/3DS
	".n64": true, ".z64": true, ".v64": true, // N64
	".iso": true, ".bin": true, ".cue": true, // Disc images
	".chd": true, ".gdi": true, ".cdi": true, // Compressed disc
	".gcz": true, ".gcm": true, ".rvz": true, // GameCube
	".wbfs": true, ".wad": true, ".rpx": true, // Wii/Wii U
	".pbp": true, ".cso": true, ".pkg": true, // PSP/PS3
	".zip": true, ".7z": true, ".rar": true, // Archives (common for ROMs)
	".exe": true, ".msi": true, // PC
}

// IsGameExtension reports whether name's extension is a recognized game or
// archive extension.
func IsGameExtension(name string) bool {
	return gameExtensions[strings.ToLower(filepath.Ext(name))]
}

// sidecarExtensions are the files that ride along with a ROM and are never
// themselves library entries: metadata, checksums, art, playlists. Shared
// with the import trust gate's candidate filter in spirit — a file is a
// library candidate unless something marks it as furniture.
var sidecarExtensions = map[string]bool{
	".json": true, ".txt": true, ".nfo": true, ".sfv": true, ".md5": true,
	".sha1": true, ".m3u": true, ".jpg": true, ".png": true, ".xml": true,
	".dat": true, ".log": true, ".torrent": true,
}

// IsSidecarExtension reports whether name's extension marks a non-ROM
// companion file.
func IsSidecarExtension(name string) bool {
	return sidecarExtensions[strings.ToLower(filepath.Ext(name))]
}
