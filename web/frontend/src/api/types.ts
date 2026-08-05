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
  event_type: string
  title: string
  timestamp?: string
  detail?: string
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
