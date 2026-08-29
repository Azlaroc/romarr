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
