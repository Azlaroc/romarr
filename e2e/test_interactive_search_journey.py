"""Wanted → interactive search journey (the relocated manual search).

The Requests screen used to be the only place you could pick a title, run a
search, and choose a release by hand — Radarr's interactive search living
inside a request queue RomArr does not have. It now lives on the row it is
about, so this journey drives it from a wishlist row.

State-neutral by construction: it adds one wishlist row, searches it, closes
the modal WITHOUT grabbing, and deletes the row. Nothing is imported and no
profile or definition is saved (browser tests run before the API-only
test_z* journeys, so a save here would poison them).
"""
from playwright.sync_api import expect

SLOW_MS = 15_000

# A title the archive.org stub serves for the "gb" item, so the search
# returns something rather than proving only that the modal opened.
TITLE = "Wario Land"


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_interactive_search_from_a_wishlist_row(ui):
    page = ui["page"]

    _nav(page, "wanted", "Wanted")

    # One door under Wanted, not two: the seerr-shaped request queue is gone.
    expect(page.get_by_test_id("nav-wanted-requests")).to_have_count(0)

    page.get_by_test_id("wish-title").fill(TITLE)
    page.get_by_test_id("wish-platform").select_option("gb")
    page.get_by_test_id("wish-add").click()

    wishlist = page.get_by_test_id("wishlist")
    expect(wishlist).to_contain_text(TITLE, timeout=SLOW_MS)

    row = page.locator('[data-testid="wishlist"] > div', has_text=TITLE).first
    row.locator('[data-testid^="wish-search-"]').click()

    # The modal searches on open, seeded with the row's title and platform.
    expect(page.get_by_test_id("interactive-search")).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("results")).to_contain_text(TITLE, timeout=SLOW_MS)
    # It says which policy ranked them — the reason this is not just a
    # prefilled search box.
    expect(page.get_by_test_id("interactive-search-info")).to_contain_text("profile", timeout=SLOW_MS)

    # Close without grabbing: nothing enters the library from this journey.
    page.get_by_test_id("interactive-search-close").click()
    expect(page.get_by_test_id("interactive-search")).to_have_count(0)

    row.locator('[data-testid="wish-delete"]').click()
    expect(wishlist).not_to_contain_text(TITLE, timeout=SLOW_MS)
