"""Disc-set fulfillment journey (F4 PR-4): three DDL downloads stamped with
disc-set fields converge into ONE game directory and finalize together — one
library row keyed set:<id>, every member job completed + set_finalized.

Named test_zz_* so it runs after the other journeys: it adds a psx library row
and activity entries that earlier tests' assertions predate.

Pure API/filesystem test: no browser needed. rom-converto is absent in the
harness, so normalize/convert no-op by contract — no .m3u assertions here
(the live deploy-verify covers those).
"""
import json
import time
import urllib.parse
import urllib.request

from conftest import IA_PSX_ITEM, PSX_FILES

SET_ID = "set-e2e-ff7"
SET_DIR = "Final Fantasy VII (USA)"
DISCS = PSX_FILES[:3]


def _post_json(base: str, path: str, payload: dict) -> dict:
    req = urllib.request.Request(
        base + path,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def _get_json(base: str, path: str) -> dict:
    with urllib.request.urlopen(base + path, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def test_disc_set_converges_to_one_game_dir_and_one_library_row(app, stub_server):
    base = app["base"]

    for i, name in enumerate(DISCS, start=1):
        url = f"{stub_server}/download/{IA_PSX_ITEM}/{urllib.parse.quote(name)}"
        resp = _post_json(base, "/api/download", {
            "download_url": url,
            "title": name,
            "platform": "PlayStation",
            "platform_slug": "psx",
            "source_type": "ddl",
            "disc_set_id": SET_ID,
            "disc_index": i,
            "disc_total": len(DISCS),
            "set_dir": SET_DIR,
        })
        assert resp.get("success"), f"disc {i} rejected: {resp}"

    # All three imports run async; the set finalizes when the last lands.
    set_dir = app["roms_dir"] / "psx" / SET_DIR
    library_row = None
    for _ in range(60):
        lib = _get_json(base, "/api/library?platform=psx&page_size=100")
        items = lib if isinstance(lib, list) else lib.get("items") or lib.get("library") or []
        library_row = next(
            (it for it in items if it.get("source_id") == f"set:{SET_ID}"), None)
        if library_row:
            break
        time.sleep(0.5)
    assert library_row, "no library row for the completed disc set"

    # One row, named for the set dir, pointing at the shared game directory.
    lib = _get_json(base, "/api/library?platform=psx&page_size=100")
    items = lib if isinstance(lib, list) else lib.get("items") or lib.get("library") or []
    set_rows = [it for it in items if it.get("source_id") == f"set:{SET_ID}"]
    assert len(set_rows) == 1, f"expected 1 set row, got {len(set_rows)}"
    assert set_rows[0]["title"] == SET_DIR
    assert set_rows[0]["file_path"] == str(set_dir)

    # The shared dir holds all three payloads (extraction settings decide the
    # exact file names, so count entries rather than pin names).
    assert set_dir.is_dir(), "shared set dir missing"
    entries = [p for p in set_dir.iterdir() if p.name != ".gamarr.json"]
    assert len(entries) >= 3, f"set dir has {len(entries)} entries, want >= 3"

    # No stray per-disc dirs next to the set dir.
    strays = [p for p in (app["roms_dir"] / "psx").iterdir()
              if p.is_dir() and p.name != SET_DIR and "Final Fantasy" in p.name]
    assert not strays, f"per-disc dirs leaked outside the set dir: {strays}"

    # Every member job completed, done, and finalized.
    downloads = _get_json(base, "/api/downloads")
    jobs = downloads if isinstance(downloads, list) else downloads.get("downloads", [])
    member_jobs = [j for j in jobs if "Final Fantasy VII" in (j.get("title") or "")]
    assert len(member_jobs) == 3, f"expected 3 member jobs, got {len(member_jobs)}"
    for j in member_jobs:
        assert j.get("status") == "completed", f"member not completed: {j}"
