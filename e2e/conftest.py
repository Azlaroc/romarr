"""Hermetic end-to-end test harness for the RomArr web UI.

Boots the real gamarr binary against a single local stub server that
impersonates every external service RomArr talks to, so the full user
journey — search, DDL download, organize, library, wishlist, settings —
runs with ZERO external network access:

    [chromium] -> [gamarr binary] -> [stub qBittorrent/Prowlarr/archive.org on 127.0.0.1]

The injected sources registry points the native archive.org driver at the stub
(which serves a real ZIP so the DDL pipeline completes for real) and Vimm at a
dead local port that refuses connections instantly, keeping runs fast and
deterministic. Myrient was retired (host shut down 2026-03-31); archive.org is
the DDL source of record.

Requires: the gamarr binary (built automatically, or set GAMARR_E2E_BIN),
pytest-playwright with chromium installed.
"""
import hashlib
import io
import json
import os
import re
import socket
import subprocess
import threading
import time
import urllib.parse
import urllib.request
import zipfile
from http.server import BaseHTTPRequestHandler, ThreadingHTTPServer
from pathlib import Path

import pytest

REPO_ROOT = Path(__file__).resolve().parent.parent

# Stateful qBittorrent stub (F3): torrents/add registers a torrent that is
# instantly complete-and-seeding, materializing a real payload under the app's
# incoming dir so the completion watcher can import it. The app fixture fills
# in "incoming" before the binary boots. Keyed by lowercase infohash.
QBIT_STATE = {"incoming": None, "torrents": {}}

BTIH_RE = re.compile(r"xt=urn:btih:([0-9a-fA-F]{40})")
QBIT_PAYLOAD = b"QBIT-E2E-ROM" * 64

# archive.org collection item the stub serves for the "gb" platform.
IA_GB_ITEM = "nointro-gb-e2e"

# archive.org fixture files for the "gb" platform. Names mirror No-Intro naming
# so region filtering and title matching run the same code paths as prod.
GB_FILES = [
    "Tetris (World) (Rev 1).zip",
    "Tetris Attack (USA, Europe) (SGB Enhanced).zip",
    "Wario Land - Super Mario Land 3 (World).zip",
]

# Prowlarr fixture releases: one obviously-good seeded release and one
# zero-seeder release so the UI renders both shapes.
PROWLARR_RELEASES = [
    {
        "title": "Stardew Harvest Deluxe Edition [FitGirl Repack]",
        "size": 1_500_000_000,
        "seeders": 120,
        "leechers": 4,
        "indexer": "StubIndexer",
        "downloadUrl": "http://127.0.0.1:1/stub.torrent",
        "guid": "stub-guid-1",
        "categories": [{"id": 4050}],
        "age": 12,
    },
    {
        "title": "Stardew Harvest (Repack, dead)",
        "size": 900_000_000,
        "seeders": 0,
        "leechers": 0,
        "indexer": "StubIndexer",
        "downloadUrl": "http://127.0.0.1:1/dead.torrent",
        "guid": "stub-guid-2",
        "categories": [{"id": 4050}],
        "age": 400,
    },
]


# RomM stub: the library-ownership source the sync mirrors. Credentials must
# match the ROMM_API_* env in the app fixture — the stub 401s without them,
# which is exactly how a real RomM behaves.
ROMM_USER = "romarr-e2e"
ROMM_PASS = "romm-stub-pw"
ROMM_PAGE = 3  # server-side page size: forces the sync client through 2 pages

ROMM_PLATFORMS = [
    {"id": 41, "slug": "psx", "fs_slug": "psx", "name": "PlayStation", "rom_count": 5},
]


def _romm_rom(rid: int, fs_name: str, name: str, missing: bool = False) -> dict:
    stem = fs_name.rsplit(".", 1)[0] if "." in fs_name else fs_name
    return {
        "id": rid,
        "platform_id": 41,
        "platform_slug": "psx",
        "platform_fs_slug": "psx",
        "fs_name": fs_name,
        "fs_name_no_tags": stem.split(" (")[0],
        "fs_name_no_ext": stem,
        "fs_path": "roms/psx",
        "fs_size_bytes": 1_000_000 + rid,
        "name": name,
        "crc_hash": f"crc{rid}",
        "md5_hash": f"md5{rid}",
        "sha1_hash": f"sha{rid}",
        "igdb_id": 10_000 + rid,
        "missing_from_fs": missing,
    }


