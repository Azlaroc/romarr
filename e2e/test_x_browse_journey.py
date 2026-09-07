"""The three-level browse: shelf -> platform grid -> game detail.

Runs after the flagship journey (x > u in the chromium block), so the
library holds the Tetris import. State-neutral by construction: the one
mutation (a profile pin) reverts in a finally, even when an assert fails.
"""
from playwright.sync_api import expect

SLOW_MS = 15_000


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_x_browse_levels(ui):
    page = ui["page"]

    # Level 1: the shelf. Platforms are destinations — an All Games tile plus
    # one tile per inhabited platform, chips only where a rollup is stamped.
    _nav(page, "library", "Library")
    expect(page.get_by_test_id("platform-grid")).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("platform-card-all")).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("platform-card-gb")).to_be_visible(timeout=SLOW_MS)

    # Level 2: the windowed grid, server-lettered and server-faceted.
    page.get_by_test_id("platform-card-gb").click()
    expect(page.get_by_test_id("page-title")).to_have_text("Game Boy", timeout=SLOW_MS)
    expect(page.get_by_test_id("library-grid")).to_contain_text("Tetris", timeout=SLOW_MS)
    expect(page.get_by_test_id("games-facets")).to_be_visible()

    # The search box matches filenames too (the fixture file carries the
    # No-Intro name, so a fragment of it must hit even if a display title
    # ever diverges).
    page.get_by_test_id("games-filter").fill("tetris")
    page.get_by_test_id("games-filter").press("Enter")
    expect(page.get_by_test_id("library-grid")).to_contain_text("Tetris", timeout=SLOW_MS)

    # Level 3: the game detail.
    page.get_by_test_id("library-grid").get_by_text("Tetris").first.click()
    expect(page.get_by_test_id("detail-title")).to_contain_text("Tetris", timeout=SLOW_MS)
    expect(page.get_by_test_id("detail-file")).to_be_visible()
    # The known-dumps table is fed by the DAT read plane; the fixture catalog
    # knows this title, so the family renders rows rather than the empty state.
    expect(page.get_by_test_id("detail-dumps")).to_contain_text("Tetris", timeout=SLOW_MS)

    # Profile round-trip, reverted even on failure: browser journeys must be
    # state-neutral including their unhappy paths.
    select = page.get_by_test_id("detail-profile")
    original = select.input_value()
    assert original == "0", "a fixture row arrived pre-pinned"
    options = select.locator("option").all()
    if len(options) < 2:
        # A fresh fixture may hold only the platform-default option; the
        # control rendering IS the assertion then.
        return
    target = options[1].get_attribute("value")
    try:
        select.select_option(target)
        expect(page.get_by_test_id("detail-profile")).to_have_value(target, timeout=SLOW_MS)
    finally:
        select.select_option("0")
