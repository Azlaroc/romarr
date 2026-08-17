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
	return nil
}
