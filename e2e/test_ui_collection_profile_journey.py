"""Collection profiles through the UI: define an arc, point a platform at it,
and watch the set view answer under it.

State-neutral in a finally block even when an assertion fails: the platform is
re-pointed at the default and the created profile deleted via the API, because
a leftover strict profile would silently reshape every later journey's set —
which reads as a policy bug, not a leftover.
"""
import json
import urllib.error
import urllib.request

from playwright.sync_api import expect

SLOW_MS = 15_000


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


def test_collection_profile_ui_journey(ui, app):
    page = ui["page"]
    base = app["base"]
    created_name = "E2E Strict Retail"

    try:
        # 1. The shipped rows are visible where profiles live, defaults badged.
        _nav(page, "settings", "Settings")
        page.get_by_test_id("nav-settings-profiles").click()
        expect(page.get_by_test_id("profiles-collection-list")).to_be_visible(timeout=SLOW_MS)
        expect(page.get_by_test_id("profiles-collection-list")).to_contain_text("Standard")

        # 2. Define an arc through the editor: verified-only, English required,
        #    usa-first. The knobs are Retool's — anyone from the 1G1R scene
        #    reads this screen on sight.
        page.get_by_test_id("cprof-add").click()
        page.get_by_test_id("cprof-name").fill(created_name)
        page.get_by_test_id("cprof-region-add-usa").click()
        page.get_by_test_id("cprof-verified-only").click()
        page.get_by_test_id("cprof-keep-orphans").click()  # default on -> off
        page.get_by_test_id("cprof-save").click()
        expect(page.get_by_test_id("profiles-collection-list")).to_contain_text(created_name, timeout=SLOW_MS)

        # 3. Point gb at it on the Platforms page, through the SaveBar.
        _nav(page, "platforms", "Platforms")
        page.get_by_test_id("plat-cprof-gb").select_option(label=created_name)
        expect(page.get_by_test_id("save-bar")).to_be_visible(timeout=SLOW_MS)
        page.get_by_test_id("save-bar-save").click()
        expect(page.get_by_test_id("save-bar")).to_have_count(0, timeout=SLOW_MS)

        # 4. The set view answers under the new arc, and says so by name.
        page.get_by_test_id("plat-set-gb").click()
        expect(page.get_by_test_id("set-policy")).to_contain_text(created_name, timeout=SLOW_MS)
        expect(page.get_by_test_id("set-policy")).to_contain_text("English required")
        expect(page.get_by_test_id("set-quadrants")).to_be_visible()

        # Every quadrant is a chip; filtering to one shows only its rows. The
        # gb fixture catalog is tiny, so assert mechanics, not counts.
        page.get_by_test_id("set-chip-out").click()
        expect(page.get_by_test_id("set-entries")).to_be_visible(timeout=SLOW_MS)

        # 5. A profile a platform follows refuses deletion — the delete can
        #    never silently change what a platform collects.
        profiles = _req(base, "/api/collection-profiles")["profiles"]
        mine = next(p for p in profiles if p["name"] == created_name)
        try:
            _req(base, f"/api/collection-profiles/{mine['id']}", "DELETE")
            raise AssertionError("deleting a followed profile must be refused")
        except urllib.error.HTTPError as e:
            assert e.code == 409, e.code
    finally:
        # Re-point gb at the default and remove the profile, whatever happened
        # above. Failures here surface loudly — a broken cleanup IS a failure.
        _req(base, "/api/platforms/gb", "PUT", {"collection_profile_id": 0})
        for p in _req(base, "/api/collection-profiles")["profiles"]:
            if p["name"] == created_name:
                _req(base, f"/api/collection-profiles/{p['id']}", "DELETE")
