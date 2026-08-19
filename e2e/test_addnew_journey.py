"""Add New: the two doors (#317's acceptance clause, UI-driven).

Discover asks the metadata authority what games exist and shows cover art;
Browse reads the DAT catalog for one platform. Both end at the same add
dialog, which reports what the catalog knows BEFORE a wishlist row is created
— "no known dumps" said up front is the difference between a wishlist that
fills and one that quietly never does.

State-neutral: the one wishlist row this creates is deleted through the API at
the end, and nothing is imported.
"""
import json
import urllib.request

from playwright.sync_api import expect

SLOW_MS = 15_000
TITLE = "Wario Land - Super Mario Land 3"


def _req(base: str, path: str, method: str = "GET", payload=None):
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(base + path, data=data, method=method,
                                 headers={"Content-Type": "application/json"})
    with urllib.request.urlopen(req, timeout=15) as r:
        raw = r.read()
        return json.loads(raw) if raw else {}


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_add_new_two_doors(ui, app):
    page, base = ui["page"], app["base"]

    _nav(page, "add-new", "Add New")

    # ── Door 2 first: it needs no provider, and the catalog is empty this
    # early in the run, so it proves the honest empty state rather than a
    # populated table (the API journey covers the populated case).
    page.get_by_test_id("door-browse").click()
    page.get_by_test_id("browse-platform").select_option("gb")
    expect(page.get_by_test_id("browse-count")).to_be_visible(timeout=SLOW_MS)

    # ── Door 1: art-forward search against the stubbed metadata authority.
    page.get_by_test_id("door-discover").click()
    page.get_by_test_id("discover-input").fill("wario land")
    page.get_by_test_id("discover-submit").click()

    results = page.get_by_test_id("discover-results")
    expect(results).to_contain_text("Wario Land", timeout=SLOW_MS)
    # The registry resolved IGDB's platform identity to our slug.
    expect(results).to_contain_text("gb", timeout=SLOW_MS)

    page.get_by_test_id("discover-game-1074").click()
    dialog = page.get_by_test_id("add-dialog")
    expect(dialog).to_be_visible(timeout=SLOW_MS)
    # Only the platforms we have a lane for are offered; the unmapped one
    # from the fixture must not become a button.
    expect(page.get_by_test_id("add-platform-gb")).to_be_visible()
    expect(page.get_by_test_id("add-platform-nolane")).to_have_count(0)

    page.get_by_test_id("add-platform-gb").click()
    # What the catalog knows is stated before committing.
    expect(page.get_by_test_id("add-dumps")).to_be_visible(timeout=SLOW_MS)
    page.get_by_test_id("add-confirm").click()
    expect(dialog).to_have_count(0, timeout=SLOW_MS)

    # The row exists, carrying the title the authority named.
    wl = _req(base, "/api/wishlist")
    rows = wl if isinstance(wl, list) else wl.get("wishlist") or wl.get("items") or []
    match = [w for w in rows if TITLE in (w.get("title") or "")]
    assert match, f"Add New did not create a wishlist row: {rows}"
    assert match[0].get("platform_slug") == "gb"

    # Cleanup: leave the wishlist exactly as it was found.
    _req(base, f"/api/wishlist/{match[0]['id']}", "DELETE")
    wl = _req(base, "/api/wishlist")
    rows = wl if isinstance(wl, list) else wl.get("wishlist") or wl.get("items") or []
    assert not [w for w in rows if TITLE in (w.get("title") or "")]
