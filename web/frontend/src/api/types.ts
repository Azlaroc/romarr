// Types for the RomArr (Gamarr fork) REST API. The backend has no universal
// envelope; collections are wrapped under a per-resource key (see unwrap.ts)
// and fields are loosely typed, so most are optional here on purpose.

export interface Platform {
  id: string // slug (or "all" sentinel from /api/platforms)
  name: string
}

// A registry row: the canonical vocabulary for one platform. Served by
// /api/platforms?full=1 and /api/platforms/{slug}. Identity fields (slug,
// IGDB, RomM fs_slug, categories) are read-only — they are what a platform
// IS; the editable fields below are how RomArr treats it.
export interface PlatformRow {
  slug: string
  display_name: string
  igdb_slug: string
  igdb_id: number
  romm_fs_slug: string
  prowlarr_categories?: number[] | null
  torznab_category: string
  media_class: string // carts | discs | arcade | computer | pc
  converts_to_chd: boolean
  /** On-disk renames refused: non-hashable lanes + arcade (identity, read-only). */
  rename_frozen: boolean
  acquisition_enabled: boolean
  /** Monitors the platform's whole 1G1R set: its gaps become wanted work. */
  collection_mode: boolean
  is_system: boolean
  default_profile_id: number
  updated_at?: string
  dat_authority?: string
  dat_code?: string
}

export interface SearchResult {
  title: string
  platform?: string
  platform_slug?: string
  is_pc?: boolean
  indexer?: string
  source_type?: string // "ddl" | "torrent"
  download_protocol?: string // "nzb" | "torrent"
  seeders?: number
  leechers?: number
  size_human?: string
  safety_score?: number
  safety_warnings?: string[]
  in_library?: boolean
  // The whole object is POSTed back to /api/download, so keep unknown fields.
  [k: string]: unknown
}

export interface SearchResponse {
  results: SearchResult[]
  search_time_ms?: number
  sources?: string[]
}

export interface LibraryItem {
  id: number
  title: string
  platform?: string
  platform_slug?: string
  is_pc?: boolean
  file_path?: string
  file_size?: number
  source?: string
  source_type?: string
}

export interface NormalizeCollision {
  with_library_id?: number
  with_name: string
  verdict: 'byte-identical' | 'different-bytes' | 'unknown'
}

export interface NormalizePreviewRow {
  library_id: number
  platform_slug: string
  old_path: string
  old_name: string
  new_name?: string
  name_source?: 'dat' | 'playmatch'
  status: 'rename' | 'renamed' | 'noop' | 'skip' | 'review'
  reason?: string
  collision?: NormalizeCollision
}

export interface NormalizeStatus {
  enabled: boolean
  running?: boolean
  phase?: string
  scope?: string
  total?: number
  done?: number
  planned?: number
  renamed?: number
  skipped?: number
  collisions?: number
  reviews?: number
  dat_misses?: number
  source_dat?: number
  source_playmatch?: number
  errors?: number
  last_error?: string
  started_at?: string | null
  finished_at?: string | null
  resume_note?: string
  [k: string]: unknown
}

export interface LibraryPage {
  items: LibraryItem[]
  total: number
  page: number
  total_pages: number
}

export interface DownloadItem {
  title: string
  status: string
  progress?: number | null
  eta?: number
  hash?: string
  job_id?: string
  platform?: string
  size?: string
  speed?: string
  detail?: string
  error?: string
  // Disc-set membership (absent on non-set jobs and pure torrents).
  disc_set_id?: string
  disc_index?: number
  disc_total?: number
}

export interface WishlistItem {
  id: number
  title: string
  platform?: string
  platform_slug?: string
  /** Per-title profile override; 0 means "whatever the platform defaults to". */
  profile_id?: number
  added_at?: string
}

/** What POST /api/wishlist answers. materialized_profile is present only when
 *  the add created a platform's default profile — announced once, not on
 *  every subsequent add. */
export interface WishlistAddResponse {
  id: number
  success?: boolean
  materialized_profile?: { id: number; name: string }
}

export interface SourceInfo {
  name?: string
  label: string
  source_type: string // "torrent" | "ddl"
  enabled: boolean
  health?: SourceHealth
}

