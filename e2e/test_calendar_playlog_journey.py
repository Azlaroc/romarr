"""Calendar, Play Log, and notifications-bell journeys (F6 PR-F).

Runs first in the [chromium] browser block (pytest groups the browser-fixture
tests ahead of the API-only test_z* journeys, and this file sorts first within
the block). Fully self-contained and state-neutral: the play-log entry it
creates it also deletes; it never touches the wishlist, library, or profiles.
"""
import re

from playwright.sync_api import expect

SLOW_MS = 15_000


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_calendar_shows_honest_no_rawg_state(ui):
    page = ui["page"]
    _nav(page, "calendar", "Calendar")
    # The CI harness has no RAWG key — the backend silently serves empty lists,
    # and the screen must say so instead of rendering a blank page.
    expect(page.get_by_test_id("calendar-no-rawg")).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("calendar-no-rawg")).to_contain_text(
        re.compile("RAWG", re.I), timeout=SLOW_MS)


def test_playlog_add_and_delete(ui):
    page = ui["page"]
    _nav(page, "play-log", "Play Log")

    # Container stays mounted when empty.
    plist = page.get_by_test_id("playlog-list")
    expect(plist).to_be_attached(timeout=SLOW_MS)

    page.get_by_test_id("play-title").fill("E2E Zelda Session")
    page.get_by_test_id("play-platform").select_option("gb")
    page.get_by_test_id("play-rating").fill("8")
    page.get_by_test_id("play-add").click()
    expect(plist).to_contain_text("E2E Zelda Session", timeout=SLOW_MS)

    # State-neutral: delete the row and watch the mounted list shrink.
    row = page.locator('[data-testid="playlog-list"] > div', has_text="E2E Zelda Session").first
    row.locator('[data-testid^="play-delete-"]').click()
    expect(plist).not_to_contain_text("E2E Zelda Session", timeout=SLOW_MS)


def test_bell_panel_opens_reads_and_closes(ui):
    page = ui["page"]
    page.get_by_test_id("notifications-bell").click()

    panel = page.get_by_test_id("bell-panel")
    expect(panel).to_be_visible(timeout=SLOW_MS)
    expect(panel).to_contain_text(re.compile("no notifications", re.I), timeout=SLOW_MS)

    # Mark-all must be clickable without error even with nothing unread.
    page.get_by_test_id("bell-read-all").click()
    expect(panel).to_be_visible()

    page.keyboard.press("Escape")
    expect(panel).not_to_be_attached(timeout=SLOW_MS)
