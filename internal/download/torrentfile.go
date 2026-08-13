package download

import (
	"crypto/sha1"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"time"
)

// maxTorrentFileSize caps a fetched .torrent metadata file. Real-world
// .torrent files are KBs; even huge packs stay well under this.
const maxTorrentFileSize = 10 << 20 // 10 MiB

// fetchTorrentFile downloads a .torrent file server-side so qBittorrent can be
// handed the raw bytes instead of a URL (see qbit.AddTorrentFile for why).
func fetchTorrentFile(torrentURL string) ([]byte, error) {
	client := &http.Client{Timeout: 60 * time.Second}
	req, err := http.NewRequest("GET", torrentURL, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "Gamarr/1.0")
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("HTTP %d fetching torrent file", resp.StatusCode)
	}
	blob, err := io.ReadAll(io.LimitReader(resp.Body, maxTorrentFileSize+1))
	if err != nil {
		return nil, err
	}
	if len(blob) > maxTorrentFileSize {
		return nil, fmt.Errorf("torrent file exceeds %d bytes", maxTorrentFileSize)
	}
	return blob, nil
}

// infohashFromTorrentData returns the lowercase hex v1 infohash of a bencoded
// .torrent file: the SHA-1 of the raw byte span of the top-level "info" dict.
// Hashing the original bytes (rather than re-encoding a parsed structure)
// cannot introduce canonicalization bugs. Returns "" if the data is not a
// bencoded dict with an info dict inside.
func infohashFromTorrentData(data []byte) string {
	if len(data) == 0 || data[0] != 'd' {
		return ""
	}
	i := 1
	for i < len(data) && data[i] != 'e' {
		key, next, ok := bdecodeString(data, i)
		if !ok {
			return ""
		}
		end, ok := bdecodeSkipValue(data, next)
		if !ok {
			return ""
		}
		if string(key) == "info" {
			if data[next] != 'd' {
				return ""
			}
			sum := sha1.Sum(data[next:end])
			return hex.EncodeToString(sum[:])
		}
		i = end
	}
	return ""
}

// bdecodeString reads a bencoded string ("<len>:<bytes>") at offset i,
// returning the bytes and the offset just past it.
func bdecodeString(data []byte, i int) ([]byte, int, bool) {
	n := 0
	start := i
	for i < len(data) && data[i] >= '0' && data[i] <= '9' {
		n = n*10 + int(data[i]-'0')
		i++
		if n > len(data) { // overflow guard: length can't exceed input
			return nil, 0, false
		}
	}
	if i == start || i >= len(data) || data[i] != ':' {
		return nil, 0, false
	}
	i++
	if i+n > len(data) {
		return nil, 0, false
	}
	return data[i : i+n], i + n, true
}

// bdecodeSkipValue returns the offset just past the bencoded value at offset
// i, without materializing it.
func bdecodeSkipValue(data []byte, i int) (int, bool) {
	if i >= len(data) {
		return 0, false
	}
	switch {
	case data[i] == 'i':
		i++
		for i < len(data) && data[i] != 'e' {
			i++
		}
		if i >= len(data) {
			return 0, false
		}
		return i + 1, true
	case data[i] == 'l' || data[i] == 'd':
		isDict := data[i] == 'd'
		i++
		for i < len(data) && data[i] != 'e' {
			var ok bool
			if isDict {
				if _, i, ok = bdecodeString(data, i); !ok {
					return 0, false
				}
			}
			if i, ok = bdecodeSkipValue(data, i); !ok {
				return 0, false
			}
		}
		if i >= len(data) {
			return 0, false
		}
		return i + 1, true
	default:
		_, end, ok := bdecodeString(data, i)
		return end, ok
	}
}
