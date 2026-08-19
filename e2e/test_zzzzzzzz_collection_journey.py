"""Collection-mode journey — the 1G1R set becomes wanted work, through the
real server.

This is the epic's acceptance clause in miniature: turn one switch on for a
platform, and everything its catalog says should exist but the library does
not have becomes a listed gap. No file edits, no restart.

Ordering: it sorts after the DAT journey and reuses the catalog that journey
uploaded for gb, which is deliberate — a set means nothing without a catalog,
and importing a second one here would only duplicate that file's work.

State: it turns collection mode ON and then OFF again, and the off-switch is
itself an assertion (leaving collection mode must drop the gap list). The
cleanup runs in a finally block, because a journey that fails halfway must
still not leave the next one a platform quietly acquiring a whole catalog.
"""
import json
import urllib.error
import urllib.request


def _req(base: str, path: str, method: str = "GET", payload: dict | None = None) -> dict:
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.loads(r.read() or b"{}")


def _set_collection(base: str, slug: str, on: bool) -> dict:
    return _req(base, f"/api/platforms/{slug}", "PUT", {"collection_mode": on})


def test_collection_mode_turns_a_catalog_into_wanted_work(app):
    base = app["base"]

    catalogued = _req(base, "/api/platforms/gb/set")
    assert catalogued["counts"]["groups"] > 0, (
        "the gb catalog is missing — this journey runs after the DAT one for a reason"
    )
    gaps = catalogued["counts"]["gaps"]
    assert gaps > 0, catalogued["counts"]

    # Every entry explains itself: a keeper says what it won on, a surplus dump
    # says what it lost to. A set nobody can read is a set nobody can trust.
    for entry in catalogued["entries"]:
        for member in entry["members"]:
            assert member.get("reason"), (entry["title"], member["name"])

    # Nothing is monitored yet, so nothing is wanted.
    assert _req(base, "/api/collection/targets")["total"] == 0

    try:
        row = _set_collection(base, "gb", True)
        assert row["collection_mode"] is True, row

        synced = _req(base, "/api/collection/sync?platform=gb", "POST", {})
        assert synced["success"], synced
        assert synced["results"][0]["added"] == gaps, synced

        listed = _req(base, "/api/collection/targets?platform=gb")
        assert listed["total"] == gaps, listed
        assert "gb" in listed["platforms"], listed
        # The pace is visible next to the queue it paces.
        assert listed["fill_per_cycle"] >= 0, listed
        first = listed["targets"][0]
        assert first["status"] == "wanted", first
        assert first["title"], first
        # The catalogue name rides along: it is what the set actually wants.
        assert first["dump_name"], first

        # A second sync is a no-op, not a re-add: attempt history has to
        # survive, or a title nothing carries is searched every cycle forever.
        again = _req(base, "/api/collection/sync?platform=gb", "POST", {})
        assert again["results"][0]["added"] == 0, again
        assert _req(base, "/api/collection/targets?platform=gb")["total"] == gaps
    finally:
        _set_collection(base, "gb", False)

    # 🔴 Leaving collection mode drops the queue with it: the targets are
    # derived from a policy that no longer applies.
    assert _req(base, "/api/collection/targets")["total"] == 0
    assert _req(base, "/api/platforms/gb")["collection_mode"] is False
