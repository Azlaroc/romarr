"""Library-scan smoke (runs beside the hash smoke in the browser block).

State-neutral BY CONSTRUCTION rather than by cleanup: the run is a dry run,
which is the whole point of the flag — it walks, measures, reports what it
would adopt or create, and writes nothing. Nothing to undo, so nothing can be
left behind if an assertion fails partway.

What it proves that a Go test cannot: the page is reachable from the Library
header, the poller stops on its own, and the dry-run badge renders.
"""
from playwright.sync_api import expect

SLOW_MS = 15_000


def test_scan_dry_run_writes_nothing(ui):
    page = ui["page"]
    # Reached from the Library header, not the nav — like Rename and Hashes.
    page.get_by_test_id("library-scan-link").click()
    expect(page.get_by_test_id("page-title")).to_have_text("Scan", timeout=SLOW_MS)
    expect(page.get_by_test_id("scan-controls")).to_be_visible(timeout=SLOW_MS)

    # Dry run is the default, so the button says so and the run is safe.
    run_btn = page.get_by_test_id("scan-run-btn")
    expect(run_btn).to_contain_text("Dry run")

    run_btn.click()
    status = page.get_by_test_id("scan-status")
    # The poller has to notice the run ended on its own; "Last run finished"
    # only appears once the status query returns running:false.
    expect(status).to_contain_text("finished", timeout=SLOW_MS)
    expect(status).to_contain_text("dry run", timeout=SLOW_MS)

    # Ready to go again rather than stuck mid-run.
    expect(run_btn).to_be_enabled(timeout=SLOW_MS)
