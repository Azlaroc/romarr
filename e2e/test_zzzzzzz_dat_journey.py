"""DAT catalog journey — fetch, snapshot, coverage, and the hand-upload
escape hatch, through the real server.

Covers both transports (the libretro mirror's bare DAT and Redump's zip),
both refresh outcomes an operator sees (ok, and the multipart upload that
replaces a fetched snapshot), and the coverage contract: two independent
counts, never a completion percentage.

The authorities are pointed at the stub through the API rather than an env
var — repointing an authority is a data edit, which is the property that
makes losing a provider survivable, so the test exercises it for real.

Ordering: pytest runs the [chromium]-parametrized browser tests as one block
BEFORE the unparametrized API-only journeys, and this file sorts last within
that second block. It writes only DAT tables plus two activity rows, which
nothing else reads. It leaves both authorities pointing at the stub on
purpose: the refresh cadence ships off, so nothing consults fetch_base again
unless a later test asks it to. Do not add a "cleanup" that races the
session-scoped app fixture.
"""
import io
import json
import time
import urllib.request
import uuid
import zipfile

from conftest import DAT_LIBRETRO_PREFIX, DAT_REDUMP_PREFIX, DAT_GAMES

SLOW_S = 45


def _req(base: str, path: str, method: str = "GET", payload: dict | None = None) -> dict:
    data = json.dumps(payload).encode() if payload is not None else None
    req = urllib.request.Request(
        base + path, data=data, method=method,
        headers={"Content-Type": "application/json"} if data else {},
    )
    with urllib.request.urlopen(req, timeout=15) as r:
        return json.loads(r.read() or b"{}")


def _multipart(base: str, path: str, filename: str, body: bytes,
               content_type: str | None = None) -> tuple[int, dict]:
    """POST a file the way the endpoint requires. Hand-rolled: the e2e suite
    has no requests dependency, and the point of the endpoint is that a raw
    body would be rejected at 1MB."""
    boundary = "----romarr" + uuid.uuid4().hex
    payload = b"".join([
        f'--{boundary}\r\nContent-Disposition: form-data; name="file"; filename="{filename}"\r\n'
        f"Content-Type: application/octet-stream\r\n\r\n".encode(),
        body,
        f"\r\n--{boundary}--\r\n".encode(),
    ])
    ctype = content_type or f"multipart/form-data; boundary={boundary}"
    req = urllib.request.Request(base + path, data=payload, method="POST",
                                 headers={"Content-Type": ctype})
    try:
        with urllib.request.urlopen(req, timeout=30) as r:
            return r.status, json.loads(r.read() or b"{}")
    except urllib.error.HTTPError as e:
        return e.code, json.loads(e.read() or b"{}")


def _wait(cond, timeout=SLOW_S, step=0.25, msg="condition"):
    deadline = time.time() + timeout
    while time.time() < deadline:
        result = cond()
        if result:
            return result
        time.sleep(step)
    raise AssertionError(f"timed out waiting for {msg}")


def _authority(base: str, name: str) -> dict:
    for a in _req(base, "/api/dat/authorities")["authorities"]:
        if a["name"] == name:
            return a
    raise AssertionError(f"authority {name} missing")


def _coverage(base: str, slug: str) -> dict:
    for row in _req(base, "/api/dat/coverage")["coverage"]:
        if row["platform_slug"] == slug:
            return row
    return {}


def _refresh(base: str, name: str):
    started = _req(base, f"/api/dat/authorities/{name}/refresh", "POST", {})
    assert started.get("success"), started
    _wait(lambda: _req(base, "/api/dat/status").get("running") is False,
          msg=f"{name} refresh to finish")


def _dat(name: str, games: int, version: str) -> bytes:
    rows = [f'clrmamepro (\n\tname "{name}"\n\tversion "{version}"\n)\n']
    for i in range(1, games + 1):
        rows.append(
            f'game (\n\tname "{name} Hand {i} (USA)"\n'
            f'\trom ( name "{name} Hand {i} (USA).rom" size {2048 * i} '
            f'crc {i:08x} sha1 {i * 977:040x} )\n)\n'
        )
    return "\n".join(rows).encode()