/** Per-source circuit/health snapshot from GET /api/sources/health (keyed by source name). */
export interface SourceHealth {
  name: string
  search_ok: number
  search_fail: number
  download_ok: number
  download_fail: number
  last_error: string
  last_error_kind?: string
  last_error_at?: number
  last_success_at: number
  score: number
  circuit_open: boolean
  circuit_retry_in_sec: number
}

/** Row from GET /api/ddl-sources — the builtin row comes first and cannot be
 *  deleted; custom rows carry stable ids for DELETE /api/ddl-sources/{id}. */
export interface DDLSource {
  id?: number
  name: string
  url: string
  type: string
  builtin?: boolean
  enabled?: boolean
  platforms?: string[]
}

/** Row from GET /api/source-registry — the built-in driver specs. */
export interface SourceRegistryRow {
  name: string
  label: string
  builtin: boolean
  enabled: boolean
  base_url: string
  mapping: Record<string, string>
  active: boolean
}

export interface Stats {
  library_total?: number
  total_jobs?: number
  platforms?: Record<string, number>
}

export interface ActivityEntry {
  id?: number
  event_type: string
  title: string
  timestamp?: string
  detail?: string
  job_id?: string
}

/** GET /api/activity envelope — server page size is fixed at 50. */
export interface ActivityPage {
  entries: ActivityEntry[]
  total: number
  page: number
}

/** Per-integration status block in GET /api/config. */
export interface ServiceConfig {
  configured?: boolean
  url?: string
}

export interface AppConfig {
  romm_url?: string
  prowlarr?: ServiceConfig
  qbittorrent?: ServiceConfig
  sabnzbd?: ServiceConfig
  romm?: ServiceConfig
  [k: string]: unknown
}

export interface Settings {
  extract_archives?: boolean
  normalize_roms?: boolean
  normalize_online_fallback?: boolean
  convert_roms?: boolean
  remove_torrent_after_import?: boolean
  seed_janitor_enabled?: boolean
  scheduler_auto_download?: boolean
  scheduler_min_score?: number
  watcher_enabled?: boolean
  watcher_interval_seconds?: number
  scheduler_enabled?: boolean
  scheduler_interval_hours?: number
  selector_mode?: string
  selector_set_timeout_hours?: number
  romm_sync_enabled?: boolean
  romm_sync_interval_seconds?: number
  romm_connect_enabled?: boolean
  romm_exclude_platforms?: string
  dat_auto_refresh_enabled?: boolean
  dat_refresh_interval_days?: number
}

/** GET /api/settings/env — read-only, boot-time env config (admin): deploy
 * contracts only; everything else is editable via /api/settings. */
export interface SettingsEnv {
  paths: {
    roms_path: string
    vault_path: string
    roms_free_bytes: number
    vault_free_bytes: number
    platform_dir_count: number
  }
  converto: { available: boolean; version: string }
}

/** Row from GET /api/webhooks (envelope {success, webhooks}). */
export interface Webhook {
  id: number
  name: string
  url: string
  type: string // "discord" | "generic"
  enabled: boolean
  events: string // comma-separated event names, or "*" for all
  created_at: string
}

/** Row from GET /api/tags (envelope {success, tags}). */
export interface Tag {
  id: number
  name: string
  color: string
}

export interface AuthStatus {
  has_users: boolean
  authenticated: boolean
  oidc_enabled: boolean
  oidc_provider?: string
  totp_enabled?: boolean
  username?: string
  role?: string
  user_id?: number
}

export interface LoginResponse {
  success: boolean
  token?: string
  username?: string
  role?: string
  needs_totp?: boolean
  session_pending?: string
  used_backup?: boolean
  error?: string
}

export interface TestResult {
  success: boolean
  error?: string
  message?: string
}

// Quality profiles are the one full-body-replace surface: PUT zeroes omitted
// fields server-side, so every field is required here and the editors always
// round-trip the complete object (including the reserved upgrade fields).
export interface QualityProfile {
  /** A template is cloned for a new platform, never used directly. */
  is_template?: boolean
  template_class?: string
  id: number
  name: string
  platform_slug: string // "" = global
  is_default: boolean
  region_priority: string[]
  format_preference: string[]
  prefer_1g1r: boolean
  allow_proto: boolean
  allow_demo: boolean
  allow_bios: boolean
  source_ranking: string[]
  preferred_size_min: number // bytes, 0 = platform default
  preferred_size_max: number // bytes, 0 = platform default
  upgrade_allowed: boolean // reserved — stored but not read by the selector
  cutoff_source: string // reserved — stored but not read by the selector
}

