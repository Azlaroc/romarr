// datdrift replays the library's stored hashes against the local DAT
// snapshot and reports how today's on-disk names compare with what the
// catalog would call each file. It is the read-only evidence gate before the
// naming authority switches to the snapshot: run it, read the drift, then
// flip.
//
// It shares the production code paths — db.ParseGamarrHashes,
// db.LookupDatRomsByHash, datname.Resolve/ProposedName — so its report and a
// normalize preview cannot disagree.
//
// ⚠ Opening the store runs schema migrations. Point --db at a COPY of the
// database, never the live file. The tool itself never reads or writes the
// filesystem beyond that one sqlite file.
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gamarr/internal/datname"
	"gamarr/internal/db"
)

const (
	classMatch    = "match"
	classDrift    = "drift"
	classAmbig    = "ambiguous"
	classNoDatRow = "no-dat-row"
	classNoHash   = "no-hash"
	classSkip     = "skip"
)

var classes = []string{classMatch, classDrift, classAmbig, classNoDatRow, classNoHash, classSkip}

type sample struct {
	Class    string `json:"class"`
	Old      string `json:"old"`
	Proposed string `json:"proposed,omitempty"`
	Game     string `json:"game,omitempty"`
	Detail   string `json:"detail,omitempty"`
}

type platformReport struct {
	Counts  map[string]int `json:"counts"`
	Total   int            `json:"total"`
	Samples []sample       `json:"samples,omitempty"`
}

func main() {
	dbPath := flag.String("db", "", "path to a COPY of gamarr.db (required; opening runs migrations)")
	platformFlag := flag.String("platform", "", "platform slug to replay (default: all)")
	sampleN := flag.Int("sample", 20, "drift/ambiguous samples to keep per platform")
	jsonOut := flag.Bool("json", false, "emit the full report as JSON")
	flag.Parse()

	if *dbPath == "" {
		fmt.Fprintln(os.Stderr, "usage: datdrift --db <copy-of-gamarr.db> [--platform slug] [--sample N] [--json]")
		os.Exit(2)
	}
	fmt.Fprintln(os.Stderr, "⚠ datdrift opens the store with migrations enabled — this must be a COPY, never the live database.")

	store, err := db.New(*dbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open %s: %v\n", *dbPath, err)
		os.Exit(1)
	}
	defer store.Close()

	items := store.ListLibraryItemsForRename(*platformFlag)
	reports := map[string]*platformReport{}

	for i := range items {
		it := &items[i]
		rep := reports[it.PlatformSlug]
		if rep == nil {
			rep = &platformReport{Counts: map[string]int{}}
			reports[it.PlatformSlug] = rep
		}
		rep.Total++

		class, smp := classify(store, it)
		rep.Counts[class]++
		if smp != nil && len(rep.Samples) < *sampleN {
			rep.Samples = append(rep.Samples, *smp)
		}
	}

	if *jsonOut {
		enc := json.NewEncoder(os.Stdout)
		enc.SetIndent("", "  ")
		enc.Encode(reports)
		return
	}
	printTables(reports)
}

// classify runs one library row through the exact resolver the rename plane
// will use. No filesystem access: hashes come from the row's metadata, the
// answer from the snapshot.
func classify(store *db.JobStore, it *db.LibraryItem) (string, *sample) {
	base := filepath.Base(it.FilePath)
	ext := strings.ToLower(filepath.Ext(base))

	gh, haveGamarr := db.ParseGamarrHashes(it.Metadata)
	rCRC, rMD5, rSHA1, haveRomm := db.ParseRommContentHashes(it.Metadata)
	if !haveGamarr && !haveRomm {
		if reason := hashSkipReason(it.Metadata); reason != "" {
			return classSkip, &sample{Class: classSkip, Old: base, Detail: reason}
		}
		return classNoHash, nil
	}

	var matches []db.DatRomMatch
	if haveGamarr {
		matches = store.LookupDatRomsByHash(it.PlatformSlug, gh.CRC, gh.MD5, gh.SHA1)
		if len(matches) == 0 && gh.Unh != nil {
			matches = store.LookupDatRomsByHash(it.PlatformSlug, gh.Unh.CRC, gh.Unh.MD5, gh.Unh.SHA1)
		}
	}
	if len(matches) == 0 && haveRomm {
		matches = store.LookupDatRomsByHash(it.PlatformSlug, rCRC, rMD5, rSHA1)
	}

	cands := make([]datname.Candidate, 0, len(matches))
	for _, m := range matches {
		cands = append(cands, datname.Candidate{RomName: m.RomName, GameName: m.GameName})
	}
	res := datname.Resolve(cands)
	switch res.Outcome {
	case datname.NoMatch:
		return classNoDatRow, nil
	case datname.Ambiguous:
		return classAmbig, &sample{Class: classAmbig, Old: base, Detail: strings.Join(res.Stems, " | ")}
	}

	archiveExt := ""
	if ext == ".zip" || ext == ".7z" {
		archiveExt = ext
	}
	proposed := datname.ProposedName(res.Stem, res.Ext, base, archiveExt)
	if proposed == base {
		return classMatch, nil
	}
	return classDrift, &sample{Class: classDrift, Old: base, Proposed: proposed, Game: res.GameName}
}

// hashSkipReason surfaces $.gamarr.hash_skipped so permanently unhashable
// rows (directories, multi-file archives, rar) read as skips, not gaps.
func hashSkipReason(metadata string) string {
	var envelope struct {
		Gamarr struct {
			HashSkipped string `json:"hash_skipped"`
		} `json:"gamarr"`
	}
	if err := json.Unmarshal([]byte(metadata), &envelope); err != nil {
		return ""
	}
	return envelope.Gamarr.HashSkipped
}

func printTables(reports map[string]*platformReport) {
	slugs := make([]string, 0, len(reports))
	for s := range reports {
		slugs = append(slugs, s)
	}
	sort.Strings(slugs)

	totals := map[string]int{}
	grand := 0
	fmt.Printf("%-22s %7s %7s %7s %7s %10s %8s %6s\n",
		"platform", "total", classMatch, classDrift, classAmbig, classNoDatRow, classNoHash, classSkip)
	for _, slug := range slugs {
		rep := reports[slug]
		fmt.Printf("%-22s %7d %7d %7d %7d %10d %8d %6d\n",
			slug, rep.Total,
			rep.Counts[classMatch], rep.Counts[classDrift], rep.Counts[classAmbig],
			rep.Counts[classNoDatRow], rep.Counts[classNoHash], rep.Counts[classSkip])
		for _, c := range classes {
			totals[c] += rep.Counts[c]
		}
		grand += rep.Total
	}
	fmt.Printf("%-22s %7d %7d %7d %7d %10d %8d %6d\n",
		"TOTAL", grand,
		totals[classMatch], totals[classDrift], totals[classAmbig],
		totals[classNoDatRow], totals[classNoHash], totals[classSkip])

	for _, slug := range slugs {
		rep := reports[slug]
		if len(rep.Samples) == 0 {
			continue
		}
		fmt.Printf("\n── %s samples ──\n", slug)
		for _, s := range rep.Samples {
			switch s.Class {
			case classDrift:
				fmt.Printf("  drift: %s -> %s  [%s]\n", s.Old, s.Proposed, s.Game)
			case classAmbig:
				fmt.Printf("  ambiguous: %s  (%s)\n", s.Old, s.Detail)
			case classSkip:
				fmt.Printf("  skip: %s  (%s)\n", s.Old, s.Detail)
			}
		}
	}
}
