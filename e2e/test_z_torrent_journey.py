"""Torrent completion journey (F3 PR-2): magnet submit → stateful qBit stub →
watcher import → ROM library, with the seeding payload left untouched.

Named test_z_* so it runs after the UI journey — it leaves a torrent behind in
the shared stub state, and the UI journey's Activity assertions predate it.

Pure API/filesystem test: no browser needed. WATCHER_INTERVAL=2 in the app
fixture keeps the whole cycle inside a few seconds.
"""
import json
import time
import urllib.parse
import urllib.request
from pathlib import Path

from conftest import QBIT_PACK_FILES, QBIT_PACK_NAME, QBIT_PAYLOAD, QBIT_STATE

HASH = "ab" * 20
NAME = "Wario Land - Super Mario Land 3 (World)"

PACK_HASH = "cd" * 20
PACK_TARGET = QBIT_PACK_FILES[1]  # "Baseball (World).gb"


def _post_json(base: str, path: str, payload: dict) -> dict:
    req = urllib.request.Request(
        base + path,
        data=json.dumps(payload).encode(),
        headers={"Content-Type": "application/json"},
    )
    with urllib.request.urlopen(req, timeout=10) as r:
        return json.loads(r.read() or b"{}")


def test_torrent_import_is_seed_safe(app):
    base = app["base"]
    magnet = f"magnet:?xt=urn:btih:{HASH}&dn={urllib.parse.quote_plus(NAME)}"

    resp = _post_json(base, "/api/download", {
        "magnet_url": magnet,
        "info_hash": HASH,
        "title": NAME,
        "platform": "Game Boy",
        "platform_slug": "gb",
        "download_protocol": "torrent",
    })
    assert resp.get("success"), f"download submit failed: {resp}"

    # The stub registered the torrent as instantly complete-and-seeding; the
    # watcher must pick it up and import it into the ROM library tree.
    dest = app["roms_dir"] / "gb" / NAME
    deadline = time.time() + 90
    while time.time() < deadline and not (dest / f"{NAME}.gb").exists():
        time.sleep(1)
    assert (dest / f"{NAME}.gb").exists(), "ROM never landed in the library tree"
    assert (dest / ".gamarr.json").exists(), "metadata sidecar missing"

    # Seed-safety: the torrent is still registered (never deleted) and its
    # payload is byte-identical — the import copied, not moved.
    t = QBIT_STATE["torrents"].get(HASH)
    assert t, "torrent was deleted from qBittorrent by the import"
    payload = Path(t["content_path"]) / f"{NAME}.gb"
    assert payload.exists(), "seeding payload file vanished"
    assert payload.read_bytes() == QBIT_PAYLOAD, "seeding payload was modified"


def test_selective_download_plucks_target(app):
    """#256: a pack torrent with target_file set downloads/imports ONLY the
    target — every other file is prio-0'd and never reaches the library."""
    base = app["base"]
    magnet = f"magnet:?xt=urn:btih:{PACK_HASH}&dn={urllib.parse.quote_plus(QBIT_PACK_NAME)}"

    resp = _post_json(base, "/api/download", {
        "magnet_url": magnet,
        "info_hash": PACK_HASH,
        "title": QBIT_PACK_NAME,
        "platform": "Game Boy",
        "platform_slug": "gb",
        "download_protocol": "torrent",
        "target_file": f"{QBIT_PACK_NAME}/{PACK_TARGET}",
    })
    assert resp.get("success"), f"pluck submit failed: {resp}"

    # Only the plucked file lands in the library.
    dest = app["roms_dir"] / "gb" / PACK_TARGET
    deadline = time.time() + 90
    while time.time() < deadline and not dest.exists():
        time.sleep(1)
    assert dest.exists(), "plucked file never landed in the library tree"
    assert dest.read_bytes() == QBIT_PAYLOAD

    # The other pack members never appear anywhere in the library.
    for other in QBIT_PACK_FILES:
        if other == PACK_TARGET:
            continue
        hits = list(app["roms_dir"].rglob(other))
        assert not hits, f"non-target pack file leaked into the library: {hits}"

    # The unwanted files were prio-0'd (indexes 0 and 2 for target index 1).
    prio_calls = [c for c in QBIT_STATE["file_prio"] if c["hash"] == PACK_HASH]
    assert prio_calls, "filePrio was never called for the pack"
    assert prio_calls[0]["priority"] == 0
    assert sorted(prio_calls[0]["ids"]) == [0, 2], prio_calls

    # Seed-safety still holds for the pack: torrent present, all 3 files intact.
    t = QBIT_STATE["torrents"].get(PACK_HASH)
    assert t, "pack torrent was deleted by the import"
    for fn in QBIT_PACK_FILES:
        p = Path(t["content_path"]) / fn
        assert p.exists() and p.read_bytes() == QBIT_PAYLOAD, f"pack payload touched: {fn}"