ROMM_ROMS = [
    _romm_rom(101, "Castlevania - Symphony of the Night (USA).chd", "Castlevania: Symphony of the Night"),
    _romm_rom(102, "Wipeout (USA).chd", "Wipeout"),
    _romm_rom(103, "Ridge Racer (USA).chd", "Ridge Racer"),
    _romm_rom(104, "Crash Bandicoot (USA).chd", "Crash Bandicoot"),
    _romm_rom(105, "Vanished Game (USA).chd", "Vanished Game", missing=True),
]


def _free_port() -> int:
    with socket.socket() as s:
        s.bind(("127.0.0.1", 0))
        return s.getsockname()[1]


def _rom_zip(name: str) -> bytes:
    """A small but genuine ZIP containing a fake .gb ROM. Deterministic bytes
    (zipfile stamps writestr entries with a fixed 1980 date), so the md5/sha1/
    size published in the metadata below stay stable."""
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr(name.replace(".zip", ".gb"), b"GAMARR-E2E-ROM" * 64)
    return buf.getvalue()


def _ia_metadata() -> bytes:
    """An archive.org /metadata/<item> document for the gb item: one files[]
    entry per GB_FILES, carrying real size/md5/sha1 (archive.org encodes size
    as a string) so the driver parses exactly the prod shape."""
    files = []
    for n in GB_FILES:
        b = _rom_zip(n)
        files.append({
            "name": n,
            "source": "original",
            "format": "ZIP",
            "size": str(len(b)),
            "md5": hashlib.md5(b).hexdigest(),
            "sha1": hashlib.sha1(b).hexdigest(),
        })
    return json.dumps(
        {"server": "stub", "dir": f"/{IA_GB_ITEM}", "files": files}
    ).encode()


