package collection

import (
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"gamarr/internal/selection"
)

// Clone lists are the preservation community's answer to the question parsed
// names cannot answer: which differently-NAMED dumps are the same game.
// Probotector, Operation C and Contra are one game with three regional titles;
// "Centipede (Accolade)" and "Centipede (Majesco)" are two different games
// that share a title. No amount of string parsing gets either right.
//
// RomArr adopts Retool's lists rather than minting its own opinion (BSD-3,
// keyed on the same DAT-name vocabulary the catalog already stores). They are
// an OVERLAY: title agreement does most of the work — Atari 7800's list holds
// six groups against 141 titles — and the list corrects it where upstream
// knows better.

// CloneList is one platform's parsed list.
type CloneList struct {
	Name        string
	LastUpdated string
	Groups      []CloneGroup
}

// CloneGroup is one game and the titles that are it.
type CloneGroup struct {
	Name       string
	Categories []string
	Titles     []CloneTitle
}

// CloneTitle is one name belonging to a group. Priority is Retool's preference
// between the group's titles (1 = preferred); absent means 1.
type CloneTitle struct {
	SearchTerm string
	Priority   int
}

// ParseCloneList reads Retool's clone-list JSON. Unknown fields are ignored on
// purpose: the format carries features we do not consume yet (compilations,
// filters, local names), and a new one must not break a refresh.
func ParseCloneList(data []byte) (CloneList, error) {
	var raw struct {
		Description struct {
			Name        string `json:"name"`
			LastUpdated string `json:"lastUpdated"`
		} `json:"description"`
		Variants []struct {
			Group      string   `json:"group"`
			Categories []string `json:"categories"`
			Titles     []struct {
				SearchTerm string `json:"searchTerm"`
				Priority   int    `json:"priority"`
			} `json:"titles"`
		} `json:"variants"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return CloneList{}, fmt.Errorf("parse clone list: %w", err)
	}
	if len(raw.Variants) == 0 {
		return CloneList{}, fmt.Errorf("clone list has no variant groups")
	}
	out := CloneList{Name: raw.Description.Name, LastUpdated: raw.Description.LastUpdated}
	for _, v := range raw.Variants {
		g := CloneGroup{Name: strings.TrimSpace(v.Group), Categories: v.Categories}
		if g.Name == "" {
			continue
		}
		for _, t := range v.Titles {
			term := strings.TrimSpace(t.SearchTerm)
			if term == "" {
				continue
			}
			prio := t.Priority
			if prio <= 0 {
				prio = 1
			}
			g.Titles = append(g.Titles, CloneTitle{SearchTerm: term, Priority: prio})
		}
		if len(g.Titles) == 0 {
			continue
		}
		out.Groups = append(out.Groups, g)
	}
	if len(out.Groups) == 0 {
		return CloneList{}, fmt.Errorf("clone list has no usable groups")
	}
	return out, nil
}

// Hit is what an overlay knows about a matched dump.
type Hit struct {
	Group      string
	Categories []string
	Priority   int
}

// Overlay resolves a catalogued dump to a clone-list group.
type Overlay struct {
	byTerm map[string]Hit
}

// NewOverlay indexes a parsed list for lookup. A nil Overlay is valid and
// matches nothing, so callers never branch on "is there a list".
func NewOverlay(list CloneList) *Overlay {
	o := &Overlay{byTerm: make(map[string]Hit, len(list.Groups)*2)}
	for _, g := range list.Groups {
		for _, t := range g.Titles {
			key := strings.ToLower(t.SearchTerm)
			// First writer wins: a term claimed by two groups is an upstream
			// ambiguity, and picking deterministically beats picking last.
			if _, dup := o.byTerm[key]; dup {
				continue
			}
			o.byTerm[key] = Hit{Group: g.Name, Categories: g.Categories, Priority: t.Priority}
		}
	}
	return o
}

// Match resolves one dump. The MOST SPECIFIC matching search term wins:
// "1942 (Extended)" beats the bare "1942" for a dump that is both, which is
// how the list distinguishes variants that share a title.
func (o *Overlay) Match(m Member) (Hit, bool) {
	if o == nil || len(o.byTerm) == 0 {
		return Hit{}, false
	}
	var (
		best  Hit
		bestN int
		found bool
	)
	for _, k := range candidateKeys(m.Name) {
		if hit, ok := o.byTerm[strings.ToLower(k)]; ok && len(k) > bestN {
			best, bestN, found = hit, len(k), true
		}
	}
	return best, found
}

var parenTokenRe = regexp.MustCompile(`\(([^)]*)\)`)

// candidateKeys are the name forms a clone list's search terms are written in.
//
// Retool's terms carry the identifying parentheticals a title needs
// ("Centipede (Accolade)") but not the standard tags ("(USA, Europe)"), and
// not always the hardware ones ("(SGB Enhanced)"). Rather than maintain a
// second vocabulary of which tags are identifying — the thing this whole
// design refuses to do — the dump offers every reading of itself and the
// longest match wins:
//
//	"Centipede (USA) (Majesco)" -> "Centipede (USA) (Majesco)",
//	                               "Centipede (Majesco)", "Centipede"
func candidateKeys(name string) []string {
	attrs := selection.Parse(name)
	clean := strings.TrimSpace(attrs.CleanTitle)
	bare := strings.TrimSpace(selection.BareTitle(name))

	keys := []string{}
	add := func(k string) {
		k = strings.TrimSpace(k)
		if k == "" {
			return
		}
		for _, have := range keys {
			if strings.EqualFold(have, k) {
				return
			}
		}
		keys = append(keys, k)
	}
	add(strings.TrimSpace(name))
	add(clean)
	add(bare)
	if bare != "" {
		for _, m := range parenTokenRe.FindAllStringSubmatch(clean, -1) {
			if tok := strings.TrimSpace(m[1]); tok != "" {
				add(bare + " (" + tok + ")")
			}
		}
	}
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	return keys
}