export interface PreferredWord {
  word: string
  score: number
}

export interface ReleaseProfile {
  id: number
  name: string
  must_contain: string[]
  must_not_contain: string[]
  preferred: PreferredWord[]
  enabled: boolean
}

export interface BlocklistItem {
  id: number
  title: string
  source: string
  download_url: string
  info_hash: string
  reason: string
  created_at: string // SQLite datetime text ("2026-08-14 03:09:00"), NOT RFC3339
}

/** GET /api/calendar[.../recent] entry (empty until a metadata provider is wired). */
export interface CalendarEntry {
  id: number
  name: string
  release_date: string // "YYYY-MM-DD" — render verbatim
  platforms?: string[]
  background_image?: string
  rating?: number
  on_wishlist?: boolean
}

export interface PlayHistoryEntry {
  id: number
  user_id?: string
  game_title: string
  platform?: string
  platform_slug?: string
  started_at?: string // RFC3339 — render substrings only
  finished_at?: string
  rating?: number
  notes?: string
  hours_played?: number
}

export interface PlayHistoryStats {
  games_this_month?: number
  games_this_year?: number
  games_total?: number
  avg_rating?: number
  total_hours?: number
  by_platform?: Record<string, number>
}

/** models.Notification — name avoids clashing with the DOM Notification type. */
export interface AppNotification {
  id: number
  user_id?: string
  type?: string
  title: string
  message?: string
  read: boolean
  created_at?: string // RFC3339 (Go time.Time)
}

// ---------- system section (PR-G) ----------

/** Sanitized user row from GET /api/users (admin). */
export interface SafeUser {
  id: number
  username: string
  role: string
  created_at: string // RFC3339
  last_login?: string
}

export interface InviteCode {
  id: number
  code: string
  created_by?: number
  role: string
  max_uses: number
  uses: number
  created_at?: string
  expires_at?: number // unix seconds
}

export interface AdminDashboard {
  library_stats?: Record<string, number>
  library_total?: number
  active_downloads?: number
  sources_health?: { name: string; label: string; status: string }[]
  total_users?: number
  system?: { version?: string; uptime?: string; go_version?: string }
}

/** GET /api/scheduler/status — degraded nil-scheduler shape is {enabled:false, error}. */
export interface SchedulerStatus {
  enabled: boolean
  error?: string
  interval_hours?: number
  auto_download?: boolean
  min_score?: number
  selector_mode?: string
  running?: boolean
  last_run?: string // RFC3339; zero value = "0001-01-01T00:00:00Z"
  last_results?: number
  auto_downloads?: number
}

export interface LibrarySyncStatus {
  enabled?: boolean
  running?: boolean
  last_sync?: string
  last_error?: string
  [k: string]: unknown
}

export interface BackupInfo {
  name: string
  filename: string
  size: number
  created_at: string // RFC3339
}

export interface TotpSetupResponse {
  success: boolean
  secret: string
  url: string
  backup_codes: string[]
}

// ---------- DAT catalogs (Settings > Metadata) ----------

/** A catalog authority: No-Intro, Redump, MAME. `kind` doubles as the profile
 *  template class (carts / discs / arcade). */
export interface DatAuthority {
  name: string
  label: string
  kind: string
  fetch_driver: string
  fetch_base: string
  enabled: boolean
  pinned_version?: string
  last_refresh?: string
  last_status?: string
  last_error?: string
}

/** One platform's assignment to an authority. `dat_code` is driver-specific:
 *  a catalog filename for the libretro mirror, a short code for Redump. */
export interface DatPlatformAssignment {
  platform_slug: string
  authority: string
  dat_code: string
  enabled: boolean
}

/** Authorities and their assignments arrive together — edited as one screen. */
export interface DatAuthoritiesResponse {
  authorities: DatAuthority[]
  platforms: DatPlatformAssignment[]
}

export interface DatPlatformResult {
  platform: string
  status: string
  version?: string
  games?: number
  roms?: number
  added?: number
  removed?: number
  changed?: number
  error?: string
}

export interface DatSkippedMember {
  member: string
  reason: string
}

export interface DatStatus {
  enabled: boolean
  running: boolean
  interval_days?: number
  loop_running?: boolean
  phase?: string
  authority?: string
  total?: number
  done?: number
  results?: DatPlatformResult[] | null
  last_error?: string
  started_at?: string | null
  finished_at?: string | null
}