class _StubHandler(BaseHTTPRequestHandler):
    """One handler impersonating qBittorrent + Prowlarr + archive.org + RomM."""

    def _send(self, code: int, body: bytes, ctype: str):
        self.send_response(code)
        self.send_header("Content-Type", ctype)
        self.send_header("Content-Length", str(len(body)))
        self.end_headers()
        self.wfile.write(body)

    def _romm_auth_ok(self) -> bool:
        import base64
        want = "Basic " + base64.b64encode(f"{ROMM_USER}:{ROMM_PASS}".encode()).decode()
        return self.headers.get("Authorization") == want

    # ── qBittorrent (stateful) ─────────────────────────────────────────────
    def do_POST(self):  # noqa: N802 (http.server API)
        body = self.rfile.read(int(self.headers.get("Content-Length", 0) or 0)).decode("utf-8", "replace")
        path = self.path.split("?")[0]
        if path == "/api/v2/auth/login":
            # Real qBittorrent 5.x behavior: HTTP 200 + "Ok." on success.
            self._send(200, b"Ok.", "text/plain")
        elif path == "/api/v2/torrents/add":
            form = urllib.parse.parse_qs(body)
            urls = form.get("urls", [""])[0]
            m = BTIH_RE.search(urls)
            if m and QBIT_STATE["incoming"]:
                h = m.group(1).lower()
                name = f"Stub Torrent {h[:8]}"
                dn = re.search(r"dn=([^&]+)", urls)
                if dn:
                    name = urllib.parse.unquote_plus(dn.group(1))
                content = Path(QBIT_STATE["incoming"]) / name
                content.mkdir(parents=True, exist_ok=True)
                (content / f"{name}.gb").write_bytes(QBIT_PAYLOAD)
                QBIT_STATE["torrents"][h] = {
                    "name": name,
                    "hash": h,
                    "progress": 1.0,
                    "amount_left": 0,
                    "state": "uploading",
                    "total_size": len(QBIT_PAYLOAD),
                    "dlspeed": 0,
                    "eta": 0,
                    "save_path": str(QBIT_STATE["incoming"]),
                    "content_path": str(content),
                    "category": form.get("category", [""])[0],
                    "tags": form.get("tags", [""])[0],
                    "ratio": 0.0,
                }
            self._send(200, b"Ok.", "text/plain")
        elif path == "/api/v2/torrents/delete":
            form = urllib.parse.parse_qs(body)
            for h in form.get("hashes", [""])[0].split("|"):
                QBIT_STATE["torrents"].pop(h.lower(), None)
            self._send(200, b"", "text/plain")
        else:
            self._send(404, b"not found", "text/plain")

    def do_GET(self):  # noqa: N802
        path = self.path.split("?")[0]

        if path == "/api/v2/torrents/info":
            from urllib.parse import parse_qs, urlparse
            q = parse_qs(urlparse(self.path).query)
            ts = list(QBIT_STATE["torrents"].values())
            if q.get("hashes", [""])[0]:
                want = set(q["hashes"][0].lower().split("|"))
                ts = [t for t in ts if t["hash"] in want]
            if q.get("tag", [""])[0]:
                tag = q["tag"][0]
                ts = [t for t in ts if tag in [x.strip() for x in (t.get("tags") or "").split(",")]]
            if q.get("category", [""])[0]:
                ts = [t for t in ts if t.get("category") == q["category"][0]]
            self._send(200, json.dumps(ts).encode(), "application/json")
        elif path == "/api/v2/torrents/files":
            from urllib.parse import parse_qs, urlparse
            h = parse_qs(urlparse(self.path).query).get("hash", [""])[0].lower()
            t = QBIT_STATE["torrents"].get(h)
            files = [{"name": f"{t['name']}/{t['name']}.gb"}] if t else []
            self._send(200, json.dumps(files).encode(), "application/json")
        # ── Prowlarr ──────────────────────────────────────────────────────
        elif path == "/api/v1/search":
            q = ""
            if "?" in self.path:
                from urllib.parse import parse_qs, urlparse
                q = parse_qs(urlparse(self.path).query).get("query", [""])[0].lower()
            hits = [r for r in PROWLARR_RELEASES if q and q.split()[0] in r["title"].lower()]
            self._send(200, json.dumps(hits).encode(), "application/json")
        elif path == "/api/v1/indexer":
            self._send(200, b"[]", "application/json")
        # ── RomM: heartbeat + platforms + paginated roms ──────────────────
        elif path == "/api/heartbeat":
            self._send(200, b"{}", "application/json")
        elif path == "/api/platforms":
            if not self._romm_auth_ok():
                self._send(401, b"unauthorized", "text/plain")
                return
            self._send(200, json.dumps(ROMM_PLATFORMS).encode(), "application/json")
        elif path == "/api/roms":
            if not self._romm_auth_ok():
                self._send(401, b"unauthorized", "text/plain")
                return
            from urllib.parse import parse_qs, urlparse
            offset = int(parse_qs(urlparse(self.path).query).get("offset", ["0"])[0])
            body = {
                "items": ROMM_ROMS[offset:offset + ROMM_PAGE],
                "total": len(ROMM_ROMS),
                "limit": ROMM_PAGE,
                "offset": offset,
            }
            self._send(200, json.dumps(body).encode(), "application/json")
        # ── archive.org: metadata listing + per-file downloads ────────────
        elif path == f"/metadata/{IA_GB_ITEM}":
            self._send(200, _ia_metadata(), "application/json")
        elif path.startswith(f"/download/{IA_GB_ITEM}/"):
            name = urllib.parse.unquote(path.split(f"/download/{IA_GB_ITEM}/", 1)[1])
            if name in GB_FILES:
                self._send(200, _rom_zip(name), "application/zip")
            else:
                self._send(404, b"no such file", "text/plain")
        else:
            self._send(404, b"not found", "text/plain")

    def log_message(self, *args):  # keep pytest output clean
        pass


