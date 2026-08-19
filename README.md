# Gamarr

[![Build & Test](https://github.com/JeremiahM37/gamarr/actions/workflows/test.yml/badge.svg)](https://github.com/JeremiahM37/gamarr/actions/workflows/test.yml)
[![Release](https://img.shields.io/github/v/release/JeremiahM37/gamarr?include_prereleases)](https://github.com/JeremiahM37/gamarr/releases)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

**The missing *arr for games.** Self-hosted game and ROM search, download, and library manager.

Gamarr searches across all configured indexers (Torznab proxies, direct-download archive listings, web-scrape sources) in parallel for 24 platforms. Results are scored for safety and quality, downloads are managed through your choice of torrent or Usenet client, and files are automatically organized into your game vault and ROM library.

Single ~17MB Go binary, no runtime dependencies — **~9MB RSS idle** in a real homelab[^1], typically 10-30× lower than other self-hosted game library / ROM tools. Comfortable on a Pi or any thermally-constrained mini-PC.

[^1]: Measured on the current main in an LXC on Debian 12 (Mar 2026). Reference: ROMM ≈ 320MB, GameVault backend ≈ 157MB on the same host.

![Gamarr library view — game cards across PC, NES, SNES, GBA and Genesis with platform badges, file sizes, and RomM/GameVault links](docs/screenshot.png)

## Features

### Search and Discovery

- **Pluggable indexer registry** -- driver kinds (Torznab proxy, DDL archive listing, web-scrape) loaded at runtime from an embedded JSON registry; optionally overrideable via `GAMARR_SOURCES_URL` / `GAMARR_SOURCES_PATH`
- **24 gaming platforms** -- PC, Switch, PS1-PS5, PSP, PS Vita, Xbox, Xbox 360, Wii, Wii U, NES, SNES, N64, GameCube, Game Boy, GBA, DS, 3DS, Genesis, Saturn, Dreamcast, Atari 2600
- **Search scoring** -- composite 0-100 score based on title match, platform relevance, seeder count, file size, and safety analysis
- **Safety scoring** -- analyzes file names, sizes, and scene group trust to detect malware, crack-only uploads, and suspicious downloads
- **Duplicate detection** -- search results show an `in_library` flag when a game already exists in your library

### Quality Control

- **Quality profiles** -- rank sources by preference, enable auto-upgrade when a better release appears
- **Release profiles** -- preferred and excluded words to filter and boost search results
- **Blocklist** -- failed or unwanted releases are auto-added, filtered from future results

### Downloads

- **Download clients** -- qBittorrent (torrents), SABnzbd (Usenet/NZB), plus direct-download sources
- **Download monitoring** -- real-time progress tracking with auto-organize on completion
- **Retry and recovery** -- configurable retry attempts with backoff, orphan torrent recovery on startup
- **Archive extraction** -- auto-extract 7z, zip, and rar archives after download

### Library Management

- **SQLite-backed library** with platform tagging, search, and pagination
- **Tags** -- create and assign tags for custom organization
- **Rename on import** -- configurable pattern (e.g., `{title} ({platform}).{ext}`), scene tag cleanup
- **File organization** -- auto-sort ROMs by platform directory, PC games to vault
- **Manual import** -- scan directories and import existing files
- **Import/export** -- JSON and CSV for library and wishlist
- **Backup and restore** -- full database backup with admin-only access

### Wanted

- **Wishlist** -- save wanted games, each with its own quality profile
- **Interactive search** -- pick a title, see every release, choose one by hand; reachable from a wishlist row or a library item, ranked under that title's profile
- **Scheduled searches** -- automatic wishlist searches with configurable interval, auto-download best match

A public request queue is deliberately not part of RomArr, the same way Radarr
leaves that to Overseerr.

### Notifications

- **In-app notifications** -- unread count, mark read, per-event tracking
- **Webhooks** -- Discord and generic webhook support with per-event filtering
- **Event types** -- download complete, download failed, scheduler match

### Integrations

- **RomM** -- link library items to your RomM instance
- **ClamAV** (optional) -- scan downloaded files for malware

### Administration

- **Multi-user auth** -- session-based login with API key support (`X-Api-Key` header)
- **Admin dashboard** -- system overview, user management, connection tests
- **Rate limiting** -- per-category limits (login, search, download, general API)
- **Security headers** -- request size limits, CORS, standard hardening
- **Prometheus metrics** at `/metrics`

### Technical

- **Single static binary** -- ~17 MB on disk, ~9 MB RSS idle, zero CGO, pure-Go SQLite (`modernc.org/sqlite`)
- **Docker-ready** -- minimal Alpine image with p7zip, runs as non-root user
- **Mobile-responsive UI** -- Tailwind CSS, dark theme, platform filters
- **43 automated end-to-end tests**

## Supported Platforms

| Platform | Slug | DDL archive | Torznab |
|----------|------|-------------|---------|
| PC | `pc` | -- | Yes |
| Nintendo Switch | `switch` | -- | Yes |
| PS1 | `psx` | Yes | Yes |
| PS2 | `ps2` | Yes | Yes |
| PS3 | `ps3` | Yes | Yes |
| PS4 | `ps4` | -- | Yes |
| PSP | `psp` | Yes | Yes |
| PS Vita | `psvita` | Yes | Yes |
| Xbox | `xbox` | Yes | Yes |
| Xbox 360 | `xbox360` | Yes | Yes |
| Wii | `wii` | Yes | Yes |
| Wii U | `wiiu` | Yes | Yes |
| NES | `nes` | Yes | Yes |
| SNES | `snes` | Yes | Yes |
| Nintendo 64 | `n64` | Yes | Yes |
| Nintendo DS | `nds` | Yes | Yes |
| Nintendo 3DS | `3ds` | Yes | Yes |
| Game Boy | `gb` | Yes | Yes |
| Game Boy Advance | `gba` | Yes | Yes |
| Sega Genesis | `genesis` | Yes | Yes |
| Sega Saturn | `saturn` | Yes | Yes |
| Dreamcast | `dreamcast` | Yes | Yes |
| GameCube | `gamecube` | Yes | Yes |
| Atari 2600 | `atari2600` | Yes | Yes |

## Quick Start

### Docker (recommended)

```yaml
services:
  gamarr:
    build: .
    ports:
      - "5001:5001"
    volumes:
      - ./data:/data/gamarr
      - /path/to/games/vault:/data/vault
      - /path/to/roms:/data/roms
    environment:
      - PROWLARR_URL=http://prowlarr:9696
      - PROWLARR_API_KEY=your-prowlarr-api-key
      - QB_URL=http://qbittorrent:8080
      - QB_USER=admin
      - QB_PASS=changeme
    restart: unless-stopped
```

```bash
docker compose up -d
```

### Binary

```bash
# Build
go build -o gamarr ./cmd/gamarr/

# Configure
export PROWLARR_URL=http://localhost:9696
export PROWLARR_API_KEY=your-prowlarr-api-key
export QB_URL=http://localhost:8080
# ... set other env vars as needed

# Run
./gamarr
```

Open `http://localhost:5001` in your browser.

## Configuration

Deploy contracts (paths, ports, credentials, client endpoints) are environment
variables. Behavior knobs — pipeline toggles, download handling, the watcher,
wishlist-search/scheduler settings, and the RomM sync/Connect switches — are
editable in the Settings UI and stored in the database: their env variables
below are the **fresh-install defaults**, consulted only until the knob is
first edited in-app (resetting a field in the UI returns it to the env
default).

### Server

| Variable | Default | Description |
|----------|---------|-------------|
| `GAMARR_PORT` | `5001` | HTTP listen port |
| `DATA_DIR` | `/data/gamarr` | Data directory (SQLite DB, settings) |
| `METRICS_ENABLED` | `true` | Enable Prometheus metrics endpoint |
| `AUTH_USERNAME` | | Admin username (enables auth when set) |
| `AUTH_PASSWORD` | | Admin password |

### Sources Registry

The registry is **database-managed**: `GAMARR_SOURCES_PATH`/`GAMARR_SOURCES_URL`
(and the `VIMM_URL`/`ARCHIVEORG_URL` overrides) resolve a seed that imports on
**first boot only**; afterwards edit sources in Settings › Indexers — changes
apply immediately, no restart.

The active indexer list (base URLs, per-platform path mappings) is loaded at startup from, in order: `GAMARR_SOURCES_PATH`, `GAMARR_SOURCES_URL`, or an embedded fallback. Legacy per-source env vars below continue to take precedence over registry values.

| Variable | Default | Description |
|----------|---------|-------------|
| `GAMARR_SOURCES_URL` | | URL of a JSON sources registry; fetched at startup, falls back to embedded default if unreachable |
| `GAMARR_SOURCES_PATH` | | Local path to a sources registry JSON file; takes precedence over URL |
| `MYRIENT_URL` | | Override base URL of the DDL archive-listing source |
| `VIMM_URL` | | Override base URL of the web-scrape source |

### Search Sources

| Variable | Default | Description |
|----------|---------|-------------|
| `PROWLARR_URL` | `http://prowlarr:9696` | Prowlarr URL |
| `PROWLARR_API_KEY` | | Prowlarr API key |
| `PROWLARR_GAME_INDEXERS` | | Comma-separated indexer IDs to restrict searches to (empty = search all configured indexers) |

### Download Clients

| Variable | Default | Description |
|----------|---------|-------------|
| `QB_URL` | `http://qbittorrent:8080` | qBittorrent Web UI URL |
| `QB_USER` | `admin` | qBittorrent username |
| `QB_PASS` | | qBittorrent password |
| `QB_SAVE_PATH` | `/data/incoming/` | Download save path |
| `QB_CATEGORY` | `games` | Torrent category |
| `SABNZBD_URL` | | SABnzbd URL |
| `SABNZBD_API_KEY` | | SABnzbd API key |
| `SABNZBD_CATEGORY` | `games` | NZB download category |

### Library and Organization

| Variable | Default | Description |
|----------|---------|-------------|
| `GAMES_VAULT_PATH` | `/data/vault` | PC game storage directory |
| `GAMES_ROMS_PATH` | `/data/roms` | ROM storage directory |
| `RENAME_ENABLED` | `false` | Rename files on import |
| `RENAME_PATTERN` | `{title} ({platform}).{ext}` | Rename pattern |
| `IGDB_CLIENT_ID` | | Twitch application client id — IGDB is Twitch-owned, and this is what makes game search and cover art work |
| `IGDB_CLIENT_SECRET` | | Twitch application client secret |
| `IGDB_API_BASE` | `https://api.igdb.com/v4` | Override the IGDB API host (a mirror, or a stub in tests) |
| `IGDB_AUTH_BASE` | `https://id.twitch.tv` | Override the token host |
| `ROMM_URL` | | RomM server URL (also the API base for the RomM integration) |
| `ROMM_API_USER` | | RomM API username (read access; enables the RomM integration) |
| `ROMM_API_PASS` | | RomM API password |
| `ROMM_SYNC_ENABLED` | `true` | Let RomM own the ROM library view (requires the RomM API credentials). `GAMES_ROMS_PATH` must be the directory RomM scans as its roms folder |
| `ROMM_SYNC_INTERVAL` | `1800` | Seconds between incremental RomM syncs (a full reconcile runs at startup and daily) |
| `ROMM_EXCLUDE_PLATFORMS` | | Comma-separated RomM fs_slugs to skip when syncing |

### Notifications

| Variable | Default | Description |
|----------|---------|-------------|
| `WEBHOOK_URL` | | Default webhook URL |
| `WEBHOOK_TYPE` | `generic` | Webhook type (`discord`, `generic`) |

### Downloads

| Variable | Default | Description |
|----------|---------|-------------|
| `EXTRACT_ARCHIVES` | `false` | Auto-extract downloaded archives |

### ClamAV (optional)

| Variable | Default | Description |
|----------|---------|-------------|
| `CLAMAV_CONTAINER` | `clamav` | ClamAV Docker container name |
| `CLAMAV_SOCKET` | `/run/clamav/clamd.sock` | ClamAV socket path |

## Architecture

Single static binary, zero CGO dependencies, pure-Go SQLite via `modernc.org/sqlite`.

```
cmd/gamarr/main.go              Entry point
internal/
  config/                        Environment variable configuration
  db/                            SQLite persistence + migrations
  models/                        Core types (games, downloads, notifications)
  api/                           HTTP handlers + chi router + auth + rate limiting
  search/                        Source drivers (Torznab, DDL archive, web-scrape)
  sources/                       Runtime sources registry (embedded defaults + loader)
  download/                      Download manager (qBit, Usenet, DDL)
  sabnzbd/                       SABnzbd client
  safety/                        Safety scoring engine
  scheduler/                     Scheduled wishlist searches
  metadata/                      Metadata-provider seam (no active provider yet)
  organize/                      File organization and rename
  platform/                      Platform definitions, detection, category mapping
  qbit/                          qBittorrent API client
  webhook/                       Discord + generic webhook delivery
web/
  embed.go                       go:embed of the UI into the binary
  index.html                     Single-page web UI markup
  static/js/app.js               UI logic (strict-CSP event delegation)
  static/js/vendor/tailwind.js   Vendored Tailwind runtime (no CDN)
  static/css/app.css             Custom styles
e2e/
  conftest.py                    Hermetic Playwright harness (stubbed services)
  test_user_journey.py           Browser e2e: search, download, library, wishlist
tests/
  e2e_test.py                    43 end-to-end API tests
Dockerfile                       Multi-stage Alpine build
```

## Testing

```bash
# Run tests (requires a running Gamarr instance)
cd tests
python e2e_test.py
```

## License

MIT

## Disclaimer

This software is provided for **educational and personal use only**. Users are responsible for ensuring their use complies with all applicable laws and regulations in their jurisdiction. The developers do not condone or encourage copyright infringement or any illegal activity. This tool does not host, store, or distribute any copyrighted content, and ships with no built-in catalog of indexers -- the list of endpoints to query comes from a user-overridable registry.
