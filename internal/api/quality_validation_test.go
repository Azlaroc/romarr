package api

import (
	"fmt"
	"testing"
)

func TestQualityProfileValidation(t *testing.T) {
	env := newTestEnv(t, nil)

	t.Run("region tokens no longer validated here", func(t *testing.T) {
		// The field is retired off quality profiles (the collection profile
		// owns regions); the API tolerates legacy payloads instead of
		// policing a column that only awaits its drop.
		rr := env.do("POST", "/api/quality-profiles", `{"name":"Legacy Region Payload","region_priority":["usa","narnia"]}`)
		wantStatus(t, rr, 201)
	})

	t.Run("unknown format token rejected", func(t *testing.T) {
		rr := env.do("POST", "/api/quality-profiles", `{"name":"Bad Format","format_preference":["chd","tar"]}`)
		wantStatus(t, rr, 400)
	})

	t.Run("collection profiles own region validation", func(t *testing.T) {
		rr := env.do("POST", "/api/collection-profiles", `{"name":"Bad Region","region_priority":["usa","narnia"]}`)
		wantStatus(t, rr, 400)
		rr = env.do("POST", "/api/collection-profiles", `{"name":"Mixed Case","region_priority":["USA","World"]}`)
		wantStatus(t, rr, 201)
	})

	t.Run("duplicate platform_slug conflicts", func(t *testing.T) {
		rr := env.do("POST", "/api/quality-profiles", `{"name":"N64 A","platform_slug":"n64"}`)
		wantStatus(t, rr, 201)
		rr = env.do("POST", "/api/quality-profiles", `{"name":"N64 B","platform_slug":"n64"}`)
		wantStatus(t, rr, 409)
	})

	t.Run("update to a taken platform_slug conflicts", func(t *testing.T) {
		rr := env.do("POST", "/api/quality-profiles", `{"name":"GBA","platform_slug":"gba"}`)
		wantStatus(t, rr, 201)
		id := int64(decodeMap(t, rr)["id"].(float64))
		rr = env.do("PUT", fmt.Sprintf("/api/quality-profiles/%d", id), `{"name":"GBA","platform_slug":"n64"}`)
		wantStatus(t, rr, 409)
		// Keeping its own slug on a full-body update is fine.
		rr = env.do("PUT", fmt.Sprintf("/api/quality-profiles/%d", id), `{"name":"GBA v2","platform_slug":"gba"}`)
		wantStatus(t, rr, 200)
	})
}
