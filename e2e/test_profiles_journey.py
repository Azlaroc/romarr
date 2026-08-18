"""Settings → Profiles + Activity → Blocklist journeys (F6 PR-C).

CRUD the per-platform quality/format profiles and release profiles through the
UI, and drive the blocklist screen. Runs first: pytest groups the
[chromium]-parametrized browser tests into one block ahead of the API-only
journeys, and this file sorts first within that block. State-neutral:
everything it creates it also deletes, and it NEVER touches the seeded global
default profile — that row is owned by test_zzz_selector_journey.py.
"""
import json
import re
import urllib.request

from playwright.sync_api import expect

SLOW_MS = 15_000


def _req(base: str, path: str, method: str = "GET", payload: dict | None = None) -> dict:
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def _nav(page, section: str, title: str):
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


def _open_profiles(page):
    _nav(page, "settings", "Settings")
    page.get_by_test_id("nav-settings-profiles").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_be_visible(timeout=SLOW_MS)


def test_quality_profile_crud(ui):
    page = ui["page"]
    _open_profiles(page)

    # Seeded rows render, including the two templates a new platform clones
    # its default from.
    qlist = page.get_by_test_id("profiles-quality-list")
    expect(qlist).to_contain_text("ROM Default", timeout=SLOW_MS)
    expect(qlist).to_contain_text("PC Default", timeout=SLOW_MS)
    expect(qlist).to_contain_text("Carts Default", timeout=SLOW_MS)
    expect(qlist).to_contain_text("Discs Default", timeout=SLOW_MS)

    # Create a profile. A profile is free-standing now — no platform field —
    # and which platform DEFAULTS to it is set on the Platforms page, so the
    # editor reports that relationship rather than owning it.
    page.get_by_test_id("qp-add").click()
    page.get_by_test_id("qp-name").fill("E2E SNES")
    expect(page.get_by_test_id("qp-platform")).to_have_count(0)
    # Any profile may be the global default now; nothing disables the toggle.
    expect(page.get_by_test_id("qp-default-toggle")).to_be_enabled()

    page.get_by_test_id("qp-region-add-usa").click()
    page.get_by_test_id("qp-region-add-world").click()
    page.get_by_test_id("qp-region-add-europe").click()
    # [usa, world, europe] -> move europe up -> [usa, europe, world]
    page.get_by_test_id("qp-region-up-2").click()
    expect(page.get_by_test_id("qp-region-item-1")).to_contain_text("europe")

    page.get_by_test_id("qp-1g1r").click()  # default true -> off
    page.get_by_test_id("qp-size-min").fill("1")
    page.get_by_test_id("qp-save").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_contain_text("E2E SNES", timeout=SLOW_MS)

    # Two profiles may now target the same platform — "PSX CHD" and "PSX raw"
    # are both legitimate — so the old duplicate-platform refusal is gone.
    page.get_by_test_id("qp-add").click()
    page.get_by_test_id("qp-name").fill("E2E SNES Two")
    page.get_by_test_id("qp-save").click()
    expect(page.get_by_test_id("profiles-quality-list")).to_contain_text("E2E SNES Two", timeout=SLOW_MS)
    row = page.locator('[data-testid="profiles-quality-list"] > div', has_text="E2E SNES Two").first
    row.locator('[data-testid^="qp-delete-"]').click()
    page.get_by_test_id("confirm-ok").click()
    expect(page.get_by_test_id("profiles-quality-list")).not_to_contain_text("E2E SNES Two", timeout=SLOW_MS)

    # Duplicate name -> caught client-side (backend would 500 on its UNIQUE column).
    page.get_by_test_id("qp-add").click()
    page.get_by_test_id("qp-name").fill("ROM Default")
    page.get_by_test_id("qp-save").click()
    expect(page.get_by_text("A profile with this name already exists")).to_be_visible(timeout=SLOW_MS)
    page.get_by_test_id("qp-cancel").click()

    # Reopen: the full-body PUT/POST round-trip preserved order and toggles.
    row = page.locator('[data-testid="profiles-quality-list"] > div', has_text="E2E SNES").first
    row.locator('[data-testid^="qp-edit-"]').click()
    expect(page.get_by_test_id("qp-region-item-0")).to_contain_text("usa")
    expect(page.get_by_test_id("qp-region-item-1")).to_contain_text("europe")
    expect(page.get_by_test_id("qp-region-item-2")).to_contain_text("world")
    expect(page.get_by_test_id("qp-1g1r")).not_to_be_checked()
    expect(page.get_by_test_id("qp-size-min")).to_have_value("1")
    # The editor says what defaults to this profile instead of setting it.
    expect(page.get_by_test_id("qp-used-by")).to_contain_text("No platform defaults to this profile")
    page.get_by_test_id("qp-cancel").click()

    # Delete it again (state-neutral).
    row = page.locator('[data-testid="profiles-quality-list"] > div', has_text="E2E SNES").first
    row.locator('[data-testid^="qp-delete-"]').click()
    page.get_by_test_id("confirm-ok").click()
    expect(page.get_by_test_id("profiles-quality-list")).not_to_contain_text("E2E SNES", timeout=SLOW_MS)


