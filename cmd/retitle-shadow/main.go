// retitle-shadow runs the retitle PREVIEW against a copy of a production
// gamarr.db and prints the shadow-run assertions the P3 gate needs. It never
// boots the app — no scheduler, no sources, no HTTP — and it only ever
// previews: the apply path is not reachable from here.
//
// Usage: retitle-shadow /path/to/gamarr-copy.db
package main

import (
	"encoding/json"
	"fmt"
	"os"
	"time"
	"unicode"

	"gamarr/internal/db"
	"gamarr/internal/retitle"
)

func nonASCII(s string) bool {
	for _, r := range s {
		if r > unicode.MaxASCII {
			return true
		}
	}
	return false
}

func main() {
	if len(os.Args) != 2 {
		fmt.Fprintln(os.Stderr, "usage: retitle-shadow <gamarr.db copy>")
		os.Exit(2)
	}
	store, err := db.New(os.Args[1])
	if err != nil {
		fmt.Fprintln(os.Stderr, "open:", err)
		os.Exit(1)
	}
	defer store.Close()

	r := retitle.New(store)
	if !r.TriggerPreview() {
		fmt.Fprintln(os.Stderr, "preview did not start")
		os.Exit(1)
	}
	for {
		st := r.Status()
		if st["state"] == "done" {
			break
		}
		if st["state"] == "error" {
			fmt.Fprintln(os.Stderr, "preview error:", st["error"])
			os.Exit(1)
		}
		time.Sleep(200 * time.Millisecond)
	}

	var all []retitle.PreviewRow
	for page := 1; ; page++ {
		rows, total := r.PreviewPage(page, 200)
		if len(rows) == 0 {
			_ = total
			break
		}
		all = append(all, rows...)
	}

	counts := map[string]int{}
	var nonASCIIRows, suppressed, skips []retitle.PreviewRow
	var datSample, stemSample []retitle.PreviewRow
	for _, row := range all {
		counts[row.Status]++
		if row.Status == retitle.StatusRetitle {
			counts["source:"+row.Source]++
			if len(datSample) < 5 && row.Source == retitle.SourceDat {
				datSample = append(datSample, row)
			}
			if len(stemSample) < 5 && row.Source == retitle.SourceStem {
				stemSample = append(stemSample, row)
			}
		}
		if nonASCII(row.Old) {
			nonASCIIRows = append(nonASCIIRows, row)
		}
		if row.Status == retitle.StatusSkip {
			skips = append(skips, row)
		}
		if row.Reason != "" && row.Status == retitle.StatusRetitle {
			suppressed = append(suppressed, row)
		}
	}

	nonASCIIUnresolved := 0
	for _, row := range nonASCIIRows {
		if row.Status == retitle.StatusSkip {
			nonASCIIUnresolved++
		}
	}

	out := map[string]interface{}{
		"rows_total":           len(all),
		"counts":               counts,
		"non_ascii_old_total":  len(nonASCIIRows),
		"non_ascii_unresolved": nonASCIIUnresolved,
		"follow_suppressed":    suppressed,
		"skips":                skips,
		"dat_sample":           datSample,
		"stem_sample":          stemSample,
	}
	enc := json.NewEncoder(os.Stdout)
	enc.SetIndent("", "  ")
	_ = enc.Encode(out)
}
