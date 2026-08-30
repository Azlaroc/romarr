package dat

import (
	"encoding/xml"
	"fmt"
	"io"
	"strconv"
	"strings"
	"unicode"

	"gamarr/internal/selection"
)

// Parse reads a DAT in either supported syntax, sniffing the format from the
// first non-space bytes. Callers hand it decompressed bytes; unwrapping any
// transport container is the fetch layer's job.
//
// A leading UTF-8 byte-order mark is stripped before sniffing: hand-uploaded
// DATs are routinely saved by Windows tooling, and a BOM would otherwise send
// an XML file to the clrmamepro parser and fail with a misleading error.
func Parse(raw []byte) (*File, error) {
	head := strings.TrimLeftFunc(strings.TrimPrefix(string(raw), "\ufeff"), unicode.IsSpace)
	switch {
	case strings.HasPrefix(head, "<"):
		return parseLogiqx(raw)
	case head == "":
		return nil, fmt.Errorf("dat: empty file")
	default:
		return parseClrMamePro(head)
	}
}

// ---------- logiqx XML (Redump, MAME, most modern publishers) ----------

type xmlRom struct {
	Name   string `xml:"name,attr"`
	Size   string `xml:"size,attr"`
	CRC    string `xml:"crc,attr"`
	MD5    string `xml:"md5,attr"`
	SHA1   string `xml:"sha1,attr"`
	Serial string `xml:"serial,attr"`
}

type xmlGame struct {
	Name        string   `xml:"name,attr"`
	CloneOf     string   `xml:"cloneof,attr"`
	Description string   `xml:"description"`
	Roms        []xmlRom `xml:"rom"`
}

type xmlDatafile struct {
	Header struct {
		Name        string `xml:"name"`
		Description string `xml:"description"`
		Version     string `xml:"version"`
		Date        string `xml:"date"`
	} `xml:"header"`
	// Publishers disagree on the element name for an entry: logiqx says
	// <game>, MAME-derived DATs say <machine>. Accept both.
	Games    []xmlGame `xml:"game"`
	Machines []xmlGame `xml:"machine"`
}

func parseLogiqx(raw []byte) (*File, error) {
	var doc xmlDatafile
	dec := xml.NewDecoder(strings.NewReader(string(raw)))
	// Real-world DATs carry a DTD reference and occasional non-UTF8 bytes in
	// title text; tolerate both rather than rejecting the whole catalog.
	dec.Strict = false
	dec.CharsetReader = func(_ string, input io.Reader) (io.Reader, error) { return input, nil }
	if err := dec.Decode(&doc); err != nil {
		return nil, fmt.Errorf("dat: parse logiqx: %w", err)
	}
	out := &File{Header: Header{
		Name:        strings.TrimSpace(doc.Header.Name),
		Description: strings.TrimSpace(doc.Header.Description),
		Version:     strings.TrimSpace(doc.Header.Version),
		Date:        strings.TrimSpace(doc.Header.Date),
	}}
	entries := append(doc.Games, doc.Machines...)
	out.Games = make([]Game, 0, len(entries))
	for _, e := range entries {
		if strings.TrimSpace(e.Name) == "" {
			continue
		}
		g := Game{
			Name:        strings.TrimSpace(e.Name),
			Description: strings.TrimSpace(e.Description),
			CloneOf:     strings.TrimSpace(e.CloneOf),
		}
		for _, r := range e.Roms {
			if strings.TrimSpace(r.Name) == "" {
				continue
			}
			size, _ := strconv.ParseInt(strings.TrimSpace(r.Size), 10, 64)
			g.Roms = append(g.Roms, Rom{
				Name:   strings.TrimSpace(r.Name),
				Size:   size,
				CRC:    normHash(r.CRC),
				MD5:    normHash(r.MD5),
				SHA1:   normHash(r.SHA1),
				Serial: strings.TrimSpace(r.Serial),
			})
		}
		annotate(&g, "")
		out.Games = append(out.Games, g)
	}
	return out, nil
}

// ---------- clrmamepro (the libretro No-Intro mirror) ----------

// parseClrMamePro reads the parenthesised block syntax:
//
//	game (
//	        name "Adventure (USA)"
//	        region "USA"
//	        rom ( name "Adventure (USA).a26" size 8192 crc F39E7F9E md5 ... )
//	)
//
// Quoted values are lexed before parentheses, so the parens inside a game
// name never confuse block nesting.
func parseClrMamePro(src string) (*File, error) {
	toks := lexCMP(src)
	out := &File{}
	i := 0
	for i < len(toks) {
		if toks[i].kind != tokWord {
			i++
			continue
		}
		name := strings.ToLower(toks[i].val)
		if i+1 >= len(toks) || toks[i+1].kind != tokOpen {
			i++
			continue
		}
		body, next := readBlock(toks, i+1)
		i = next
		switch name {
		case "clrmamepro", "datafile", "header":
			out.Header = Header{
				Name:        body.str("name"),
				Description: body.str("description"),
				Version:     body.str("version"),
				Date:        body.str("date"),
			}
		case "game", "machine", "set":
			g := Game{
				Name:        body.str("name"),
				Description: body.str("description"),
				CloneOf:     body.str("cloneof"),
			}
			if g.Name == "" {
				continue
			}
			for _, sub := range body.blocks("rom") {
				rn := sub.str("name")
				if rn == "" {
					continue
				}
				size, _ := strconv.ParseInt(sub.str("size"), 10, 64)
				g.Roms = append(g.Roms, Rom{
					Name:   rn,
					Size:   size,
					CRC:    normHash(sub.str("crc")),
					MD5:    normHash(sub.str("md5")),
					SHA1:   normHash(sub.str("sha1")),
					Serial: sub.str("serial"),
				})
			}
			annotate(&g, body.str("region"))
			out.Games = append(out.Games, g)
		}
	}
	if out.Header.Name == "" && len(out.Games) == 0 {
		return nil, fmt.Errorf("dat: no clrmamepro blocks found")
	}
	return out, nil
}