export interface DatRefreshResponse {
  success: boolean
  message?: string
  authority?: string
  error?: string
}

export interface DatUploadResponse {
  success: boolean
  authority?: string
  imported?: DatPlatformResult[]
  skipped?: DatSkippedMember[]
  error?: string
}

export interface DatAuthorityPatchResponse {
  success: boolean
  authority?: DatAuthority
  warnings?: string[]
}

/** Owned and known are independent counts; the server renders `summary` so the
 *  UI cannot accidentally present them as a completion figure. */
export interface DatCoverageRow {
  platform_slug: string
  authority?: string
  owned: number
  known: number
  summary: string
  snapshot_version?: string
  last_refresh?: string
}

export interface DatCoverageResponse {
  coverage: DatCoverageRow[]
  note: string
}

// ---------- per-platform size definitions (Settings > Quality Definitions) ----------

/** Sizes are bytes and 0 means unlimited on that end. The stored number is the
 *  enforcing number: any compression allowance was folded in when it was
 *  written, so what this screen shows is what rejects a candidate. */
export interface SizeDefinition {
  platform_slug: string
  min_size: number
  max_size: number
  source: string
  snapshot_version?: string
  updated_at?: string
  /** Whether a reset has an active catalog snapshot to re-derive from. */
  has_catalog: boolean
}

export interface SizeDefinitionsResponse {
  definitions: SizeDefinition[]
}

/** A metadata authority as GET /api/metadata/providers reports it. */
export interface MetadataProvider {
  name: string
  label: string
  configured: boolean
  role: string
  credentials_env: string[]
}

/** One game from GET /api/metadata/search — an identity, not a release. */
export interface MetadataGame {
  provider_id: number
  name: string
  slug?: string
  summary?: string
  cover_url?: string
  release_year?: number
  platforms?: string[]
  unmapped_platforms?: string[]
}

/** One catalogued dump from GET /api/dat/games. */
export interface DatGame {
  id: number
  name: string
  bare_title?: string
  region?: string
  languages?: string
  revision?: number
  clone_of?: string
  flags?: string
  total_size: number
}

/** One gap: what a platform's 1G1R set wants that the library does not have. */
export interface CollectionTarget {
  id: number
  platform_slug: string
  set_key: string
  title: string
  dump_name?: string
  status: string // wanted | grabbed | unavailable
  attempts: number
  last_attempt?: string
  last_reason?: string
  created_at?: string
}

export interface CollectionTargetsResponse {
  targets: CollectionTarget[]
  total: number
  page: number
  page_size: number
  counts: Record<string, number>
  platforms: string[]
  fill_per_cycle: number
}

export interface CollectionSyncResult {
  platform: string
  added: number
  removed: number
  counts: { groups: number; owned: number; gaps: number; out: number; surplus: number }
}

export interface PruneStatus {
  configured: boolean
  running?: boolean
  phase?: string
  scope?: string
  include_out?: boolean
  total?: number
  done?: number
  archived?: number
  skipped?: number
  errors?: number
  last_error?: string
  counts?: Record<string, number>
  uncatalogued?: number
  archive_root?: string
  started_at?: string
  finished_at?: string
  [k: string]: unknown
}

export interface HashfillStatus {
  configured: boolean
  running?: boolean
  phase?: string
  scope?: string
  dry_run?: boolean
  force?: boolean
  total?: number
  done?: number
  hashed?: number
  stripped?: number
  skipped?: number
  errors?: number
  bytes_hashed?: number
  last_error?: string
  counts?: Record<string, number>
  /** Rows still missing a hash, per platform — the size of the job. */
  pending?: Record<string, number>
  pending_all?: number
  started_at?: string
  finished_at?: string
  [k: string]: unknown
}

export interface HashfillRow {
  library_id: number
  platform_slug: string
  path: string
  name: string
  size?: number
  status: string // hashed | skip | error
  reason?: string
  md5?: string
  /** The header-stripped hash, present only when a container header was found. */
  unh_md5?: string
  header?: string
}

export interface PrunePreviewRow {
  library_id: number
  platform_slug: string
  path: string
  name: string
  size?: number
  title?: string
  keeper?: string
  matched_by?: string
  verdict: string // archive | review | excluded-group | uncatalogued
  status: string // planned | archived | skip | reported
  reason?: string
  archived_to?: string
}
