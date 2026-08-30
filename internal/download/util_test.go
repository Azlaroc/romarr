package download

import (
	"testing"
)

func TestCleanTitle(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"strip zip extension", "Game.zip", "Game"},
		{"strip nsp extension", "Zelda.nsp", "Zelda"},
		{"strip rar extension", "Game.rar", "Game"},
		{"strip 7z extension", "Game.7z", "Game"},
		{"strip iso extension", "Game.iso", "Game"},
		{"URL decode spaces", "Super%20Mario%20Bros", "Super Mario Bros"},
		{"URL decode parens", "Game%28USA%29", "Game(USA)"},
		{"URL decode comma", "Game%2C Part 2", "Game, Part 2"},
		{"trim whitespace", "  Game  ", "Game"},
		{"no extension to strip", "Plain Game", "Plain Game"},
		{"case insensitive ext", "Game.ZIP", "Game"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := cleanTitle(tt.in)
			if got != tt.want {
				t.Errorf("cleanTitle(%q) = %q, want %q", tt.in, got, tt.want)
			}
		})
	}
}

func TestTitlesMatch(t *testing.T) {
	tests := []struct {
		name        string
		title       string
		torrentName string
		want        bool
	}{
		{"exact", "Super Game", "Super Game", true},
		{"case insensitive", "Super Game", "SUPER game", true},
		{"torrent renamed with suffix", "Super Game", "Super Game (USA) [Repack]", true},
		{"title contains torrent name", "Super Game Deluxe Edition", "super game deluxe", true},
		{"unrelated", "Super Game", "Other Thing", false},
		{"empty title never matches", "", "Anything", false},
		{"empty torrent name never matches", "Super Game", "", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := titlesMatch(tt.title, tt.torrentName); got != tt.want {
				t.Errorf("titlesMatch(%q, %q) = %v, want %v", tt.title, tt.torrentName, got, tt.want)
			}
		})
	}
}
