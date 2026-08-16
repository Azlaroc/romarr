// Types for the RomArr (Gamarr fork) REST API. The backend has no universal
// envelope; collections are wrapped under a per-resource key (see unwrap.ts)
// and fields are loosely typed, so most are optional here on purpose.

export interface Platform {
  id: string // slug (or "all" sentinel from /api/platforms)
  name: string
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
  added_at?: string
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

export type RequestStatus =
  | 'pending'
  | 'approved'
  | 'searching'
  | 'downloading'
  | 'completed'
  | 'failed'
  | 'cancelled'

export interface GameRequest {
  id: string // request IDs are strings, not ints
  user_id: string
  title: string
  platform: string
  platform_slug: string
  status: RequestStatus | string
  notes?: string
  admin_notes?: string
  cover_url?: string
  year?: string
  genre?: string
  retry_count: number
  created_at: string // RFC3339 (Go time.Time) — render substrings only
  updated_at: string
}

export interface RequestsPage {
  requests: GameRequest[]
  total: number
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