@pytest.fixture(scope="session")
def stub_server():
    port = _free_port()
    httpd = ThreadingHTTPServer(("127.0.0.1", port), _StubHandler)
    t = threading.Thread(target=httpd.serve_forever, daemon=True)
    t.start()
    yield f"http://127.0.0.1:{port}"
    httpd.shutdown()


@pytest.fixture(scope="session")
def gamarr_binary(tmp_path_factory) -> Path:
    env_bin = os.environ.get("GAMARR_E2E_BIN")
    if env_bin:
        return Path(env_bin).resolve()
    out = tmp_path_factory.mktemp("bin") / "gamarr"
    subprocess.run(
        ["go", "build", "-o", str(out), "./cmd/gamarr"],
        cwd=REPO_ROOT, check=True,
    )
    return out


@pytest.fixture(scope="session")
def app(stub_server, gamarr_binary, tmp_path_factory):
    """Boot gamarr with an injected registry: archive.org -> stub (a live gb
    item), Vimm -> dead port that refuses connections instantly."""
    data = tmp_path_factory.mktemp("data")
    dead = "http://127.0.0.1:1/"
    registry = {
        "version": 1,
        "archiveorg": {
            "base_url": stub_server,
            "items": {"gb": IA_GB_ITEM},
        },
        "vimm": {"base_url": dead, "platform_systems": {}},
    }
    reg_path = data / "sources.json"
    reg_path.write_text(json.dumps(registry))

    port = _free_port()
    vault = data / "vault"
    roms = data / "roms"
    incoming = data / "incoming"
    vault.mkdir()
    roms.mkdir()
    incoming.mkdir()

    env = {
        **os.environ,
        "GAMARR_PORT": str(port),
        "DATA_DIR": str(data / "gamarr"),
        "GAMES_VAULT_PATH": str(vault),
        "GAMES_ROMS_PATH": str(roms),
        "GAMARR_SOURCES_PATH": str(reg_path),
        "QB_URL": stub_server,
        "QB_USER": "e2e",
        "QB_PASS": "e2e",
        # DDL staging dir — defaults to /data/incoming/ which won't exist on
        # a dev box; without this the pipeline dies as a bare "Download failed".
        "QB_SAVE_PATH": str(incoming),
        # Fast watcher ticks so the torrent journey completes in seconds.
        "WATCHER_INTERVAL": "2",
        "PROWLARR_URL": stub_server,
        "PROWLARR_API_KEY": "e2e-stub-key",
        # RomM ownership sync: boot triggers an immediate full sync against
        # the stub, so the Library shows RomM-owned titles with no local file.
        "ROMM_URL": stub_server,
        "ROMM_API_USER": ROMM_USER,
        "ROMM_API_PASS": ROMM_PASS,
    }
    QBIT_STATE["incoming"] = str(incoming)
    log = open(data / "gamarr.log", "w")
    proc = subprocess.Popen([str(gamarr_binary)], env=env, stdout=log, stderr=log)

    base = f"http://127.0.0.1:{port}"
    for _ in range(60):
        try:
            urllib.request.urlopen(f"{base}/api/health", timeout=1)
            break
        except Exception:
            if proc.poll() is not None:
                log.close()
                raise RuntimeError(
                    "gamarr exited during startup:\n" + (data / "gamarr.log").read_text())
            time.sleep(0.5)
    else:
        proc.kill()
        raise RuntimeError("gamarr did not become healthy within 30s")

    yield {"base": base, "data": data, "roms_dir": roms, "vault_dir": vault}

    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
    log.close()


@pytest.fixture()
def ui(app, page):
    """A page on the gamarr UI that records every JS error. Tests assert the
    journey stays error-free (the strongest 'the frontend works' invariant)."""
    errors: list[str] = []
    page.on("pageerror", lambda e: errors.append(f"pageerror: {e}"))
    page.on(
        "console",
        lambda m: errors.append(f"console: {m.text}")
        if m.type == "error" and "Failed to load resource" not in m.text
        else None,
    )
    page.goto(app["base"], wait_until="networkidle")
    yield {"page": page, "errors": errors, **app}
    assert errors == [], f"JS errors during journey: {errors}"
