package romfile

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"hash/crc32"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func write(t *testing.T, name string, body []byte) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), name)
	if err := os.WriteFile(p, body, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
	return p
}

// want computes the expected digests from the fixture's own bytes rather
// than restating a literal: an assertion that hard-codes a hash of bytes the
// test also chose is only checking that nothing changed, not that the answer
// is right. TestHashKnownVector is where the literals live.
func want(b []byte) Hashes {
	c := crc32.NewIEEE()
	_, _ = c.Write(b)
	m := md5.Sum(b)
	s := sha1.Sum(b)
	return Hashes{
		CRC:  hex.EncodeToString(c.Sum(nil)),
		MD5:  hex.EncodeToString(m[:]),
		SHA1: hex.EncodeToString(s[:]),
	}
}

func ines(prgBanks byte, payload []byte) []byte {
	hdr := make([]byte, inesHeaderLen)
	copy(hdr, inesMagic)
	hdr[4] = prgBanks
	hdr[5] = 1
	return append(hdr, payload...)
}

func TestHashKnownVector(t *testing.T) {
	// The one place a literal belongs: "abc" against published values, so a
	// broken digest wiring cannot hide behind a self-consistent fixture.
	p := write(t, "abc.bin", []byte("abc"))
	got, err := Hash(p)
	if err != nil {
		t.Fatalf("Hash: %v", err)
	}
	wantH := Hashes{
		CRC:  "352441c2",
		MD5:  "900150983cd24fb0d6963f7d28e17f72",
		SHA1: "a9993e364706816aba3e25717850c26c9cd0d89d",
	}
	if got != wantH {
		t.Errorf("Hash(abc) = %+v, want %+v", got, wantH)
	}
}

func TestHashPayloadStripsINESHeader(t *testing.T) {
	payload := []byte(strings.Repeat("rom-bytes", 64))
	body := ines(2, payload)
	p := write(t, "Game.nes", body)

	got, err := HashPayload(p)
	if err != nil {
		t.Fatalf("HashPayload: %v", err)
	}
	if !got.Stripped || got.HeaderKind != HeaderKindINES || got.HeaderLen != 16 {
		t.Fatalf("got stripped=%v kind=%q len=%d, want an iNES strip",
			got.Stripped, got.HeaderKind, got.HeaderLen)
	}
	if got.Hashes != want(body) {
		t.Errorf("whole-file %+v, want %+v", got.Hashes, want(body))
	}
	if got.Payload != want(payload) {
		t.Errorf("payload %+v, want %+v", got.Payload, want(payload))
	}
	if got.Size != int64(len(body)) {
		t.Errorf("size %d, want %d", got.Size, len(body))
	}
	// The two sets must differ, or the strip did nothing.
	if got.Hashes.MD5 == got.Payload.MD5 {
		t.Error("whole-file and payload md5 agree; the header was not excluded")
	}
}

func TestHashPayloadLeavesUnheaderedFilesAlone(t *testing.T) {
	cases := []struct {
		name string
		body []byte
		why  string
	}{
		{"Game.nes", []byte(strings.Repeat("x", 4096)), "a .nes name without the magic is not a header"},
		{"Game.a78", append([]byte{0x01, 'A', 'T', 'A', 'R', 'I', '7', '8', '0', '0'}, make([]byte, 200)...), "No-Intro hashes a78 WITH its header"},
		{"Game.lnx", append([]byte("LYNX"), make([]byte, 200)...), "No-Intro hashes lnx WITH its header"},
		{"tiny.nes", append(append([]byte{}, inesMagic...), 0x01), "5 bytes is a magic, not an image"},
		{"zeroprg.nes", ines(0, []byte("payload")), "prg bank count 0 is not an iNES image"},
		{"header-only.nes", ines(1, nil), "nothing but a header has no payload"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, err := HashPayload(write(t, tc.name, tc.body))
			if err != nil {
				t.Fatalf("HashPayload: %v", err)
			}
			if got.Stripped {
				t.Errorf("stripped %q (%s)", tc.name, tc.why)
			}
			if !got.Payload.Zero() {
				t.Errorf("payload hashes set on an unstripped file: %+v", got.Payload)
			}
			if got.Hashes != want(tc.body) {
				t.Errorf("whole-file %+v, want %+v", got.Hashes, want(tc.body))
			}
		})
	}
}

func TestHashPayloadAgreesWithHash(t *testing.T) {
	// One pass with two digest sets must produce the same whole-file answer
	// as the simple one-pass path, headered or not.
	for _, body := range [][]byte{
		[]byte(strings.Repeat("plain", 500)),
		ines(4, []byte(strings.Repeat("headered", 500))),
		{},
	} {
		p := write(t, "rom.bin", body)
		plain, err := Hash(p)
		if err != nil {
			t.Fatalf("Hash: %v", err)
		}
		res, err := HashPayload(p)
		if err != nil {
			t.Fatalf("HashPayload: %v", err)
		}
		if plain != res.Hashes {
			t.Errorf("Hash %+v != HashPayload %+v", plain, res.Hashes)
		}
	}
}

func TestHashMissingFile(t *testing.T) {
	if _, err := Hash(filepath.Join(t.TempDir(), "nope.gb")); err == nil {
		t.Error("Hash on a missing file returned nil error")
	}
	if _, err := HashPayload(filepath.Join(t.TempDir(), "nope.gb")); err == nil {
		t.Error("HashPayload on a missing file returned nil error")
	}
}

func TestZero(t *testing.T) {
	if !(Hashes{}).Zero() {
		t.Error("empty Hashes is not Zero")
	}
	if (Hashes{MD5: "x"}).Zero() {
		t.Error("populated Hashes reports Zero")
	}
}
