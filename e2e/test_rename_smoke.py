"""Bulk-rename smoke (runs first in the browser block — 'r' sorts before
's'ystem/'u'ser journeys).

The harness pre-seeds a handful of library rows, and rom-converto is absent
in the e2e image — so every entry classifies as a skip: the preview must
finish with nothing to rename and Apply stays disabled. State-neutral: no
files are renamed (there is nothing plannable), no downloads, no users.
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
    # rom-converto is absent → every seeded row skips; nothing is plannable.
    status = page.get_by_test_id("rename-status")
    expect(status).to_contain_text("finished", timeout=SLOW_MS)
    expect(status).to_contain_text("0 to rename", timeout=SLOW_MS)
    expect(apply_btn).to_be_disabled()
