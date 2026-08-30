"""Library-scan journey — the root-folder scanner end to end over the API:

  (a) a file dropped into a platform directory OUTSIDE every import funnel
      appears in the library after a scan, sourced `libscan`;
  (b) a second scan ADOPTS that row instead of duplicating it;
  (c) nothing else changes: the scanner deletes no rows and no files.

Scoped to lynx — a seeded platform no other journey touches — so adopting
cannot annotate rows other tests assert on. Cleanup lives in `finally`, not
after the asserts: a journey must be state-neutral even when it FAILS.
"""
import json
import time
import urllib.request

SLOW_S = 30


def _req(base: str, path: str, method: str = "GET", payload: dict | None = None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def _wait_idle(base: str, timeout=SLOW_S):
    deadline = time.time() + timeout
    while time.time() < deadline:
        st = _req(base, "/api/library/scan/status")
        if st.get("finished_at") and not st.get("running"):
            return st
        time.sleep(0.3)
    raise AssertionError("scan did not finish")


def _lynx_rows(base: str) -> list:
    lib = _req(base, "/api/library?platform=lynx&page_size=50")
    return lib if isinstance(lib, list) else lib.get("items") or lib.get("library") or []


def test_scan_adopts_and_creates(app):
    base = app["base"]
    platform_dir = app["roms_dir"] / "lynx"
    platform_dir.mkdir(exist_ok=True)
    rom = platform_dir / "Out Of Band (USA).lnx"
    row_id = None
    try:
        # (a) An out-of-band arrival: no funnel, no RomM, just a file.
        rom.write_bytes(b"e2e scan journey payload")
        resp = _req(base, "/api/library/scan/run", "POST",
                    {"platform_slug": "lynx"})
        assert resp.get("success"), resp
        st = _wait_idle(base)
        assert st.get("created") == 1 and st.get("errors") == 0, st

        rows = _lynx_rows(base)
        row = next((r for r in rows if "Out Of Band" in (r.get("title") or "")), None)
        assert row, f"scanned file missing from library: {rows}"
        assert row.get("source") == "libscan", row
        row_id = row.get("id")

        # (b) A re-scan adopts, never duplicates.
        resp = _req(base, "/api/library/scan/run", "POST",
                    {"platform_slug": "lynx"})
        assert resp.get("success"), resp
        st = _wait_idle(base)
        assert st.get("created") == 0 and st.get("adopted") == 1, st
        assert len(_lynx_rows(base)) == len(rows), "re-scan minted a duplicate row"

        # (c) The results page names the adoption.
        res = _req(base, "/api/library/scan/results?page=1&page_size=100")
        adopted = [i for i in res.get("items", []) if i.get("status") == "adopted"]
        assert len(adopted) == 1 and adopted[0].get("library_id") == row_id, res
    finally:
        if row_id:
            try:
                _req(base, f"/api/library/{row_id}", "DELETE")
            except Exception:
                pass
        rom.unlink(missing_ok=True)