const (
	tokWord = iota
	tokString
	tokOpen
	tokClose
)

type cmpTok struct {
	kind int
	val  string
}

func lexCMP(s string) []cmpTok {
	var out []cmpTok
	r := []rune(s)
	for i := 0; i < len(r); {
		c := r[i]
		switch {
		case unicode.IsSpace(c):
			i++
		case c == '(':
			out = append(out, cmpTok{kind: tokOpen})
			i++
		case c == ')':
			out = append(out, cmpTok{kind: tokClose})
			i++
		case c == '"':
			i++
			start := i
			for i < len(r) && r[i] != '"' {
				i++
			}
			out = append(out, cmpTok{kind: tokString, val: string(r[start:i])})
			if i < len(r) {
				i++
			}
		default:
			start := i
			for i < len(r) && !unicode.IsSpace(r[i]) && r[i] != '(' && r[i] != ')' && r[i] != '"' {
				i++
			}
			out = append(out, cmpTok{kind: tokWord, val: string(r[start:i])})
		}
	}
	return out
}

// cmpBlock is one parsed block's contents: scalar key/value pairs plus any
// nested blocks keyed by their leading word.
type cmpBlock struct {
	scalars map[string]string
	subs    map[string][]cmpBlock
}

func (b cmpBlock) str(key string) string { return b.scalars[key] }

func (b cmpBlock) blocks(key string) []cmpBlock { return b.subs[key] }

// readBlock consumes tokens starting at an open paren and returns the block
// plus the index just past its close.
func readBlock(toks []cmpTok, open int) (cmpBlock, int) {
	blk := cmpBlock{scalars: map[string]string{}, subs: map[string][]cmpBlock{}}
	i := open + 1
	for i < len(toks) {
		t := toks[i]
		if t.kind == tokClose {
			return blk, i + 1
		}
		if t.kind != tokWord {
			i++
			continue
		}
		key := strings.ToLower(t.val)
		if i+1 < len(toks) && toks[i+1].kind == tokOpen {
			sub, next := readBlock(toks, i+1)
			blk.subs[key] = append(blk.subs[key], sub)
			i = next
			continue
		}
		if i+1 < len(toks) && (toks[i+1].kind == tokWord || toks[i+1].kind == tokString) {
			if _, seen := blk.scalars[key]; !seen {
				blk.scalars[key] = toks[i+1].val
			}
			i += 2
			continue
		}
		i++
	}
	return blk, i
}

// ---------- shared derivation ----------

// annotate fills the attributes the canonical DAT name encodes, reusing the
// selection engine's No-Intro/Redump name parser so both layers speak one
// vocabulary instead of two drifting implementations.
//
// 🔴 The explicit region attribute is UNIONED with the name's, not preferred
// over it. The mirror's DATs declare one primary region while the name lists
// every region the dump covers — `region "USA"` on a game named
// "Air-Sea Battle ~ Target Fun (Japan, USA) (En)" — and taking the attribute
// alone throws away the fact that it is a USA release. That cost a real 1G1R
// keeper: the dump filed as Japan-only lost to a European one under a
// USA-first region order, on a platform where the user wanted the USA dump.
// The attribute leads because it is the publisher's own primary; the name's
// regions follow because they are true too.
func annotate(g *Game, explicitRegion string) {
	attrs := selection.Parse(g.Name)
	g.BareTitle = selection.BareTitle(g.Name)
	g.Revision = attrs.Revision
	g.Languages = strings.Join(attrs.Languages, ",")
	g.Region = unionRegions(explicitRegion, attrs.Regions)
	g.Flags = flagList(
		boolFlag(attrs.IsBIOS, "bios"),
		boolFlag(attrs.IsProto, "proto"),
		boolFlag(attrs.IsDemo, "demo"),
		boolFlag(attrs.IsUnlicensed, "unl"),
		boolFlag(attrs.IsAftermarket, "aftermarket"),
		boolFlag(attrs.IsPirate, "pirate"),
		boolFlag(attrs.BadDump, "bad"),
		boolFlag(attrs.VerifiedDump, "verified"),
	)
}

// unionRegions merges the DAT's declared region with the name's, primary
// first, without duplicates. Either side may be empty.
func unionRegions(explicit string, fromName []string) string {
	var out []string
	seen := map[string]bool{}
	add := func(r string) {
		r = strings.ToLower(strings.TrimSpace(r))
		if r == "" || seen[r] {
			return
		}
		seen[r] = true
		out = append(out, r)
	}
	for _, r := range strings.Split(explicit, ",") {
		add(r)
	}
	for _, r := range fromName {
		add(r)
	}
	return strings.Join(out, ",")
}

func boolFlag(on bool, name string) string {
	if on {
		return name
	}
	return ""
}

func normHash(s string) string { return strings.ToLower(strings.TrimSpace(s)) }
