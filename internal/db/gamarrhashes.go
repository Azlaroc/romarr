package db

import (
	"encoding/json"
	"strings"
)

// GamarrHashes is a row's stored identity read back off its metadata blob —
// the $.gamarr hashes SaveLibraryHashes wrote (contract at the top of
// libraryhash.go and in docs/library-identity.md).
type GamarrHashes struct {
	CRC      string
	MD5      string
	SHA1     string
	HashedAt string            // RFC3339, as written by hashSetClause
	Unh      *UnheaderedHashes // payload-only hashes; nil when no header was recognised
}

// ParseGamarrHashes reads $.gamarr.{crc,md5,sha1,hashed_at,unh} out of a
// metadata blob. ok=false when the blob is malformed or carries neither a
// whole-file hash nor an unh block. Release hashes ($.gamarr.release) are a
// different object — the source's published hash of the file it served, an
// archive's OUTER bytes — and are deliberately not read here.
func ParseGamarrHashes(metadata string) (GamarrHashes, bool) {
	var envelope struct {
		Gamarr struct {
			CRC      string            `json:"crc"`
			MD5      string            `json:"md5"`
			SHA1     string            `json:"sha1"`
			HashedAt string            `json:"hashed_at"`
			Unh      *UnheaderedHashes `json:"unh"`
		} `json:"gamarr"`
	}
	if err := json.Unmarshal([]byte(metadata), &envelope); err != nil {
		return GamarrHashes{}, false
	}
	lower := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	g := envelope.Gamarr
	h := GamarrHashes{
		CRC:      lower(g.CRC),
		MD5:      lower(g.MD5),
		SHA1:     lower(g.SHA1),
		HashedAt: strings.TrimSpace(g.HashedAt),
	}
	if g.Unh != nil {
		unh := UnheaderedHashes{
			CRC:    lower(g.Unh.CRC),
			MD5:    lower(g.Unh.MD5),
			SHA1:   lower(g.Unh.SHA1),
			Header: g.Unh.Header,
		}
		if unh.CRC != "" || unh.MD5 != "" || unh.SHA1 != "" {
			h.Unh = &unh
		}
	}
	if h.CRC == "" && h.MD5 == "" && h.SHA1 == "" && h.Unh == nil {
		return GamarrHashes{}, false
	}
	return h, true
}

// ParseRommContentHashes reads $.romm.{crc,md5,sha1} — RomM's inner-content
// hashes, the same domain a DAT lookup wants (proven on the Hagane pair; see
// docs/library-identity.md). RomM rewrites them wholesale on every sync, so
// they are current without a timestamp. They belong to the ROM's bytes only
// for single-rom entries — a directory import's multi-file hash matches
// nothing, which is harmless in a lookup.
func ParseRommContentHashes(metadata string) (crc, md5, sha1 string, ok bool) {
	var envelope struct {
		Romm struct {
			CRC  string `json:"crc"`
			MD5  string `json:"md5"`
			SHA1 string `json:"sha1"`
		} `json:"romm"`
	}
	if err := json.Unmarshal([]byte(metadata), &envelope); err != nil {
		return "", "", "", false
	}
	lower := func(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
	crc, md5, sha1 = lower(envelope.Romm.CRC), lower(envelope.Romm.MD5), lower(envelope.Romm.SHA1)
	if crc == "" && md5 == "" && sha1 == "" {
		return "", "", "", false
	}
	return crc, md5, sha1, true
}
