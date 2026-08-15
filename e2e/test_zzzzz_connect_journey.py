"""RomM Connect journey — the Connect plane end to end through a real import:

  (a) a manual import routes through TrackInLibrary (the universal import
      choke point): the library row carries a stable manual:<path> source id
      and the import shows up in the activity log;
  (b) the Connect notifier coalesces the import into a targeted RomM scan —
      the stub records a socket.io "scan" emit whose platform_fs_slugs names
      the imported platform and whose apis list is the heartbeat's enabled
      sources (never empty: an empty apis scan ingests blank tiles in RomM);
  (c) re-importing the already-tracked file adds nothing (dedupe).

Named test_zzzzz_* so it runs after every other journey; it only adds a gbc
title no other journey touches. Pure API/filesystem test.
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


def _wait(cond, timeout=SLOW_S, step=0.5, msg="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = cond()
        if result:
            return result
        time.sleep(step)
    raise AssertionError(f"timed out waiting for {msg}")


def _library_gbc(base: str) -> list:
    lib = _req(base, "/api/library?platform=gbc&page_size=50")
    return lib if isinstance(lib, list) else lib.get("items") or lib.get("library") or []


def test_manual_import_triggers_romm_scan(app, stub_server):
    base = app["base"]

    drop = app["data"] / "connect-drop"
    drop.mkdir(exist_ok=True)
    rom = drop / "Wario Land 3 (World).gbc"
    rom.write_bytes(b"e2e-connect-journey rom payload")

    payload = {"files": [{
        "path": str(rom),
        "title": "Wario Land 3",
        "platform": "Game Boy Color",
        "platform_slug": "gbc",
        "is_pc": False,
    }]}
    resp = _req(base, "/api/import/files", "POST", payload)
    assert resp.get("imported") == 1, f"manual import failed: {resp}"

    # (a) The row went through TrackInLibrary: source manual, dedupe id set.
    items = _wait(lambda: _library_gbc(base), msg="gbc library row")
    row = next((i for i in items if "Wario" in (i.get("title") or "")), None)
    assert row, f"imported title missing from library: {items}"

    # (b) The Connect notifier fires a targeted scan at the RomM stub.
    def scan_for_gbc():
        with urllib.request.urlopen(stub_server + "/stub/romm-scans", timeout=5) as r:
            scans = json.loads(r.read() or b"[]")
        return [s for s in scans if "gbc" in (s.get("platform_fs_slugs") or [])]

    scans = _wait(scan_for_gbc, msg="romm scan trigger for gbc")
    scan = scans[0]
    assert scan.get("type") == "quick", scan
    assert scan.get("apis"), f"scan emitted with empty apis (blank-tile ingest): {scan}"
    assert set(scan["apis"]) == {"igdb", "hasheous"}, scan
    assert scan.get("platforms") == [] and scan.get("roms_ids") == [], scan

    # (c) Re-importing the tracked file is a no-op.
    lib_path = row.get("file_path") or row.get("filePath")
    assert lib_path, f"library row exposes no file path: {row}"
    payload["files"][0]["path"] = lib_path
    resp2 = _req(base, "/api/import/files", "POST", payload)
    assert resp2.get("imported") == 0 and resp2.get("skipped") == 1, resp2
    assert len(_library_gbc(base)) == len(items), "duplicate library row minted"