def test_release_profile_crud(ui):
    page = ui["page"]
    _open_profiles(page)

    rlist = page.get_by_test_id("profiles-release-list")
    expect(rlist).to_contain_text("Game Scene Default", timeout=SLOW_MS)

    page.get_by_test_id("rp-add").click()
    page.get_by_test_id("rp-name").fill("E2E Words")
    page.get_by_test_id("rp-mustnot-input").fill("cracked")
    page.get_by_test_id("rp-mustnot-add").click()
    expect(page.get_by_test_id("rp-mustnot-item-0")).to_contain_text("cracked")
    page.get_by_test_id("rp-preferred-add").click()
    page.get_by_test_id("rp-preferred-word-0").fill("repack")
    page.get_by_test_id("rp-preferred-score-0").fill("-100")
    page.get_by_test_id("rp-save").click()
    expect(page.get_by_test_id("profiles-release-list")).to_contain_text("E2E Words", timeout=SLOW_MS)

    # Round-trip proof (PUT does not nil-coerce slices — arrays must survive).
    row = page.locator('[data-testid="profiles-release-list"] > div', has_text="E2E Words").first
    row.locator('[data-testid^="rp-edit-"]').click()
    expect(page.get_by_test_id("rp-mustnot-item-0")).to_contain_text("cracked")
    expect(page.get_by_test_id("rp-preferred-word-0")).to_have_value("repack")
    expect(page.get_by_test_id("rp-preferred-score-0")).to_have_value("-100")
    page.get_by_test_id("rp-cancel").click()

    row = page.locator('[data-testid="profiles-release-list"] > div', has_text="E2E Words").first
    row.locator('[data-testid^="rp-delete-"]').click()
    page.get_by_test_id("confirm-ok").click()
    expect(page.get_by_test_id("profiles-release-list")).not_to_contain_text("E2E Words", timeout=SLOW_MS)


def test_blocklist_screen(ui, app):
    page = ui["page"]
    base = app["base"]

    _nav(page, "activity", "Activity")
    page.get_by_test_id("nav-activity-blocklist").click()
    # Container stays mounted when empty.
    expect(page.get_by_test_id("blocklist-table")).to_be_attached(timeout=SLOW_MS)
    expect(page.get_by_text(re.compile("blocklist is empty", re.I))).to_be_visible(timeout=SLOW_MS)

    # Seed an entry via the API; the screen polls (5s) so it appears unprompted.
    _req(base, "/api/blocklist", "POST", {
        "title": "E2E Blocked Release",
        "download_url": "http://example.invalid/blocked.zip",
        "source": "e2e",
        "reason": "seeded by test",
    })
    table = page.get_by_test_id("blocklist-table")
    expect(table).to_contain_text("E2E Blocked Release", timeout=SLOW_MS)
    expect(table).to_contain_text("seeded by test", timeout=SLOW_MS)

    # Remove it via the row control; the mounted container shrinks to empty.
    row = page.locator('[data-testid="blocklist-table"] > div', has_text="E2E Blocked Release").first
    row.locator('[data-testid^="bl-remove-"]').click()
    expect(table).not_to_contain_text("E2E Blocked Release", timeout=SLOW_MS)
