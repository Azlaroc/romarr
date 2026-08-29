"""Acceptance clause, UI-driven: define a quality profile in the UI, wishlist
a title under it in the UI, and watch the enforce-mode scheduler grab it under
that profile — queue → imported → library.

Profiles v2 sharpened this: the profile is chosen PER TITLE at add time rather
than being inferred from the platform, so this now exercises the override path
end to end — the add dialog's choice, the wishlist row that carries it, and
the scheduler resolving it.

Ordering reality: pytest groups the [chromium]-parametrized browser tests into
one block that runs BEFORE the unparametrized API-only test_z* journeys, so
the zzzz prefix only sorts this last WITHIN the browser block — it still runs
before test_zzz_selector_journey.py. It must therefore leave no state behind:
a second enforce cycle removes its wishlist row via the owned check (which is
also the lifecycle half of the acceptance), and the gb-scoped profile is
deleted afterwards — a leftover platform-exact profile would shadow the global
default row that test_zzz tunes for its own Kirby (gb) grab.
"""
import json
import re
import time
import urllib.request

from playwright.sync_api import expect

SLOW_MS = 15_000
GRAB_MS = 60_000


def _req(base: str, path: str, method: str = "GET", payload: dict | None = None) -> dict:
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def _wait(cond, timeout=45, step=0.5, msg="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = cond()
        if result:
            return result
        time.sleep(step)
    raise AssertionError(f"timed out waiting for {msg}")


def _wishlist(base: str) -> list:
    wl = _req(base, "/api/wishlist")
    return wl if isinstance(wl, list) else wl.get("wishlist") or wl.get("items") or []


def test_ui_profile_drives_enforce_grab(ui, app):
    page = ui["page"]
    base = app["base"]

    # 1. Define the profile through the UI. It carries no platform — a
    #    profile is free-standing now — and the title picks it at add time,
    #    which is the whole point of profiles v2. (usa > world; the stub zips
    #    are ~1KB so the band is widened here rather than globally.)
    _nav(page, "settings", "Settings")
    page.get_by_test_id("nav-settings-profiles").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_be_visible(timeout=SLOW_MS)
    page.get_by_test_id("qp-add").click()
    page.get_by_test_id("qp-name").fill("GB E2E")
    page.get_by_test_id("qp-region-add-usa").click()
    page.get_by_test_id("qp-region-add-world").click()
    page.get_by_test_id("qp-save").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_contain_text("GB E2E", timeout=SLOW_MS)

    # 2. Wishlist the title through the UI, choosing that profile for this
    #    one title. The row says so, which is the per-title override arriving
    #    where the scheduler will read it.
    _nav(page, "wanted", "Wanted")
    page.get_by_test_id("wish-title").fill("Wario Land - Super Mario Land 3")
    page.get_by_test_id("wish-platform").select_option("gb")
    page.get_by_test_id("wish-profile").select_option(label="GB E2E")
    page.get_by_test_id("wish-add").click()
    expect(page.get_by_test_id("wishlist")).to_contain_text("Wario Land", timeout=SLOW_MS)
    expect(page.get_by_test_id("wishlist")).to_contain_text("GB E2E", timeout=SLOW_MS)
    expect(page.get_by_test_id("wishlist")).to_contain_text("chosen for this title", timeout=SLOW_MS)

    # 3. Trigger a scheduler cycle (the Tasks screen button ships in PR-G; the
    #    trigger is not the surface under test here).
    _req(base, "/api/scheduler/run", "POST", {})

    # 4. Watch it move through the queue…
    _nav(page, "activity", "Activity")
    expect(page.get_by_test_id("downloads")).to_contain_text("Wario", timeout=GRAB_MS)

    # …into the library.
    _nav(page, "library", "Library")
    expect(page.get_by_test_id("library-grid")).to_contain_text("Wario Land", timeout=GRAB_MS)

    # 5. The selector really ran for it (enforce mode logs a decision event; a
    #    legacy-path grab would not).
    def _decision():
        entries = _req(base, "/api/activity").get("entries", [])
        return next(
            (e for e in entries
             if e.get("event_type") == "selector_decision" and "Wario" in (e.get("title") or "")),
            None,
        )
    assert _wait(_decision, msg="a selector_decision activity entry for Wario")

    # 5b. Lifecycle (#280): the import consumed the wishlist row — the grab's
    #     scheduler_download job is joined back to the wishlist title at
    #     import time, so the row is gone without waiting for a later cycle's
    #     owned check (which could never match a release-derived library
    #     title like "CTR - Crash Team Racing" anyway). This supersedes the
    #     PR-E chip-presence assert here: a consumed row leaves no chip.
    _wait(
        lambda: (not any("Wario" in (w.get("title") or "") for w in _wishlist(base))) or None,
        msg="import consuming the Wario wishlist row",
    )

    def _fulfilled():
        entries = _req(base, "/api/activity").get("entries", [])
        return next(
            (e for e in entries
             if e.get("event_type") == "wishlist_fulfilled" and "Wario" in (e.get("title") or "")),
            None,
        )
    assert _wait(_fulfilled, msg="a wishlist_fulfilled activity entry for Wario")

    # 6. State-neutral exit: the wishlist UI no longer lists Wario — leaving
    #    the wishlist empty for the API-only journeys that run after this block.
    _nav(page, "wanted", "Wanted")
    expect(page.get_by_test_id("wishlist")).not_to_contain_text("Wario Land", timeout=SLOW_MS)

    # Delete the profile. It is no longer platform-scoped, so it cannot
    # shadow anything — but leaving it would still add a row to the list the
    # other journeys read.
    gb = next(p for p in _req(base, "/api/quality-profiles").get("profiles", [])
              if p.get("name") == "GB E2E")
    _req(base, f"/api/quality-profiles/{gb['id']}", "DELETE")
