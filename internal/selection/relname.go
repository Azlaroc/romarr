// Package selection implements the release-selection engine (F4): parsing
// No-Intro/Redump release names into structured attributes, and (in later
// stages) filtering, ranking and picking releases against quality profiles.
package selection

import (
	"regexp"
	"strconv"
	"strings"

	"gamarr/internal/models"
)

// regionTokens is the No-Intro/Redump region vocabulary, lowered. A
// parenthetical token whose comma-separated parts all appear here is a region
// tag ("(USA)", "(USA, Europe)").
var regionTokens = map[string]bool{
	"usa": true, "world": true, "europe": true, "japan": true,
	"uk": true, "australia": true, "canada": true, "korea": true,
	"china": true, "taiwan": true, "france": true, "germany": true,
	"spain": true, "italy": true, "netherlands": true, "sweden": true,
	"brazil": true, "asia": true,
}

// formatHints maps a stripped file extension to the FormatHint vocabulary the
// selector ranks on. .bin maps to cue because a bare bin is one half of a
// bin/cue disc dump.
var formatHints = map[string]string{
	".chd": "chd",
	".cue": "cue",
	".bin": "cue",
	".iso": "iso",
	".gdi": "gdi",
	".zip": "zip",
	".7z":  "7z",
}

// knownExtensions are the trailing extensions stripped from a release name
// before tag parsing. Broader than formatHints: extensions with no format
// preference of their own still must not pollute CleanTitle/SetKey.
var knownExtensions = []string{
	".chd", ".cue", ".bin", ".iso", ".gdi", ".zip", ".7z", ".rar",
	".nes", ".sfc", ".smc", ".gb", ".gbc", ".gba", ".nds", ".3ds", ".cia",
	".z64", ".n64", ".v64", ".md", ".gen", ".sms", ".gg", ".pce", ".a26",
	".col", ".rom", ".wad", ".wbfs", ".rvz", ".nsp", ".nsz", ".xci",
}

var (
	langRe  = regexp.MustCompile(`^(En|Fr|De|Es|It|Nl|Pt|Sv|No|Da|Fi|Ja|Ko|Zh)([,+].*)?$`)
	revRe   = regexp.MustCompile(`^Rev ([0-9A-Z]+)$`)
	discRe  = regexp.MustCompile(`^Disc (\d+)(?: of (\d+))?$`)
	protoRe = regexp.MustCompile(`^(Proto|Beta|Sample)( \d+)?$`)
	demoRe  = regexp.MustCompile(`^Demo`)
	badRe   = regexp.MustCompile(`^b\d*$`)
	tokenRe = regexp.MustCompile(`\(([^)]*)\)|\[([^\]]*)\]`)
)

// Parse extracts the No-Intro/Redump attributes a release title encodes.
// Unrecognized tags are left in place (they stay part of CleanTitle), so a
// title that uses no convention parses to itself with zero attributes set.
func Parse(title string) models.ReleaseAttrs {
	var a models.ReleaseAttrs

	name := strings.TrimSpace(title)
	ext := strings.ToLower(trailingExtension(name))
	if ext != "" {
		name = name[:len(name)-len(ext)]
		a.FormatHint = formatHints[ext]
		if a.FormatHint == "" {
			a.FormatHint = "raw"
		}
	}

	// Classify each (…)/[…] token; record the spans of classified ones so
	// CleanTitle/SetKey can drop exactly those.
	var classified []span
	var discSpans []span

	for _, m := range tokenRe.FindAllStringSubmatchIndex(name, -1) {
		// m: full match span, then group 1 (parens) span, group 2 (brackets) span.
		var tok string
		paren := m[2] >= 0
		if paren {
			tok = name[m[2]:m[3]]
		} else {
			tok = name[m[4]:m[5]]
		}
		tok = strings.TrimSpace(tok)
		sp := span{m[0], m[1]}

		if paren {
			switch {
			case isRegionToken(tok):
				for _, r := range strings.Split(tok, ",") {
					a.Regions = append(a.Regions, strings.ToLower(strings.TrimSpace(r)))
				}
			case langRe.MatchString(tok):
				for _, l := range strings.FieldsFunc(tok, func(r rune) bool { return r == ',' || r == '+' }) {
					a.Languages = append(a.Languages, strings.TrimSpace(l))
				}
			case revRe.MatchString(tok):
				a.Revision = parseRevision(revRe.FindStringSubmatch(tok)[1])
			case discRe.MatchString(tok):
				g := discRe.FindStringSubmatch(tok)
				a.DiscIndex, _ = strconv.Atoi(g[1])
				if g[2] != "" {
					a.DiscTotal, _ = strconv.Atoi(g[2])
				}
				discSpans = append(discSpans, sp)
			case protoRe.MatchString(tok):
				a.IsProto = true
			case demoRe.MatchString(tok):
				a.IsDemo = true
			case tok == "Unl":
				a.IsUnlicensed = true
			default:
				continue // unclassified: keep in CleanTitle
			}
		} else {
			switch {
			case strings.EqualFold(tok, "BIOS"):
				a.IsBIOS = true
			case badRe.MatchString(tok):
				a.BadDump = true
			case tok == "!":
				a.VerifiedDump = true
			default:
				continue
			}
		}
		classified = append(classified, sp)
	}

	a.CleanTitle = collapseSpaces(removeSpans(name, classified))
	a.SetKey = strings.ToLower(collapseSpaces(removeSpans(name, discSpans)))
	return a
}

// trailingExtension returns the known rom/archive extension ending name, or "".
func trailingExtension(name string) string {
	lower := strings.ToLower(name)
	for _, ext := range knownExtensions {
		if strings.HasSuffix(lower, ext) {
			return name[len(name)-len(ext):]
		}
	}
	return ""
}

func isRegionToken(tok string) bool {
	parts := strings.Split(tok, ",")
	for _, p := range parts {
		if !regionTokens[strings.ToLower(strings.TrimSpace(p))] {
			return false
		}
	}
	return len(parts) > 0
}

// parseRevision maps "1"/"2"… to themselves and "A"/"B"… to 1/2…; No-Intro
// uses both forms. Unparseable revisions rank as 1 (present but unordered).
func parseRevision(s string) int {
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if len(s) == 1 && s[0] >= 'A' && s[0] <= 'Z' {
		return int(s[0]-'A') + 1
	}
	return 1
}

// span is a half-open [start, end) byte range in a release name.
type span struct{ start, end int }

func removeSpans(s string, spans []span) string {
	if len(spans) == 0 {
		return s
	}
	var b strings.Builder
	prev := 0
	for _, sp := range spans {
		b.WriteString(s[prev:sp.start])
		prev = sp.end
	}
	b.WriteString(s[prev:])
	return b.String()
}

func collapseSpaces(s string) string {
	return strings.Join(strings.Fields(s), " ")
}
