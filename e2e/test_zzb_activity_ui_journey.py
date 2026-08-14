"""Activity split journeys (F6 PR-D): grouped disc-set rows in the Queue and
the History screen.

Ordering: browser tests run as one [chromium] block BEFORE the API-only
test_z* journeys (pytest parametrized-fixture grouping), and zzb sorts after
test_user_journey within that block. So this test may not consume the API
journeys' state (they haven't run yet) and must not poison it: it never
touches FF7 / Chrono Cross (owned by test_zz_discset / test_zzz), seeds its
disc set with deliberately-nonexistent stub files (members error out — the
grouping UI renders the same, and nothing lands in the library or on disk),
and clears its error jobs on the way out.
"""
import json
import time
import urllib.request

from playwright.sync_api import expect

SLOW_MS = 15_000
SET_ID = "set-e2e-ui"
SET_DIR = "Phantom Quest (USA)"


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


def test_queue_groups_disc_sets_and_history_renders(ui, app, stub_server):
    page = ui["page"]
    base = app["base"]

    # Seed a synthetic 2-disc set. The files do not exist in the stub catalog,
    # so both members fail fast — by design (see module docstring).
    for i in (1, 2):
        resp = _req(base, "/api/download", "POST", {
            "download_url": f"{stub_server}/download/redump-psx-e2e/Phantom%20Quest%20%28USA%29%20%28Disc%20{i}%29.zip",
            "title": f"Phantom Quest (USA) (Disc {i})",
            "platform": "PlayStation",
            "platform_slug": "psx",
            "source_type": "ddl",
            "disc_set_id": SET_ID,
            "disc_index": i,
            "disc_total": 2,
            "set_dir": SET_DIR,
        })
        assert resp.get("success"), f"disc {i} rejected: {resp}"

    # The queue groups the two members under one expandable set card.
    _nav(page, "activity", "Activity")
    group = page.get_by_test_id(f"queue-group-{SET_ID}")
    expect(group).to_be_visible(timeout=SLOW_MS)
    expect(group).to_contain_text("Phantom Quest (USA)", timeout=SLOW_MS)  # disc token stripped
    expect(group).to_contain_text("2-disc set", timeout=SLOW_MS)

    # Members may start collapsed (both already terminal) — expand if needed.
    members = page.locator(f'[data-testid^="queue-group-item-{SET_ID}-"]')
    if members.count() == 0:
        page.get_by_test_id(f"queue-group-toggle-{SET_ID}").click()
    expect(members).to_have_count(2, timeout=SLOW_MS)
    expect(members.first).to_contain_text("Disc 1 of 2")
    expect(members.nth(1)).to_contain_text("Disc 2 of 2")

    # Collapse hides the member rows; the group card stays.
    page.get_by_test_id(f"queue-group-toggle-{SET_ID}").click()
    expect(members).to_have_count(0, timeout=SLOW_MS)
    expect(group).to_be_visible()

    # History: the block's earlier DDL import (test_user_journey) is on record.
    page.get_by_test_id("nav-activity-history").click()
    expect(page.get_by_test_id("history-list")).to_be_attached(timeout=SLOW_MS)
    expect(page.get_by_test_id("history-list")).to_contain_text("Imported", timeout=SLOW_MS)
    assert page.locator('[data-testid^="history-row-"]').count() > 0

    # State-neutral exit: wait for both members to reach a clearable state,
    # then clear them and prove nothing leaked into wishlist or the queue.
    def _phantoms():
        return [d for d in _req(base, "/api/downloads").get("downloads", [])
                if "Phantom" in (d.get("title") or "")]

    deadline = time.time() + 45
    while time.time() < deadline:
        jobs = _phantoms()
        if len(jobs) == 2 and all(j.get("status") in ("error", "completed", "dead_letter") for j in jobs):
            break
        time.sleep(0.5)

    # Both members carry the disc-set fields on the wire (the PR-D backend
    # addition) — belt-and-braces beyond the UI render assertions above.
    jobs = _phantoms()
    assert len(jobs) == 2
    for j in jobs:
        assert j.get("disc_set_id") == SET_ID, f"missing disc_set_id on {j}"
        assert j.get("disc_total") == 2, f"missing disc_total on {j}"

    # dead_letter survives /clear — retry it down to error first if needed.
    for j in jobs:
        if j.get("status") == "dead_letter" and j.get("job_id"):
            _req(base, f"/api/downloads/{j['job_id']}", "DELETE")
    _req(base, "/api/downloads/clear", "POST", {})
    deadline = time.time() + 15
    while time.time() < deadline and _phantoms():
        time.sleep(0.5)
    assert not _phantoms(), "Phantom jobs must be cleared"
    wl = _req(base, "/api/wishlist")
    rows = wl if isinstance(wl, list) else wl.get("wishlist") or wl.get("items") or []
    assert not any("Phantom" in (w.get("title") or "") for w in rows), "wishlist must be untouched"
