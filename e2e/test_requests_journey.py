"""Wanted → Requests journey (F6 PR-E).

Create a request through the UI, watch it render pending, delete it through
the UI. Self-contained and state-neutral: it wishlists nothing, imports
nothing, and removes the one request it creates (leaving only harmless
request_created activity rows). Runs inside the [chromium] browser block
(before the API-only test_z* journeys — pytest parametrized-fixture grouping).
"""
from playwright.sync_api import expect

SLOW_MS = 15_000


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_request_create_and_delete(ui):
    page = ui["page"]

    # /wanted redirects to the wishlist child; the Requests child is a click away.
    _nav(page, "wanted", "Wanted")
    page.get_by_test_id("nav-wanted-requests").click()
    # Container stays mounted when empty.
    expect(page.get_by_test_id("requests-list")).to_be_attached(timeout=SLOW_MS)

    # Create through the modal.
    page.get_by_test_id("req-add").click()
    page.get_by_test_id("req-title").fill("E2E Request Journey")
    page.get_by_test_id("req-platform").select_option("gb")
    page.get_by_test_id("req-notes").fill("seeded by test")
    page.get_by_test_id("req-create").click()

    rlist = page.get_by_test_id("requests-list")
    expect(rlist).to_contain_text("E2E Request Journey", timeout=SLOW_MS)
    expect(rlist).to_contain_text("pending", timeout=SLOW_MS)
    expect(rlist).to_contain_text("seeded by test", timeout=SLOW_MS)

    # Delete through the row control + shared ConfirmDialog.
    row = page.locator('[data-testid="requests-list"] > div', has_text="E2E Request Journey").first
    row.locator('[data-testid^="req-delete-"]').click()
    page.get_by_test_id("confirm-ok").click()
    expect(rlist).not_to_contain_text("E2E Request Journey", timeout=SLOW_MS)
