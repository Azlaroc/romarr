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

from conftest import QBIT_PAYLOAD, QBIT_STATE

HASH = "ab" * 20
NAME = "Wario Land - Super Mario Land 3 (World)"


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
