"""Platforms page: the vocabulary a title is added against, and how RomArr
treats each system.

Sorts before the settings journeys in the browser block, and is strictly
state-neutral: it edits and CANCELS, never saves. A saved acquisition toggle
would silently stop a later journey's platform from being searched, which
reads as a pipeline bug rather than as a leftover.
"""
import re

from playwright.sync_api import expect

SLOW_MS = 15_000


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_platforms_page(ui):
    page = ui["page"]
    _nav(page, "platforms", "Platforms")
    expect(page.get_by_test_id("plat-table")).to_be_visible(timeout=SLOW_MS)

    # The whole shipped vocabulary is here, not only platforms already in the
    # library — that is the difference between being able to add a game for a
    # system you have never acquired for and not.
    for slug in ("atari2600", "gbc", "psx", "switch"):
        expect(page.get_by_test_id(f"row-{slug}")).to_be_visible()

    # Display names, not "Unknown", and not the raw slug.
    expect(page.get_by_test_id("row-atari2600")).to_contain_text("Atari 2600")
    expect(page.get_by_test_id("row-psx")).to_contain_text("PS1")

    # Catalog assignment rides along, so this page and the coverage table can
    # never disagree about which authority owns a platform.
    expect(page.get_by_test_id("plat-catalog-psx")).to_contain_text("redump")
    expect(page.get_by_test_id("plat-catalog-nes")).to_contain_text("no-intro")
    # A platform with no lane says so rather than showing a blank cell.
    expect(page.get_by_test_id("plat-catalog-switch")).to_contain_text(
        re.compile("no catalog", re.I))

    # Directories that are not platforms are not manageable systems.
    expect(page.get_by_test_id("row-supporting_files")).to_have_count(0)

    # Editing raises the save bar and names what is pending.
    expect(page.get_by_test_id("save-bar")).to_have_count(0)
    page.get_by_test_id("plat-acq-atari2600").click()
    expect(page.get_by_test_id("save-bar")).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("save-bar-summary")).to_contain_text("1 platform")

    # Cancel restores the row and clears the bar — nothing is written.
    page.get_by_test_id("save-bar-cancel").click()
    expect(page.get_by_test_id("save-bar")).to_have_count(0, timeout=SLOW_MS)
    expect(page.get_by_test_id("plat-acq-atari2600")).to_be_checked()
