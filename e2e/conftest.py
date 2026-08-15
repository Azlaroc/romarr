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
# "file_prio" records every torrents/filePrio call for journey assertions.
QBIT_STATE = {"incoming": None, "torrents": {}, "file_prio": []}

BTIH_RE = re.compile(r"xt=urn:btih:([0-9a-fA-F]{40})")
QBIT_PAYLOAD = b"QBIT-E2E-ROM" * 64

# A multi-file pack torrent (#256 selective download): adding a torrent with
# this display name materializes a 3-file loose pack instead of a single ROM.
QBIT_PACK_NAME = "GB Pack Vol 1 E2E"
QBIT_PACK_FILES = [
    "Alleyway (World).gb",
    "Baseball (World).gb",
    "Catrap (USA).gb",
]

# archive.org collection item the stub serves for the "gb" platform.
IA_GB_ITEM = "nointro-gb-e2e"

# archive.org fixture files for the "gb" platform. Names mirror No-Intro naming
# so region filtering and title matching run the same code paths as prod.
GB_FILES = [
    "Tetris (World) (Rev 1).zip",
    "Tetris Attack (USA, Europe) (SGB Enhanced).zip",
    "Wario Land - Super Mario Land 3 (World).zip",
    # 1G1R fixture family (F4 PR-5 selector journey): same game, three dumps —
    # the selector must pick World+Rev 1 over base-rev World and Europe.
    "Kirby's Dream Land (World).zip",
    "Kirby's Dream Land (World) (Rev 1).zip",
    "Kirby's Dream Land (Europe).zip",
]

# archive.org item for the "psx" platform: a Redump-named 3-disc set (F4
# disc-set journey) plus a single-disc title.
IA_PSX_ITEM = "redump-psx-e2e"

PSX_FILES = [
    "Final Fantasy VII (USA) (Disc 1).zip",
    "Final Fantasy VII (USA) (Disc 2).zip",
    "Final Fantasy VII (USA) (Disc 3).zip",
    "Wipeout XL (USA).zip",
    # Second disc set for the selector journey — FF7 is already imported by
    # the PR-4 discset journey, so the enforce scheduler needs a fresh set.
    "Chrono Cross (USA) (Disc 1).zip",
    "Chrono Cross (USA) (Disc 2).zip",
]

# item -> (files, inner ROM extension) the stub serves.
IA_ITEMS = {
    IA_GB_ITEM: (GB_FILES, ".gb"),
    IA_PSX_ITEM: (PSX_FILES, ".bin"),
}

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

# RomM Connect stub: gamarr's notifier logs in (session cookie), walks the
# socket.io long-polling handshake and emits "scan" events; every scan payload
# lands in ROMM_SOCKET["scans"] for tests to read via GET /stub/romm-scans.
ROMM_SESSION_COOKIE = "romm_session=stub-session"
ROMM_SOCKET = {"sessions": {}, "scans": []}

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


def _rom_zip(name: str, ext: str = ".gb") -> bytes:
    """A small but genuine ZIP containing a fake ROM entry. Deterministic bytes
    (zipfile stamps writestr entries with a fixed 1980 date), so the md5/sha1/
    size published in the metadata below stay stable."""
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as z:
        z.writestr(name.replace(".zip", ext), b"GAMARR-E2E-ROM" * 64)
    return buf.getvalue()


