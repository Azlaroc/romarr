package datsvc

import (
	"fmt"
	"net/url"
	"path/filepath"
	"regexp"
	"strings"
)

// dat_code is driver-specific, so a code that belongs to a different driver
// would otherwise fail only at fetch time — hours later, as a "partial"
// refresh nobody is watching. internal/db deliberately leaves the pair
// unvalidated ("callers validate the pair before saving"); these rules are
// that validation, and both the platform write and the authority patch run
// them.

// redumpCode matches a Redump system code as it appears in a datfile URL.
var redumpCode = regexp.MustCompile(`^[a-z0-9-]{2,16}$`)

// platformSlug is the charset a slug must keep: it becomes a filename
// component under DataDir and a key in every DAT table.
//
// It is deliberately NOT checked against internal/platform's map. That map
// serves download-side category detection and has never carried atari2600,
// gbc, sms or gg — all of which the shipped DAT pack assigns. The catalog
// vocabulary is wider on purpose; do not "fix" this by joining them.
var platformSlug = regexp.MustCompile(`^[a-z0-9][a-z0-9-]{0,31}$`)

// ValidateSlug checks a platform slug's shape.
func ValidateSlug(slug string) error {
	if !platformSlug.MatchString(strings.TrimSpace(slug)) {
		return fmt.Errorf("platform slug %q must be lowercase letters, digits and hyphens", slug)
	}
	return nil
}

// ValidateDriver rejects a fetch_driver no driver implements.
func ValidateDriver(driver string) error {
	switch driver {
	case DriverLibretro, DriverRedump, DriverUpload:
		return nil
	default:
		return fmt.Errorf("unknown fetch driver %q (want %s, %s or %s)",
			driver, DriverLibretro, DriverRedump, DriverUpload)
	}
}

// ValidateDriverCode checks that a dat_code is usable by a driver.
func ValidateDriverCode(driver, datCode string) error {
	code := strings.TrimSpace(datCode)
	switch driver {
	case DriverLibretro:
		if code == "" {
			return fmt.Errorf("%s dat_code is the mirror's DAT name, e.g. \"Atari - 2600\"", DriverLibretro)
		}
		if strings.ContainsAny(code, `/\`) || !filepath.IsLocal(code) {
			return fmt.Errorf("%s dat_code %q must be a plain DAT name, not a path", DriverLibretro, code)
		}
		// The driver appends the extension; carrying it in the stored code
		// would fetch "...dat.dat".
		if strings.HasSuffix(strings.ToLower(code), ".dat") {
			return fmt.Errorf("%s dat_code %q must omit the .dat extension", DriverLibretro, code)
		}
		return nil
	case DriverRedump:
		if !redumpCode.MatchString(code) {
			return fmt.Errorf("%s dat_code %q must be a system code like \"psx\" or \"ss\"", DriverRedump, code)
		}
		return nil
	case DriverUpload:
		// Hand-fed authorities need no locator: the uploader names the
		// platform. A code is still allowed so an authority can be flipped
		// to an automated driver later without retyping every assignment.
		return nil
	default:
		return ValidateDriver(driver)
	}
}

// ValidateFetchBase checks an authority's base URL. Upload-driven
// authorities may leave it empty; everything else must be an absolute
// http(s) URL, because the base is an operator-editable field and a typo
// otherwise surfaces as an unexplained refresh failure.
func ValidateFetchBase(driver, base string) error {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		if driver == DriverUpload {
			return nil
		}
		return fmt.Errorf("fetch_base is required for the %s driver", driver)
	}
	u, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("fetch_base %q is not a URL: %w", base, err)
	}
	if u.Scheme != "http" && u.Scheme != "https" || u.Host == "" {
		return fmt.Errorf("fetch_base %q must be an absolute http or https URL", base)
	}
	return checkHost(u.Hostname())
}

// checkHost enforces the two source rules that cost real outages if broken.
// They live here rather than in a handler so that every caller — the API, a
// future importer, a settings migration — inherits them.
func checkHost(host string) error {
	h := strings.ToLower(host)
	// Dat-o-Matic is scriptable, which is the trap rather than the reason:
	// its anti-scrape cron bans IPs silently, and an egress ban on a
	// production box surfaces hours later as "someone can't reach the
	// server". Every project in this space keeps No-Intro human-in-the-loop.
	if strings.Contains(h, "datomatic") || h == "no-intro.org" || strings.HasSuffix(h, ".no-intro.org") {
		return fmt.Errorf("%s bans scrapers by IP: fetch No-Intro through the libretro mirror, or upload a daily pack by hand", host)
	}
	// redump.org serves no TLS on 443 and lags behind .info. Measured
	// 2026-08-16: PSX 10914/2026-06-15 there versus 10970/2026-08-16 on
	// redump.info.
	if h == "redump.org" || strings.HasSuffix(h, ".redump.org") {
		return fmt.Errorf("use redump.info rather than %s: the .org host has no TLS on 443 and serves staler data", host)
	}
	return nil
}
