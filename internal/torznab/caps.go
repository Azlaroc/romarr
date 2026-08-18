package torznab

import "gamarr/internal/platform"

// BuildCaps returns the Torznab capabilities document, advertising game-
// specific Newznab categories so Prowlarr can route searches correctly.
//
// Newznab category numbering reference:
//
//	1000 Console
//	  1010 Console/NDS    1030 Console/Wii   1080 Console/PS3
//	  1020 Console/PSP    1040 Console/Xbox  1090 Console/Other
//	                      1050 Console/Xbox 360
//	4000 PC
//	  4020 PC/0day        4050 PC/Mac        4070 PC/Games
//	  4030 PC/ISO         4060 PC/Mobile-Other
func BuildCaps() *Caps {
	return &Caps{
		Server: CapsServer{Title: "Gamarr"},
		Limits: CapsLimits{Max: 100, Default: 50},
		Searching: CapsSearching{
			Search:        CapsSearchOp{Available: "yes", SupportedParams: "q"},
			ConsoleSearch: CapsSearchOp{Available: "yes", SupportedParams: "q,platform"},
			PCSearch:      CapsSearchOp{Available: "yes", SupportedParams: "q"},
		},
		Categories: CapsCategories{
			Categories: []CapsCategory{
				{
					ID: "1000", Name: "Console",
					Subs: []CapsSubCategory{
						{ID: "1010", Name: "Console/NDS"},
						{ID: "1020", Name: "Console/PSP"},
						{ID: "1030", Name: "Console/Wii"},
						{ID: "1040", Name: "Console/Xbox"},
						{ID: "1050", Name: "Console/Xbox 360"},
						{ID: "1080", Name: "Console/PS3"},
						{ID: "1090", Name: "Console/Other"},
					},
				},
				{
					ID: "4000", Name: "PC",
					Subs: []CapsSubCategory{
						{ID: "4070", Name: "PC/Games"},
					},
				},
			},
		},
	}
}

// CategoryForPlatform maps a platform slug to a Torznab category ID, so
// Prowlarr can route results back to the right *arr instance. The mapping is
// a registry column; Console/Other stays the answer for anything unmapped.
func CategoryForPlatform(slug string) string {
	return platform.TorznabCategory(slug)
}

func categoryName(id string) string {
	switch id {
	case "1000":
		return "Console"
	case "1010":
		return "Console/NDS"
	case "1020":
		return "Console/PSP"
	case "1030":
		return "Console/Wii"
	case "1040":
		return "Console/Xbox"
	case "1050":
		return "Console/Xbox 360"
	case "1080":
		return "Console/PS3"
	case "1090":
		return "Console/Other"
	case "4000":
		return "PC"
	case "4070":
		return "PC/Games"
	}
	return "Console"
}
