package datsvc

import "testing"

func TestValidateDriverCode(t *testing.T) {
	cases := []struct {
		name    string
		driver  string
		code    string
		wantErr bool
	}{
		{"libretro seeded name", DriverLibretro, "Atari - 2600", false},
		{"libretro long seeded name", DriverLibretro, "Nintendo - Super Nintendo Entertainment System", false},
		// The driver appends .dat; a stored extension fetches "...dat.dat".
		{"libretro with extension", DriverLibretro, "Atari - 2600.dat", true},
		{"libretro with path", DriverLibretro, "no-intro/Atari - 2600", true},
		{"libretro traversal", DriverLibretro, "../secrets", true},
		{"libretro empty", DriverLibretro, "", true},
		{"redump code", DriverRedump, "psx", false},
		{"redump saturn code", DriverRedump, "ss", false},
		{"redump xbox360 code", DriverRedump, "xbox360", false},
		// A Redump assignment carrying a No-Intro DAT name is the exact
		// mismatch that would otherwise surface hours later as a partial
		// refresh nobody was watching.
		{"redump given a DAT name", DriverRedump, "Sony - PlayStation", true},
		{"redump empty", DriverRedump, "", true},
		{"upload needs no locator", DriverUpload, "", false},
		{"unknown driver", "datomatic", "psx", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateDriverCode(tc.driver, tc.code)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateDriverCode(%q, %q) = %v, wantErr %v", tc.driver, tc.code, err, tc.wantErr)
			}
		})
	}
}

func TestValidateDriverCodeMatchesTheShippedSeed(t *testing.T) {
	// Guards the shipped pack against a rule that outlaws its own data.
	seeded := map[string]string{
		"Atari - 2600":                DriverLibretro,
		"Nintendo - Game Boy Color":   DriverLibretro,
		"Sega - Mega Drive - Genesis": DriverLibretro,
		"psx":                         DriverRedump,
		"gc":                          DriverRedump,
		"ss":                          DriverRedump,
	}
	for code, driver := range seeded {
		if err := ValidateDriverCode(driver, code); err != nil {
			t.Fatalf("shipped assignment %s/%q rejected: %v", driver, code, err)
		}
	}
}

func TestValidateFetchBase(t *testing.T) {
	cases := []struct {
		name    string
		driver  string
		base    string
		wantErr bool
	}{
		{"libretro mirror", DriverLibretro, "https://raw.githubusercontent.com/libretro/libretro-database/master/metadat/no-intro/", false},
		{"redump info", DriverRedump, "https://redump.info/datfile/", false},
		{"loopback stub", DriverRedump, "http://127.0.0.1:8123/datfile/", false},
		{"upload may be empty", DriverUpload, "", false},
		{"fetching driver may not be empty", DriverRedump, "", true},
		{"relative path", DriverRedump, "/datfile/", true},
		{"no host", DriverRedump, "https://", true},
		{"wrong scheme", DriverRedump, "ftp://redump.info/datfile/", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateFetchBase(tc.driver, tc.base)
			if (err != nil) != tc.wantErr {
				t.Fatalf("ValidateFetchBase(%q, %q) = %v, wantErr %v", tc.driver, tc.base, err, tc.wantErr)
			}
		})
	}
}

func TestValidateDriver(t *testing.T) {
	for _, d := range []string{DriverLibretro, DriverRedump, DriverUpload} {
		if err := ValidateDriver(d); err != nil {
			t.Fatalf("ValidateDriver(%q) = %v", d, err)
		}
	}
	if err := ValidateDriver("datomatic"); err == nil {
		t.Fatal("Dat-o-Matic is deliberately not a driver; it must not validate")
	}
}

// The two source rules that cost real outages if a later cleanup "fixes"
// them. Both are enforced where every caller inherits them, not in a handler.
func TestValidateFetchBaseRefusesTheBannedSources(t *testing.T) {
	for _, base := range []string{
		"https://datomatic.no-intro.org/index.php?page=download",
		"https://no-intro.org/",
		"http://redump.org/datfile/psx/",
		"https://redump.org/datfile/",
	} {
		if err := ValidateFetchBase(DriverRedump, base); err == nil {
			t.Fatalf("ValidateFetchBase(%q) accepted a source we must never point at", base)
		}
	}
	// The one we do use must stay usable.
	if err := ValidateFetchBase(DriverRedump, "https://redump.info/datfile/"); err != nil {
		t.Fatalf("redump.info rejected: %v", err)
	}
}
