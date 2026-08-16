package api

import (
	"fmt"
	"testing"

	"gamarr/internal/config"
)

func TestSourceRegistryGetAndUpdate(t *testing.T) {
	env := newTestEnv(t, func(c *config.Config) {
		// Seed happens in main normally; the test env mimics it.
	})
	env.cfg.SetSourcesRegistry(env.jobs.LoadSourceRegistry(env.cfg.Sources))

	rr := env.do("GET", "/api/source-registry", "")
	wantStatus(t, rr, 200)
	m := decodeMap(t, rr)
	rows, _ := m["sources"].([]interface{})
	if len(rows) != 2 {
		t.Fatalf("sources = %v", m)
	}

	before := env.cfg.SourcesRegistry()

	// Disable vimm; the registry pointer must rotate (the intentional
	// IA-memo drop) and the change must be visible in cfg immediately.
	rr = env.do("PUT", "/api/source-registry/vimm", `{"enabled":false}`)
	wantStatus(t, rr, 200)
	row := decodeMap(t, rr)
	if row["enabled"] != false || row["active"] != false {
		t.Fatalf("vimm row after disable = %v", row)
	}
	after := env.cfg.SourcesRegistry()
	if before == after {
		t.Fatal("registry pointer did not rotate on write")
	}
	if after.Vimm.IsEnabled() {
		t.Fatal("cfg registry still has vimm enabled")
	}

	// Validation.
	rr = env.do("PUT", "/api/source-registry/vimm", `{"base_url":"notaurl"}`)
	wantStatus(t, rr, 400)
	rr = env.do("PUT", "/api/source-registry/unknown", `{"enabled":true}`)
	wantStatus(t, rr, 404)

	// Mapping replace round-trips.
	rr = env.do("PUT", "/api/source-registry/archiveorg", `{"mapping":{"snes":"item-snes"}}`)
	wantStatus(t, rr, 200)
	if row := decodeMap(t, rr); row["active"] != true {
		t.Fatalf("archiveorg row = %v", row)
	}
}

func TestDDLSourcesCRUDByID(t *testing.T) {
	env := newTestEnv(t, nil)
	env.cfg.SetSourcesRegistry(env.jobs.LoadSourceRegistry(env.cfg.Sources))

	// Built-in Vimm row always present, no id.
	rr := env.do("GET", "/api/ddl-sources", "")
	wantStatus(t, rr, 200)
	rows, _ := decodeMap(t, rr)["sources"].([]interface{})
	if len(rows) != 1 {
		t.Fatalf("initial rows = %v", rows)
	}

	rr = env.do("POST", "/api/ddl-sources", `{"name":"MySite","url":"https://example.test"}`)
	wantStatus(t, rr, 200)
	added := decodeMap(t, rr)
	if added["success"] != true || added["id"] == nil {
		t.Fatalf("add = %v", added)
	}
	id := int64(added["id"].(float64))

	rr = env.do("GET", "/api/ddl-sources", "")
	rows, _ = decodeMap(t, rr)["sources"].([]interface{})
	if len(rows) != 2 {
		t.Fatalf("rows after add = %v", rows)
	}

	rr = env.do("DELETE", "/api/ddl-sources/999999", "")
	wantStatus(t, rr, 404)
	rr = env.do("DELETE", "/api/ddl-sources/garbage", "")
	wantStatus(t, rr, 400)
	rr = env.do("DELETE", fmt.Sprintf("/api/ddl-sources/%d", id), "")
	wantStatus(t, rr, 200)

	rr = env.do("GET", "/api/ddl-sources", "")
	if rows, _ := decodeMap(t, rr)["sources"].([]interface{}); len(rows) != 1 {
		t.Fatalf("rows after delete = %v", rows)
	}
}
