"""#254 acceptance clause, UI-driven (F6 PR-C): define a per-platform quality
profile in the UI, wishlist a title in the UI, and watch the enforce-mode
scheduler grab it under that profile — queue → imported → library.

Named test_zzzz_* so it runs after test_zzz_selector_journey.py: it relies on
the wishlist being empty again and deliberately uses a gb-scoped profile so it
never touches the global default row that test owns. Platform-exact resolution
means the new profile — created through the UI — is the one the selector runs
under for the gb grab.
"""
import json
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


def test_ui_profile_drives_enforce_grab(ui, app):
    page = ui["page"]
    base = app["base"]

    # 1. Define the per-platform profile through the UI (gb; usa > world; the
    #    stub zips are ~1KB so widen the size band like the selector journey
    #    does globally — but here per-platform, via the screen under test).
    _nav(page, "settings", "Settings")
    page.get_by_test_id("nav-settings-profiles").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_be_visible(timeout=SLOW_MS)
    page.get_by_test_id("qp-add").click()
    page.get_by_test_id("qp-name").fill("GB E2E")
    page.get_by_test_id("qp-platform").fill("gb")
    page.get_by_test_id("qp-region-add-usa").click()
    page.get_by_test_id("qp-region-add-world").click()
    page.get_by_test_id("qp-size-min").fill("1")
    page.get_by_test_id("qp-size-max").fill("10000000000")
    page.get_by_test_id("qp-save").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_contain_text("GB E2E", timeout=SLOW_MS)

    # 2. Wishlist the title through the UI (unused stub catalog entry).
    _nav(page, "wanted", "Wanted")
    page.get_by_test_id("wish-title").fill("Wario Land - Super Mario Land 3")
    page.get_by_test_id("wish-platform").select_option("gb")
    page.get_by_test_id("wish-add").click()
    expect(page.get_by_test_id("wishlist")).to_contain_text("Wario Land", timeout=SLOW_MS)

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
