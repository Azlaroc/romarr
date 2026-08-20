// Package romfile answers one question: what are the bytes of this library
// entry's ROM, and what are their hashes?
//
// It exists because three planes need that answer and each had grown its own
// half of it — the bulk renamer staged and md5'd inner ROMs, the import trust
// gate hashed extracted files three ways, and the hash backfill needs both.
// Keeping one implementation means "the hash of this ROM" cannot come out
// differently depending on which caller asked.
//
// Two facts about ROM bytes shape this package:
//
//   - Most of the library is .zip/.7z, so the bytes that matter are usually
//     INSIDE an archive. Staging + extraction lives here for that reason.
//   - Some systems wrap the dump in a container header that is not part of
//     the dump. No-Intro publishes those platforms twice — once headered
//     (.nes) and once not (.unh) — and a headered library matches only the
//     second. So hashing yields TWO digest sets when a header is present:
//     the file's own bytes (its identity) and the payload's (what the
//     catalog can be asked about).
//
// Stdlib only, deliberately: every other plane may import this one.
package romfile

import (
	"crypto/md5"
	"crypto/sha1"
	"encoding/hex"
	"hash"
	"hash/crc32"
	"io"
	"os"
)

// Hashes are the three families the DAT authorities publish between them
// (No-Intro leads with crc32, Redump carries all three). Lowercased hex,
// empty when not computed.
type Hashes struct {
	CRC  string `json:"crc,omitempty"`
	MD5  string `json:"md5,omitempty"`
	SHA1 string `json:"sha1,omitempty"`
}

// Zero reports whether nothing was computed.
func (h Hashes) Zero() bool { return h.CRC == "" && h.MD5 == "" && h.SHA1 == "" }

// Result is one file measured. Hashes always covers the whole file; Payload
// is populated only when a container header was recognised and stripped.
type Result struct {
	Hashes
	Payload    Hashes
	Stripped   bool
	HeaderKind string // HeaderKindINES when Stripped
	HeaderLen  int
	Size       int64
}

// Hash computes the three hashes of a file in one pass.
func Hash(path string) (Hashes, error) {
	f, err := os.Open(path)
	if err != nil {
		return Hashes{}, err
	}
	defer f.Close()
	d := newDigests()
	if _, err := io.Copy(d.writer(), f); err != nil {
		return Hashes{}, err
	}
	return d.sum(), nil
}

// HashPayload computes the whole-file hashes and, when the file opens with a
// recognised container header, the header-stripped payload's hashes — in a
// single pass over the bytes. The payload digests simply start one header
// later; nothing is read twice and nothing is buffered beyond the header.
func HashPayload(path string) (Result, error) {
	f, err := os.Open(path)
	if err != nil {
		return Result{}, err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return Result{}, err
	}

	res := Result{Size: fi.Size()}
	whole := newDigests()

	head := make([]byte, headerProbeLen)
	n, err := io.ReadFull(f, head)
	if err != nil && err != io.EOF && err != io.ErrUnexpectedEOF {
		return Result{}, err
	}
	head = head[:n]
	if _, err := whole.writer().Write(head); err != nil {
		return Result{}, err
	}

	hlen, kind := headerLen(head, fi.Size())
	if hlen == 0 {
		if _, err := io.Copy(whole.writer(), f); err != nil {
			return Result{}, err
		}
		res.Hashes = whole.sum()
		return res, nil
	}

	// Header recognised: the probe already covers it, so the payload digests
	// take the probe's tail and then ride along with the whole-file ones.
	payload := newDigests()
	if _, err := payload.writer().Write(head[hlen:]); err != nil {
		return Result{}, err
	}
	if _, err := io.Copy(io.MultiWriter(whole.writer(), payload.writer()), f); err != nil {
		return Result{}, err
	}
	res.Hashes = whole.sum()
	res.Payload = payload.sum()
	res.Stripped = true
	res.HeaderKind = kind
	res.HeaderLen = hlen
	return res, nil
}

type digests struct {
	c hash.Hash32
	m hash.Hash
	s hash.Hash
}

func newDigests() digests {
	return digests{c: crc32.NewIEEE(), m: md5.New(), s: sha1.New()}
}

func (d digests) writer() io.Writer { return io.MultiWriter(d.c, d.m, d.s) }

func (d digests) sum() Hashes {
	return Hashes{
		CRC:  hex.EncodeToString(d.c.Sum(nil)),
		MD5:  hex.EncodeToString(d.m.Sum(nil)),
		SHA1: hex.EncodeToString(d.s.Sum(nil)),
	}
}
