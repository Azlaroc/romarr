"""Selector-driven auto-grab journey (F4 PR-5) — the two #235 acceptance
criteria, end to end through the REAL scheduler in enforce mode:

  (a) a wishlisted multi-disc title (Chrono Cross, psx) is grabbed as a
      complete disc set that converges into one game dir + one library row;
  (b) a wishlisted cart (Kirby's Dream Land, gb) grabs exactly the 1G1R
      pick — World region, highest revision — out of three candidate dumps;
  (c) wishlist lifecycle: each grab's import consumes its wishlist row at
      import time (#280 — the scheduler_download job is joined back to the
      wishlist title), and a later cycle re-grabs nothing.

Named test_zzz_* so it runs after every other journey: it consumes the
library/state those tests build (FF7 is already imported — which is exactly
why this test wishlists different titles).

Pure API/filesystem test. rom-converto is absent in the harness, so no .m3u
assertions (the live deploy-verify covers those).
"""
import json
import time
import urllib.request

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


def _library(base: str, platform: str) -> list:
    lib = _req(base, f"/api/library?platform={platform}&page_size=200")
    return lib if isinstance(lib, list) else lib.get("items") or lib.get("library") or []


def _wishlist(base: str) -> list:
    wl = _req(base, "/api/wishlist")
    return wl if isinstance(wl, list) else wl.get("wishlist") or wl.get("items") or []


def _wait(cond, timeout=SLOW_S, step=0.5, msg="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = cond()
        if result:
            return result
        time.sleep(step)
    raise AssertionError(f"timed out waiting for {msg}")


def test_selector_enforce_grabs_1g1r_and_disc_sets(app, stub_server):
    base = app["base"]

    assert _req(base, "/api/wishlist", "POST", {
        "title": "Chrono Cross", "platform": "PlayStation", "platform_slug": "psx",
    }).get("success", True)
    assert _req(base, "/api/wishlist", "POST", {
        "title": "Kirby's Dream Land", "platform": "Game Boy", "platform_slug": "gb",
    }).get("success", True)

    _req(base, "/api/scheduler/run", "POST", {})

    # (b) 1G1R: exactly one Kirby job, and it is the World Rev 1 dump.
    kirby_jobs = _wait(
        lambda: [j for j in _jobs(base) if "Kirby" in (j.get("title") or "")] or None,
        msg="a Kirby grab")
    assert len(kirby_jobs) == 1, f"expected exactly 1 Kirby grab, got {kirby_jobs}"
    assert "(World) (Rev 1)" in kirby_jobs[0]["title"], \
        f"1G1R picked wrong dump: {kirby_jobs[0]['title']}"

    # (a) Disc set: both Chrono Cross discs grabbed, one library row, one dir.
    chrono_jobs = _wait(
        lambda: (lambda js: js if len(js) == 2 else None)(
            [j for j in _jobs(base) if "Chrono Cross" in (j.get("title") or "")]),
        msg="both Chrono Cross disc grabs")
    set_row = _wait(
        lambda: next((it for it in _library(base, "psx")
                      if (it.get("source_id") or "").startswith("set:")
                      and it.get("title") == "Chrono Cross (USA)"), None),
        msg="the Chrono Cross set library row")
    set_dir = app["roms_dir"] / "psx" / "Chrono Cross (USA)"
    assert set_dir.is_dir(), "shared Chrono Cross set dir missing"
    strays = [p for p in (app["roms_dir"] / "psx").iterdir()
              if p.is_dir() and "Chrono Cross" in p.name and p.name != "Chrono Cross (USA)"]
    assert not strays, f"per-disc dirs leaked: {strays}"

    # Kirby import lands as a normal single grab.
    _wait(lambda: next((it for it in _library(base, "gb")
                        if "Kirby" in (it.get("title") or "")), None),
          msg="the Kirby library row")

    # (c) Lifecycle (#280): each import consumed its wishlist row at import
    # time — the release-derived library title may never match the wishlist
    # title, so a later cycle's owned check can't be trusted to clean up.
    _wait(lambda: len(_wishlist(base)) == 0 or None, msg="wishlist consumed at import")
    job_count = len(_jobs(base))

    # A later cycle finds nothing to do and re-grabs nothing.
    _req(base, "/api/scheduler/run", "POST", {})
    time.sleep(2)
    assert len(_jobs(base)) == job_count, "second cycle must not re-grab imported titles"