def test_dat_catalog_journey(app, stub_server):
    base = app["base"]

    # ── the mirror path: a bare clrmamepro DAT per cart platform ─────────
    patched = _req(base, "/api/dat/authorities/no-intro", "PATCH",
                   {"fetch_base": stub_server + DAT_LIBRETRO_PREFIX})
    assert patched["authority"]["fetch_base"].endswith(DAT_LIBRETRO_PREFIX)

    _refresh(base, "no-intro")
    auth = _authority(base, "no-intro")
    assert auth["last_status"] == "ok", auth
    assert auth["last_refresh"], "last_refresh not stamped"
    assert not auth.get("last_error"), auth

    gb = _coverage(base, "gb")
    assert gb["known"] == DAT_GAMES, gb
    # Two independent counts: coverage does not match owned files against
    # catalog entries, so it may never read as completion.
    assert "owned" in gb and "known" in gb
    assert "owned" in gb["summary"] and "known" in gb["summary"]

    raw = app["data"] / "gamarr" / "dat" / "no-intro" / "gb.dat"
    assert raw.is_file(), "the fetched catalog was not kept on disk"

    # A re-fetch of identical bytes must not churn a snapshot.
    before = _coverage(base, "gb")
    _refresh(base, "no-intro")
    assert _coverage(base, "gb")["known"] == before["known"]

    # ── the disc path: Redump serves a zip, which the app must unwrap ────
    _req(base, "/api/dat/authorities/redump", "PATCH",
         {"fetch_base": stub_server + DAT_REDUMP_PREFIX})
    _refresh(base, "redump")
    assert _authority(base, "redump")["last_status"] == "ok"
    psx = _coverage(base, "psx")
    assert psx["known"] == DAT_GAMES, psx

    # ── the escape hatch: a hand upload replaces a fetched snapshot ──────
    status, resp = _multipart(base, "/api/dat/authorities/no-intro/upload?platform=gb",
                              "Nintendo - Game Boy.dat", _dat("Nintendo - Game Boy", 7, "2026.08.17-hand"))
    assert status == 200 and resp.get("success"), (status, resp)
    assert resp["imported"][0]["games"] == 7, resp
    assert _coverage(base, "gb")["known"] == 7, "the upload did not replace the fetched catalog"

    # ── a pack fans out, and reports what it passed over ────────────────
    buf = io.BytesIO()
    with zipfile.ZipFile(buf, "w", zipfile.ZIP_DEFLATED) as zf:
        zf.writestr("Nintendo - Game Boy Color (20260801-063015).dat",
                    _dat("Nintendo - Game Boy Color", 5, "2026.08.17-pack"))
        zf.writestr("Commodore - Amiga (20260801-063015).dat",
                    _dat("Commodore - Amiga", 9, "2026.08.17-pack"))
        zf.writestr("readme.txt", b"daily pack")
    status, resp = _multipart(base, "/api/dat/authorities/no-intro/upload",
                              "no-intro-daily.zip", buf.getvalue())
    assert status == 200 and resp.get("success"), (status, resp)
    assert [r["platform"] for r in resp["imported"]] == ["gbc"], resp
    assert any("Amiga" in s["member"] for s in resp["skipped"]), resp
    assert _coverage(base, "gbc")["known"] == 5

    # ── the transport is not negotiable ─────────────────────────────────
    status, _ = _multipart(base, "/api/dat/authorities/no-intro/upload?platform=gb",
                           "x.dat", _dat("x", 2, "v"), content_type="application/octet-stream")
    assert status == 415, f"a non-multipart upload must be refused, got {status}"

    # A hand-fed authority says so instead of pretending to fetch.
    try:
        _req(base, "/api/dat/authorities/mame/refresh", "POST", {})
        raise AssertionError("refreshing an upload-only authority must fail")
    except urllib.error.HTTPError as e:
        assert e.code == 400, e.code
