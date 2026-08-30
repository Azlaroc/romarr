"""System section journeys (F6 PR-G): Status, Tasks, Users, Backup.

Runs inside the [chromium] browser block (before test_user_journey — 's' < 'u')
with only state-neutral tests ahead of it, so the wishlist is empty when the
run-now click fires a real enforce cycle (it grabs nothing).

🔴 MUST NOT create users or invites: the first user account flips the shared
app out of open mode and every later journey's anonymous API access breaks.
Render-only assertions for Users/Invites.
"""
import re

from playwright.sync_api import expect

SLOW_MS = 15_000


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def test_system_status_and_stats(ui):
    page = ui["page"]
    _nav(page, "system", "System")
    # /system redirects to Status.
    expect(page.get_by_test_id("system-health")).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("system-about")).to_be_visible(timeout=SLOW_MS)
    # Collection stats relocated here from Settings (PR-G).
    expect(page.get_by_test_id("system-stats")).to_be_visible(timeout=SLOW_MS)


def test_system_tasks_scheduler(ui):
    page = ui["page"]
    _nav(page, "system", "System")
    page.get_by_test_id("nav-system-tasks").click()

    # The harness boots with SELECTOR_MODE=enforce — the badge must say so.
    expect(page.get_by_test_id("tasks-selector-mode")).to_contain_text("enforce", timeout=SLOW_MS)

    # Empty wishlist at this point in the block: the cycle is a safe no-op.
    page.get_by_test_id("tasks-run-now").click()
    expect(page.get_by_test_id("toast-container")).to_contain_text(
        re.compile("cycle started", re.I), timeout=SLOW_MS)


def test_system_users_render_only(ui):
    page = ui["page"]
    _nav(page, "system", "System")
    page.get_by_test_id("nav-system-users").click()

    # Open mode: zero accounts; the mounted table renders its empty note.
    expect(page.get_by_test_id("users-table")).to_be_attached(timeout=SLOW_MS)
    expect(page.get_by_text(re.compile("open mode", re.I))).to_be_visible(timeout=SLOW_MS)
    expect(page.get_by_test_id("invite-create")).to_be_visible(timeout=SLOW_MS)
    # Deliberately no clicks — creating a user/invite would end open mode.


def test_system_backup_links(ui):
    page = ui["page"]
    _nav(page, "system", "System")
    page.get_by_test_id("nav-system-backup").click()

    dl = page.get_by_test_id("backup-download")
    expect(dl).to_be_visible(timeout=SLOW_MS)
    assert dl.get_attribute("href") == "/api/backup"
    expect(page.get_by_test_id("export-library")).to_have_attribute("href", "/api/export/library")
