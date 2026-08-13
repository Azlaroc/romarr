package download

import (
	"crypto/sha1"
	"encoding/hex"
	"testing"
)

// testTorrentFixture returns a minimal valid single-file .torrent and the
// expected v1 infohash, computed directly over the known info-dict byte span
// so the test does not depend on the parser under test.
func testTorrentFixture() ([]byte, string) {
	data := []byte("d4:infod6:lengthi3e4:name3:foo12:piece lengthi16384e6:pieces20:aaaaaaaaaaaaaaaaaaaaee")
	info := data[len("d4:info") : len(data)-1]
	sum := sha1.Sum(info)
	return data, hex.EncodeToString(sum[:])
}

func TestInfohashFromTorrentData(t *testing.T) {
	data, want := testTorrentFixture()
	if got := infohashFromTorrentData(data); got != want {
		t.Errorf("infohash = %q, want %q", got, want)
	}

	// info preceded by another key: the parser must skip non-info values.
	withAnnounce := append([]byte("d8:announce3:abc"), data[1:]...)
	if got := infohashFromTorrentData(withAnnounce); got != want {
		t.Errorf("infohash with announce = %q, want %q", got, want)
	}

	bad := [][]byte{
		nil,
		[]byte(""),
		[]byte("not bencode"),
		[]byte("le"),                 // not a dict
		[]byte("d4:infoi3ee"),        // info is not a dict
		[]byte("d4:spami3ee"),        // no info key
		data[:len(data)-5],           // truncated
		[]byte("d4:info"),            // key without value
		[]byte("d99999999999:spame"), // absurd string length
	}
	for _, b := range bad {
		if got := infohashFromTorrentData(b); got != "" {
			t.Errorf("infohash(%q) = %q, want empty", b, got)
		}
	}
}
