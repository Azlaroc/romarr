"""Bulk-rename smoke (runs first in the browser block — 'r' sorts before
's'ystem/'u'ser journeys — so the library is still empty).

rom-converto is absent in the e2e harness, so only the empty-scope flow is
exercised: screen renders, a preview over the empty library finishes with
zero rows, and Apply stays disabled. State-neutral: no downloads, no users.
"""
from playwright.sync_api import expect

SLOW_MS = 15_000


def test_rename_screen_empty_preview(ui):
    page = ui["page"]
    # Reached from the Library header, not the nav.
    page.get_by_test_id("library-rename-link").click()
    expect(page.get_by_test_id("page-title")).to_have_text("Rename", timeout=SLOW_MS)
    expect(page.get_by_test_id("rename-controls")).to_be_visible(timeout=SLOW_MS)

    apply_btn = page.get_by_test_id("rename-apply-btn")
    expect(apply_btn).to_be_disabled()

    page.get_by_test_id("rename-preview-btn").click()
    # Empty library → the run finishes immediately with nothing scanned.
    expect(page.get_by_test_id("rename-status")).to_contain_text("0/0 scanned", timeout=SLOW_MS)
    expect(apply_btn).to_be_disabled()
