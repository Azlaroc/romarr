package platform

import "testing"

func TestRommSlugRoundTrip(t *testing.T) {
	cases := []struct{ internal, fs string }{
		{"genesis", "genesis-slash-megadrive"}, // the one live divergence
		{"psx", "psx"},
		{"nes", "nes"},
		{"arcade", "arcade"}, // RomM-only platform: identity both ways
	}
	for _, c := range cases {
		if got := ToRommFSSlug(c.internal); got != c.fs {
			t.Errorf("ToRommFSSlug(%q) = %q, want %q", c.internal, got, c.fs)
		}
		if got := FromRommFSSlug(c.fs); got != c.internal {
			t.Errorf("FromRommFSSlug(%q) = %q, want %q", c.fs, got, c.internal)
		}
	}
}

func TestRommSlugAliasesInvertCleanly(t *testing.T) {
	for internal, fs := range fsSlugAliases {
		if internal == fs {
			t.Errorf("pointless identity alias %q", internal)
		}
		if got := FromRommFSSlug(ToRommFSSlug(internal)); got != internal {
			t.Errorf("round trip broke for %q: got %q", internal, got)
		}
	}
}
