package artsvc

import (
	"net/url"
	"strings"
)

// thumbBad are the characters libretro-thumbnails replaces with '_' in file
// names (their playlist-name sanitization). A DAT name goes through this
// before it can key a Named_Boxarts file.
var thumbBad = strings.NewReplacer(
	"&", "_", "*", "_", "/", "_", ":", "_", "`", "_",
	"<", "_", ">", "_", "?", "_", "\\", "_", "|", "_", "\"", "_",
)

// SanitizeThumbName maps a canonical name stem onto libretro-thumbnails'
// file-name alphabet. Exported for the tests that pin it against gnarly
// real DAT names.
func SanitizeThumbName(stem string) string {
	return strings.TrimSpace(thumbBad.Replace(stem))
}

// pathEscapeName renders a sanitized stem as a URL path segment.
func pathEscapeName(stem string) string {
	return url.PathEscape(stem)
}