def _ia_metadata(item: str) -> bytes:
    """An archive.org /metadata/<item> document: one files[] entry per fixture
    file, carrying real size/md5/sha1 (archive.org encodes size as a string)
    so the driver parses exactly the prod shape."""
    names, ext = IA_ITEMS[item]
    files = []
    for n in names:
        b = _rom_zip(n, ext)
        files.append({
            "name": n,
            "source": "original",
            "format": "ZIP",
            "size": str(len(b)),
            "md5": hashlib.md5(b).hexdigest(),
            "sha1": hashlib.sha1(b).hexdigest(),
        })
    return json.dumps(
        {"server": "stub", "dir": f"/{item}", "files": files}
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
                if name == QBIT_PACK_NAME:
                    fnames = QBIT_PACK_FILES
                else:
                    fnames = [f"{name}.gb"]
                files = []
                for i, fn in enumerate(fnames):
                    (content / fn).write_bytes(QBIT_PAYLOAD)
                    files.append({
                        "index": i,
                        "name": f"{name}/{fn}",
                        "size": len(QBIT_PAYLOAD),
                        "priority": 1,
                        "progress": 1.0,
                    })
                stopped = form.get("stopped", form.get("paused", ["false"]))[0] == "true"
                QBIT_STATE["torrents"][h] = {
                    "name": name,
                    "hash": h,
                    "progress": 1.0,
                    "amount_left": 0,
                    "state": "stoppedUP" if stopped else "uploading",
                    "total_size": len(QBIT_PAYLOAD) * len(fnames),
                    "dlspeed": 0,
                    "eta": 0,
                    "save_path": str(QBIT_STATE["incoming"]),
                    "content_path": str(content),
                    "category": form.get("category", [""])[0],
                    "tags": form.get("tags", [""])[0],
                    "ratio": 0.0,
                    "files": files,
                }
            self._send(200, b"Ok.", "text/plain")
        elif path == "/api/v2/torrents/filePrio":
            form = urllib.parse.parse_qs(body)
            h = form.get("hash", [""])[0].lower()
            ids = [int(i) for i in form.get("id", [""])[0].split("|") if i != ""]
            prio = int(form.get("priority", ["1"])[0])
            QBIT_STATE["file_prio"].append({"hash": h, "ids": ids, "priority": prio})
            t = QBIT_STATE["torrents"].get(h)
            if t:
                for f in t.get("files", []):
                    if f["index"] in ids:
                        f["priority"] = prio
            self._send(200, b"", "text/plain")
        elif path in ("/api/v2/torrents/stop", "/api/v2/torrents/pause"):
            form = urllib.parse.parse_qs(body)
            for h in form.get("hashes", [""])[0].split("|"):
                t = QBIT_STATE["torrents"].get(h.lower())
                if t:
                    t["state"] = "stoppedUP" if t["progress"] >= 1.0 else "stoppedDL"
            self._send(200, b"", "text/plain")
        elif path in ("/api/v2/torrents/start", "/api/v2/torrents/resume"):
            form = urllib.parse.parse_qs(body)
            for h in form.get("hashes", [""])[0].split("|"):
                t = QBIT_STATE["torrents"].get(h.lower())
                if t:
                    t["state"] = "uploading" if t["progress"] >= 1.0 else "downloading"
            self._send(200, b"", "text/plain")
        elif path == "/api/v2/torrents/delete":
            form = urllib.parse.parse_qs(body)
            for h in form.get("hashes", [""])[0].split("|"):
                QBIT_STATE["torrents"].pop(h.lower(), None)
            self._send(200, b"", "text/plain")
        # ── RomM Connect: session login + socket.io polling emits ─────────
        elif path == "/api/login":
            if not self._romm_auth_ok():
                self._send(401, b"unauthorized", "text/plain")
                return
            payload = b"null"
            self.send_response(200)
            self.send_header("Content-Type", "application/json")
            self.send_header("Set-Cookie", ROMM_SESSION_COOKIE + "; Path=/")
            self.send_header("Content-Length", str(len(payload)))
            self.end_headers()
            self.wfile.write(payload)
        elif path == "/ws/socket.io/":
            if not self._romm_session_ok():
                self._send(401, b"no session", "text/plain")
                return
            from urllib.parse import parse_qs, urlparse
            sid = parse_qs(urlparse(self.path).query).get("sid", [""])[0]
            sess = ROMM_SOCKET["sessions"].get(sid)
            if sess is None:
                self._send(400, b"unknown sid", "text/plain")
                return
            if body == "40":
                sess["joined"] = True
            elif body.startswith("42"):
                try:
                    event = json.loads(body[2:])
                    if event and event[0] == "scan":
                        ROMM_SOCKET["scans"].append(event[1])
                except (ValueError, IndexError):
                    pass
            self._send(200, b"ok", "text/plain")
        else:
            self._send(404, b"not found", "text/plain")

    def _romm_session_ok(self) -> bool:
        return ROMM_SESSION_COOKIE in (self.headers.get("Cookie") or "")

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
            files = t.get("files", []) if t else []
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
            hb = {"METADATA_SOURCES": {
                "ANY_SOURCE_ENABLED": True,
                "IGDB_API_ENABLED": True,
                "HASHEOUS_API_ENABLED": True,
                "MOBY_API_ENABLED": False,
            }}
            self._send(200, json.dumps(hb).encode(), "application/json")
        # ── RomM Connect: engine.io long-polling handshake + debug view ───
        elif path == "/ws/socket.io/":
            if not self._romm_session_ok():
                self._send(401, b"no session", "text/plain")
                return
            from urllib.parse import parse_qs, urlparse
            sid = parse_qs(urlparse(self.path).query).get("sid", [""])[0]
            if not sid:
                sid = f"eio-{len(ROMM_SOCKET['sessions'])}"
                ROMM_SOCKET["sessions"][sid] = {"joined": False, "acked": False}
                open_pkt = '0{"sid":"%s","upgrades":[],"pingInterval":25000,"pingTimeout":60000}' % sid
                self._send(200, open_pkt.encode(), "text/plain")
                return
            sess = ROMM_SOCKET["sessions"].get(sid)
            if sess is None:
                self._send(400, b"unknown sid", "text/plain")
            elif sess["joined"] and not sess["acked"]:
                sess["acked"] = True
                self._send(200, ('40{"sid":"ns-%s"}' % sid).encode(), "text/plain")
            else:
                # Quiet connection: throttle the client's listen loop a bit,
                # then answer with an engine.io ping like a real idle server.
                time.sleep(0.2)
                self._send(200, b"2", "text/plain")
        elif path == "/stub/romm-scans":
            self._send(200, json.dumps(ROMM_SOCKET["scans"]).encode(), "application/json")
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
        elif path.startswith("/metadata/") and path.split("/metadata/", 1)[1] in IA_ITEMS:
            self._send(200, _ia_metadata(path.split("/metadata/", 1)[1]), "application/json")
        elif (
            path.startswith("/download/")
            and len(path.split("/", 3)) == 4
            and path.split("/", 3)[2] in IA_ITEMS
        ):
            item = path.split("/", 3)[2]
            names, ext = IA_ITEMS[item]
            name = urllib.parse.unquote(path.split(f"/download/{item}/", 1)[1])
            if name in names:
                self._send(200, _rom_zip(name, ext), "application/zip")
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


def _app_env(stub_server: str, data: Path, port: int) -> dict:
    """Environment for one gamarr instance rooted at data: injected registry
    (archive.org -> stub with live gb/psx items, Vimm -> dead port that
    refuses connections instantly), stub-backed integrations, fast ticks.
    Creates the instance dirs; safe to call again on the same data dir (the
    set-repair journey reboots its instance to trigger the boot sweep)."""
    dead = "http://127.0.0.1:1/"
    registry = {
        "version": 1,
        "archiveorg": {
            "base_url": stub_server,
            "items": {"gb": IA_GB_ITEM, "psx": IA_PSX_ITEM},
        },
        "vimm": {"base_url": dead, "platform_systems": {}},
    }
    reg_path = data / "sources.json"
    reg_path.write_text(json.dumps(registry))

    vault = data / "vault"
    roms = data / "roms"
    incoming = data / "incoming"
    for d in (vault, roms, incoming):
        d.mkdir(exist_ok=True)

    return {
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
        # RomM Connect: fast coalescing so import-triggered scans land while
        # a journey is still watching (defaults are 120s/30s).
        "ROMM_CONNECT_FLUSH_IDLE": "1",
        "ROMM_CONNECT_TICK": "1",
        # F4 selector: enforce mode drives the scheduler in the selector
        # journey; harmless elsewhere (nothing else triggers a scheduler run).
        "SELECTOR_MODE": "enforce",
    }


def _boot_gamarr(gamarr_binary: Path, env: dict, data: Path):
    """Start the binary and wait for /api/health. Returns (proc, log handle);
    the log opens in append mode so a rebooted instance keeps its history."""
    log = open(data / "gamarr.log", "a")
    proc = subprocess.Popen([str(gamarr_binary)], env=env, stdout=log, stderr=log)
    base = f"http://127.0.0.1:{int(env['GAMARR_PORT'])}"
    for _ in range(60):
        try:
            urllib.request.urlopen(f"{base}/api/health", timeout=1)
            return proc, log
        except Exception:
            if proc.poll() is not None:
                log.close()
                raise RuntimeError(
                    "gamarr exited during startup:\n" + (data / "gamarr.log").read_text())
            time.sleep(0.5)
    proc.kill()
    log.close()
    raise RuntimeError("gamarr did not become healthy within 30s")


def _stop_gamarr(proc, log):
    proc.terminate()
    try:
        proc.wait(timeout=5)
    except subprocess.TimeoutExpired:
        proc.kill()
    log.close()


@pytest.fixture(scope="session")
def app(stub_server, gamarr_binary, tmp_path_factory):
    """Boot the session gamarr instance against the stub."""
    data = tmp_path_factory.mktemp("data")
    port = _free_port()
    env = _app_env(stub_server, data, port)
    QBIT_STATE["incoming"] = str(data / "incoming")
    proc, log = _boot_gamarr(gamarr_binary, env, data)

    yield {
        "base": f"http://127.0.0.1:{port}",
        "data": data,
        "roms_dir": data / "roms",
        "vault_dir": data / "vault",
    }

    _stop_gamarr(proc, log)


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
