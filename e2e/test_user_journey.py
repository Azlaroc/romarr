"""Hermetic Playwright journey tests for the RomArr web UI (React SPA).

Every test runs against the real compiled binary + real Chromium, with all
external services stubbed on loopback (see conftest.py). The `ui` fixture
fails any test that produced a JS error, so each journey doubles as a
zero-console-error assertion.

The UI is a client-rendered React SPA (Vite build embedded in the Go binary).
Navigation is a left sidebar (data-testid="nav-*"); each screen is a route with
a data-testid="page-title" header and screen-specific testids.
"""
import re
import urllib.request

from playwright.sync_api import expect

SLOW_MS = 15_000  # generous single-action timeout; suite stays fast when green


def _nav(page, section: str, title: str):
    """Click a sidebar entry and wait for its page to render."""
    page.get_by_test_id(f"nav-{section}").click()
    expect(page.get_by_test_id("page-title")).to_have_text(title, timeout=SLOW_MS)


# ── boot & shell ──────────────────────────────────────────────────────────────

def test_boot_shell_and_nav(ui):
    page = ui["page"]
    expect(page).to_have_title("RomArr")
    # Every sidebar section navigates and renders its page header.
    _nav(page, "add-new", "Add New")
    _nav(page, "activity", "Activity")
    _nav(page, "wanted", "Wanted")
    _nav(page, "settings", "Settings")
    _nav(page, "library", "Library")


def test_no_cdn_and_csp(ui, app):
    """The UI must be self-contained: no third-party origins, strict CSP, and
    no inline event handlers (React attaches listeners, never onclick=)."""
    page = ui["page"]
    html = page.content()
    assert "cdn.tailwindcss.com" not in html, "UI must not depend on a CDN"
    assert "onclick=" not in html, "inline handlers violate the strict CSP"

    req = urllib.request.urlopen(app["base"], timeout=5)
    csp = req.headers.get("Content-Security-Policy", "")
    assert "script-src 'self'" in csp, f"weak or missing CSP: {csp!r}"


# ── search (Add New) ──────────────────────────────────────────────────────────

def test_ddl_search_renders_results(ui):
    page = ui["page"]
    _nav(page, "add-new", "Add New")
    page.get_by_test_id("search-input").fill("tetris")
    page.get_by_test_id("search-platform").select_option("gb")
    page.get_by_test_id("search-submit").click()
    results = page.get_by_test_id("results")
    expect(results).to_contain_text("Tetris (World) (Rev 1)", timeout=SLOW_MS)
    expect(results).to_contain_text("Tetris Attack", timeout=SLOW_MS)


def test_torrent_search_renders_results(ui):
    page = ui["page"]
    _nav(page, "add-new", "Add New")
    page.get_by_test_id("search-input").fill("stardew")
    page.get_by_test_id("search-platform").select_option("all")
    page.get_by_test_id("search-submit").click()
    results = page.get_by_test_id("results")
    expect(results).to_contain_text("Stardew Harvest Deluxe Edition", timeout=SLOW_MS)
    expect(results).to_contain_text("StubIndexer", timeout=SLOW_MS)


def test_search_no_results_is_clean(ui):
    page = ui["page"]
    _nav(page, "add-new", "Add New")
    page.get_by_test_id("search-input").fill("zzzznotagame")
    page.get_by_test_id("search-platform").select_option("gb")
    page.get_by_test_id("search-submit").click()
    # Whatever empty-state copy is used, no result rows and no JS errors.
    page.wait_for_timeout(1500)
    assert page.locator('[data-testid^="dl-btn-"]').count() == 0


# ── the flagship journey: DDL download -> organize -> library ─────────────────

def test_ddl_download_pipeline_to_library(ui, app):
    page = ui["page"]
    _nav(page, "add-new", "Add New")
    page.get_by_test_id("search-input").fill("tetris")
    page.get_by_test_id("search-platform").select_option("gb")
    page.get_by_test_id("search-submit").click()
    expect(page.get_by_test_id("results")).to_contain_text(
        "Tetris (World) (Rev 1)", timeout=SLOW_MS)

    # Download the first result.
    page.locator('[data-testid^="dl-btn-"]').first.click()

    # The job must reach the queue and complete. The Activity view self-refreshes
    # every 5s; expect() retries until the status lands.
    _nav(page, "activity", "Activity")
    expect(page.get_by_test_id("downloads")).to_contain_text(
        re.compile("completed", re.I), timeout=60_000)

    # The ROM physically landed in the roms dir.
    rom_files = list(app["roms_dir"].rglob("*Tetris*"))
    assert rom_files, f"no Tetris file under {app['roms_dir']}"

    # And the library shows it.
    _nav(page, "library", "Library")
    expect(page.get_by_test_id("library-grid")).to_contain_text("Tetris", timeout=SLOW_MS)


# ── wishlist CRUD (Wanted) ────────────────────────────────────────────────────

def test_wishlist_add_and_delete(ui):
    page = ui["page"]
    _nav(page, "wanted", "Wanted")
    page.get_by_test_id("wish-title").fill("Chrono Quest E2E")
    page.get_by_test_id("wish-platform").select_option(index=0)
    page.get_by_test_id("wish-add").click()
    expect(page.get_by_test_id("wishlist")).to_contain_text("Chrono Quest E2E", timeout=SLOW_MS)

    # Delete it again via the row's delete control.
    row = page.locator('[data-testid="wishlist"] > div', has_text="Chrono Quest E2E").first
    row.get_by_test_id("wish-delete").click()
    expect(page.get_by_test_id("wishlist")).not_to_contain_text(
        "Chrono Quest E2E", timeout=SLOW_MS)


# ── settings: connection tests against the stubs ──────────────────────────────

def test_settings_connection_tests(ui):
    page = ui["page"]
    _nav(page, "settings", "Settings")

    page.get_by_test_id("test-qbittorrent").click()
    expect(page.get_by_test_id("test-qbittorrent-status")).to_have_text(
        re.compile("connected", re.I), timeout=SLOW_MS)

    page.get_by_test_id("test-prowlarr").click()
    expect(page.get_by_test_id("test-prowlarr-status")).to_have_text(
        re.compile("connected", re.I), timeout=SLOW_MS)

    # SABnzbd is deliberately unconfigured in the harness.
    page.get_by_test_id("test-sabnzbd").click()
    expect(page.get_by_test_id("test-sabnzbd-status")).to_have_text(
        re.compile("fail|not configured", re.I), timeout=SLOW_MS)


def test_settings_shows_sources_and_stats(ui):
    page = ui["page"]
    _nav(page, "settings", "Settings")
    expect(page.get_by_test_id("settings-sources")).to_contain_text(
        re.compile("myrient", re.I), timeout=SLOW_MS)
    expect(page.get_by_test_id("settings-stats")).to_be_visible(timeout=SLOW_MS)
