package db

import (
	"testing"
	"time"
)

func gap(key, title, dump string) CollectionGap {
	return CollectionGap{SetKey: key, Title: title, DumpName: dump}
}

func targetsOf(t *testing.T, store *JobStore, slug string) map[string]CollectionTarget {
	t.Helper()
	rows, _ := store.ListCollectionTargets(CollectionTargetQuery{PlatformSlug: slug, Limit: 200})
	out := map[string]CollectionTarget{}
	for _, r := range rows {
		out[r.SetKey] = r
	}
	return out
}

// 🔴 A gap that is STILL a gap must keep its attempt history. Re-inserting it
// every sync would reset the backoff, and a title nothing indexes would be
// searched every single cycle forever.
func TestSyncKeepsAttemptHistoryAndDropsFilledGaps(t *testing.T) {
	store, _ := datStore(t)

	added, removed := store.SyncCollectionTargets("atari7800", []CollectionGap{
		gap("title:xevious", "Xevious", "Xevious (USA)"),
		gap("title:super huey", "Super Huey", "Super Huey UH-IX (USA)"),
	})
	if added != 2 || removed != 0 {
		t.Fatalf("first sync = %d added / %d removed, want 2 / 0", added, removed)
	}

	before := targetsOf(t, store, "atari7800")["title:xevious"]
	store.RecordCollectionAttempt(before.ID, TargetUnavailable, "no results")

	// Second sync: Xevious is still missing, Super Huey has been acquired.
	added, removed = store.SyncCollectionTargets("atari7800", []CollectionGap{
		gap("title:xevious", "Xevious", "Xevious (USA)"),
	})
	if added != 0 || removed != 1 {
		t.Fatalf("second sync = %d added / %d removed, want 0 / 1", added, removed)
	}

	rows := targetsOf(t, store, "atari7800")
	if len(rows) != 1 {
		t.Fatalf("targets = %d, want only the still-missing one", len(rows))
	}
	kept := rows["title:xevious"]
	if kept.Attempts != 1 {
		t.Errorf("attempts = %d, want the recorded attempt preserved", kept.Attempts)
	}
	if kept.Status != TargetUnavailable || kept.LastReason != "no results" {
		t.Errorf("status/reason = %q/%q, want the recorded outcome preserved", kept.Status, kept.LastReason)
	}
}

func TestSyncRefreshesTitleAndDumpName(t *testing.T) {
	store, _ := datStore(t)
	store.SyncCollectionTargets("gb", []CollectionGap{gap("clone:operation c", "Operation C", "Operation C (USA)")})
	store.SyncCollectionTargets("gb", []CollectionGap{gap("clone:operation c", "Operation C", "Operation C (USA) (Rev 1)")})

	got := targetsOf(t, store, "gb")["clone:operation c"]
	if got.DumpName != "Operation C (USA) (Rev 1)" {
		t.Errorf("dump_name = %q, want the catalog's current answer", got.DumpName)
	}
}

func TestClearCollectionTargets(t *testing.T) {
	store, _ := datStore(t)
	store.SyncCollectionTargets("gb", []CollectionGap{gap("a", "A", ""), gap("b", "B", "")})
	store.SyncCollectionTargets("nes", []CollectionGap{gap("c", "C", "")})

	if n := store.ClearCollectionTargets("gb"); n != 2 {
		t.Errorf("cleared = %d, want 2", n)
	}
	if len(targetsOf(t, store, "gb")) != 0 {
		t.Error("gb targets survived a clear")
	}
	if len(targetsOf(t, store, "nes")) != 1 {
		t.Error("clearing one platform took another platform's targets with it")
	}
}

// Backoff is what stops a title no source carries from being searched every
// hour. The expectations are COMPUTED from the same rule the code states —
// doubling hours per attempt, capped at a week — rather than restated.
func TestDueBacksOffExponentially(t *testing.T) {
	now := time.Date(2026, 8, 19, 12, 0, 0, 0, time.UTC)
	stamp := func(d time.Duration) string { return now.Add(-d).Format("2006-01-02 15:04:05") }

	for _, tc := range []struct {
		attempts int
		since    time.Duration
		want     bool
	}{
		{0, 0, true},                 // never tried
		{1, 30 * time.Minute, false}, // 1h window
		{1, 2 * time.Hour, true},
		{3, 3 * time.Hour, false}, // 4h window
		{3, 5 * time.Hour, true},
		{20, 6 * 24 * time.Hour, false}, // capped at a week
		{20, 8 * 24 * time.Hour, true},
	} {
		target := CollectionTarget{Attempts: tc.attempts, LastAttempt: stamp(tc.since)}
		if tc.attempts == 0 {
			target.LastAttempt = ""
		}
		if got := target.due(now); got != tc.want {
			t.Errorf("attempts=%d since=%v due = %v, want %v", tc.attempts, tc.since, got, tc.want)
		}
	}
}

func TestDueCollectionTargetsOrdersAndLimits(t *testing.T) {
	store, _ := datStore(t)
	store.SyncCollectionTargets("gb", []CollectionGap{
		gap("a", "A title", ""), gap("b", "B title", ""), gap("c", "C title", ""),
	})
	now := time.Now().UTC()

	due := store.DueCollectionTargets("gb", 2, now)
	if len(due) != 2 {
		t.Fatalf("due = %d, want the limit of 2", len(due))
	}

	// A grabbed target is out of the queue: its release is on its way, and
	// searching for it again would grab it twice.
	all, _ := store.ListCollectionTargets(CollectionTargetQuery{PlatformSlug: "gb", Limit: 10})
	store.RecordCollectionAttempt(all[0].ID, TargetGrabbed, "grabbed")
	due = store.DueCollectionTargets("gb", 10, now)
	for _, d := range due {
		if d.ID == all[0].ID {
			t.Error("a grabbed target came back around")
		}
	}
	if len(due) != 2 {
		t.Errorf("due after one grab = %d, want 2", len(due))
	}

	counts := store.CollectionTargetCounts()
	if counts[TargetGrabbed] != 1 || counts[TargetWanted] != 2 {
		t.Errorf("counts = %v, want 1 grabbed / 2 wanted", counts)
	}
}
