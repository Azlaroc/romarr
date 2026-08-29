"""Degraded disc-set repair journey — the incomplete-set repair arc end to end
through the REAL binary, on its own restartable instance:

  1. disc 1 of a declared 2-disc set imports; disc 2 never arrives;
  2. a restart's boot sweep degrade-finalizes the set (pending == 0) and
     persists the durable $.gamarr.set marker on the library row — the
     restart doubles as the marker-durability check;
  3. a scheduler cycle repairs it: re-searches, grabs ONLY disc 2 into the
     existing set dir, reopens the barrier, and the landing disc
     re-finalizes the set complete (marker flips, repaired_at stamped);
  4. a later cycle finds nothing to repair and grabs nothing.

Named test_zzzzzz_* so it runs last. Boots its own instance (own data dir and
port, same stub) because triggering the boot sweep requires a process restart,
which the session app fixture must never suffer.

Pure API/filesystem test. rom-converto is absent in the harness, so no .m3u
assertions (the live deploy-verify covers those).
"""
import json
import time
import urllib.parse
import urllib.request

from conftest import IA_PSX_ITEM, _app_env, _boot_gamarr, _free_port, _stop_gamarr

SET_ID = "set-e2e-repair"
SET_DIR = "Chrono Cross (USA)"
DISC1 = "Chrono Cross (USA) (Disc 1).zip"
DISC2 = "Chrono Cross (USA) (Disc 2).zip"
SLOW_S = 45


def _req(base: str, path: str, method: str = "GET", payload: dict | None = None) -> dict:
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def _jobs(base: str) -> list:
    downloads = _req(base, "/api/downloads")
    return downloads if isinstance(downloads, list) else downloads.get("downloads", [])


def _set_row(base: str):
    lib = _req(base, "/api/library?platform=psx&page_size=100")
    items = lib if isinstance(lib, list) else lib.get("items") or lib.get("library") or []
    return next((it for it in items if it.get("source_id") == f"set:{SET_ID}"), None)


def _marker(row) -> dict:
    try:
        return json.loads(row.get("metadata") or "{}").get("gamarr", {}).get("set", {})
    except ValueError:
        return {}


def _wait(cond, timeout=SLOW_S, step=0.5, msg="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = cond()
        if result:
            return result
        time.sleep(step)
    raise AssertionError(f"timed out waiting for {msg}")


def test_degraded_set_repair_journey(stub_server, gamarr_binary, tmp_path_factory):
    data = tmp_path_factory.mktemp("repair-data")
    port = _free_port()
    env = _app_env(stub_server, data, port)
    base = f"http://127.0.0.1:{port}"
    proc, log = _boot_gamarr(gamarr_binary, env, data)
    try:
        # Disc 1 of a declared 2-disc set; disc 2 is never submitted.
        url = f"{stub_server}/download/{IA_PSX_ITEM}/{urllib.parse.quote(DISC1)}"
        resp = _req(base, "/api/download", "POST", {
            "download_url": url,
            "title": DISC1,
            "platform": "PlayStation",
            "platform_slug": "psx",
            "source_type": "ddl",
            "disc_set_id": SET_ID,
            "disc_index": 1,
            "disc_total": 2,
            "set_dir": SET_DIR,
        })
        assert resp.get("success"), f"disc 1 rejected: {resp}"
        _wait(
            lambda: any(j.get("status") == "completed" and DISC1 in (j.get("title") or "")
                        for j in _jobs(base)) or None,
            msg="disc 1 import")
        assert _set_row(base) is None, "set finalized with a disc still missing"
    finally:
        _stop_gamarr(proc, log)

    # Reboot: the boot janitor sweeps, sees pending == 0 with 1 of 2 imported,
    # and degrade-finalizes. The marker must survive the restart on the row.
    proc, log = _boot_gamarr(gamarr_binary, env, data)
    try:
        row = _wait(
            lambda: (lambda r: r if r and _marker(r).get("degraded") else None)(_set_row(base)),
            msg="the degraded set row + marker after the boot sweep")
        mk = _marker(row)
        assert mk.get("total") == 2 and mk.get("have") == [1], f"marker = {mk}"

        # Repair cycle: re-grab ONLY disc 2 into the existing set dir.
        _req(base, "/api/scheduler/run", "POST", {})
        _wait(
            lambda: any(DISC2 in (j.get("title") or "") for j in _jobs(base)) or None,
            msg="the disc 2 repair grab")
        assert not any(
            DISC1 in (j.get("title") or "") and j.get("status") != "completed"
            for j in _jobs(base)), "disc 1 re-grabbed although it already imported"

        row = _wait(
            lambda: (lambda r: r if r and not _marker(r).get("degraded") else None)(_set_row(base)),
            msg="the repaired marker flip")
        mk = _marker(row)
        assert mk.get("have") == [1, 2], f"marker = {mk}"
        assert mk.get("repaired_at"), "repaired_at not stamped"
        assert mk.get("repair_attempts") == 1, f"marker = {mk}"

        # Both payloads inside the ONE set dir; no stray per-disc dirs.
        set_dir = data / "roms" / "psx" / SET_DIR
        assert set_dir.is_dir(), "shared set dir missing"
        entries = [p for p in set_dir.iterdir() if p.name != ".gamarr.json"]
        assert len(entries) >= 2, f"set dir has {len(entries)} entries, want >= 2"
        strays = [p for p in (data / "roms" / "psx").iterdir()
                  if p.is_dir() and p.name != SET_DIR and "Chrono Cross" in p.name]
        assert not strays, f"per-disc dirs leaked: {strays}"

        # One library row; member jobs (survivor + repair grab) completed.
        member_jobs = [j for j in _jobs(base) if "Chrono Cross" in (j.get("title") or "")]
        assert all(j.get("status") == "completed" for j in member_jobs), member_jobs

        # A later cycle has nothing to repair and grabs nothing.
        job_count = len(_jobs(base))
        _req(base, "/api/scheduler/run", "POST", {})
        time.sleep(3)
        assert len(_jobs(base)) == job_count, "repaired set re-grabbed on a later cycle"
        assert _marker(_set_row(base)).get("repair_attempts") == 1, \
            "attempts bumped although nothing was missing"
    finally:
        _stop_gamarr(proc, log)
