package db

import "testing"

func TestParseGamarrHashesFull(t *testing.T) {
	meta := `{"romm":{"md5":"ROMM"},"gamarr":{"crc":"3D564757","md5":"AABB01","sha1":"CCDD02","hashed_at":"2026-08-20T00:00:00Z","unh":{"crc":"11223344","md5":"eeff03","sha1":"99aa04","header":"ines"},"catalog":"verified"}}`
	h, ok := ParseGamarrHashes(meta)
	if !ok {
		t.Fatal("ok = false for a fully hashed row")
	}
	if h.CRC != "3d564757" || h.MD5 != "aabb01" || h.SHA1 != "ccdd02" {
		t.Errorf("whole-file hashes not lowercased/read: %+v", h)
	}
	if h.HashedAt != "2026-08-20T00:00:00Z" {
		t.Errorf("HashedAt = %q", h.HashedAt)
	}
	if h.Unh == nil || h.Unh.CRC != "11223344" || h.Unh.Header != "ines" {
		t.Errorf("unh block not read: %+v", h.Unh)
	}
}

func TestParseGamarrHashesUnhOnly(t *testing.T) {
	h, ok := ParseGamarrHashes(`{"gamarr":{"unh":{"sha1":"AB"}}}`)
	if !ok || h.Unh == nil || h.Unh.SHA1 != "ab" {
		t.Fatalf("unh-only row = %+v ok=%v, want readable", h, ok)
	}
	if h.CRC != "" || h.MD5 != "" || h.SHA1 != "" {
		t.Errorf("whole-file hashes should be empty: %+v", h)
	}
}

func TestParseGamarrHashesAbsent(t *testing.T) {
	for name, meta := range map[string]string{
		"no gamarr":        `{"romm":{"md5":"x"}}`,
		"empty gamarr":     `{"gamarr":{}}`,
		"empty unh":        `{"gamarr":{"unh":{}}}`,
		"malformed":        `{not json`,
		"release only":     `{"gamarr":{"release":{"md5":"outer"}}}`,
		"hash_skipped row": `{"gamarr":{"hash_skipped":"directory"}}`,
	} {
		if _, ok := ParseGamarrHashes(meta); ok {
			t.Errorf("%s: ok = true, want false", name)
		}
	}
}
