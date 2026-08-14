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
  file_size?: number
  source?: string
  source_type?: string
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
  label: string
  source_type: string // "torrent" | "ddl"
  enabled: boolean
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

export interface MonitorStatus {
  enabled?: boolean
  provider?: string
  model?: string
  diagnosis?: string
}

export interface AppConfig {
  romm_url?: string
  gamevault_url?: string
  [k: string]: unknown
}

export interface Settings {
  extract_archives?: boolean
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
