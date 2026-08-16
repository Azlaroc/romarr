// TanStack Query hooks over the RomArr API. Polling (there is no realtime on
// the backend) is expressed with refetchInterval.

import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
  type UseQueryResult,
} from '@tanstack/react-query'
import { api, ApiError, qs } from './client'
import { pickArray, pickNumber } from './unwrap'
import type {
  AppConfig,
  Platform,
  SearchResponse,
  SearchResult,
  LibraryPage,
  DownloadItem,
  WishlistItem,
  SourceInfo,
  SourceHealth,
  DDLSource,
  SourceRegistryRow,
  Webhook,
  Tag,
  Stats,
  ActivityPage,
  Settings,
  SettingsEnv,
  AuthStatus,
  LoginResponse,
  TestResult,
  QualityProfile,
  ReleaseProfile,
  BlocklistItem,
  GameRequest,
  RequestsPage,
  SearchResult as SearchResultType,
  CalendarEntry,
  PlayHistoryEntry,
  PlayHistoryStats,
  AppNotification,
  AdminDashboard,
  SchedulerStatus,
  LibrarySyncStatus,
  NormalizeStatus,
  NormalizePreviewRow,
  SafeUser,
  InviteCode,
  BackupInfo,
  TotpSetupResponse,
} from './types'

export const keys = {
  config: ['config'] as const,
  settings: ['settings'] as const,
  settingsEnv: ['settings-env'] as const,
  platforms: ['platforms'] as const,
  library: (p: LibraryParams) => ['library', p] as const,
  downloads: ['downloads'] as const,
  wishlist: ['wishlist'] as const,
  sources: ['sources'] as const,
  sourceRegistry: ['source-registry'] as const,
  sourcesHealth: ['sources-health'] as const,
  ddlSources: ['ddl-sources'] as const,
  webhooks: ['webhooks'] as const,
  tags: ['tags'] as const,
  stats: ['stats'] as const,
  activity: (page: number) => ['activity', page] as const,
  authStatus: ['auth-status'] as const,
  unread: ['notifications-unread'] as const,
  qualityProfiles: ['quality-profiles'] as const,
  releaseProfiles: ['release-profiles'] as const,
  blocklist: ['blocklist'] as const,
  requests: (status: string) => ['requests', status] as const,
  calendar: ['calendar'] as const,
  calendarRecent: ['calendar-recent'] as const,
  playHistory: ['play-history'] as const,
  playStats: ['play-stats'] as const,
  notifications: ['notifications'] as const,
  dashboard: ['admin-dashboard'] as const,
  scheduler: ['scheduler-status'] as const,
  normalizeStatus: ['normalize-status'] as const,
  normalizeResults: (page: number) => ['normalize-results', page] as const,
  syncStatus: ['sync-status'] as const,
  users: ['users'] as const,
  invites: ['invites'] as const,
  backups: ['backups'] as const,
  totpStatus: ['totp-status'] as const,
}

// ---------- queries ----------

export function useAuthStatus(): UseQueryResult<AuthStatus> {
  return useQuery({
    queryKey: keys.authStatus,
    queryFn: () => api.get<AuthStatus>('/api/auth/status'),
    staleTime: 0,
  })
}

export function useConfig() {
  return useQuery({ queryKey: keys.config, queryFn: () => api.get<AppConfig>('/api/config') })
}

export function usePlatforms() {
  return useQuery({
    queryKey: keys.platforms,
    queryFn: async () => pickArray<Platform>(await api.get('/api/platforms'), 'platforms'),
    staleTime: 5 * 60_000,
  })
}

export interface LibraryParams {
  page: number
  q: string
  platform: string
}

export function useLibrary(params: LibraryParams) {
  return useQuery({
    queryKey: keys.library(params),
    queryFn: () =>
      api.get<LibraryPage>(
        `/api/library${qs({ page: params.page, q: params.q, platform: params.platform })}`,
      ),
    placeholderData: keepPreviousData,
    // No realtime backend — refetch so freshly-imported items appear without a
    // manual reload (page-1 paginated fetch, cheap; only while Library is open).
    refetchInterval: 5000,
  })
}

export function useDownloads(pollMs?: number) {
  return useQuery({
    queryKey: keys.downloads,
    queryFn: async () => pickArray<DownloadItem>(await api.get('/api/downloads'), 'downloads'),
    refetchInterval: pollMs,
  })
}

export function useWishlist() {
  return useQuery({
    queryKey: keys.wishlist,
    queryFn: async () => pickArray<WishlistItem>(await api.get('/api/wishlist'), 'items'),
  })
}

export function useSources() {
  return useQuery({
    queryKey: keys.sources,
    queryFn: async () => pickArray<SourceInfo>(await api.get('/api/sources'), 'sources'),
  })
}

/** Health/circuit snapshots keyed by source name (empty until a source has been used). */
export function useSourcesHealth() {
  return useQuery({
    queryKey: keys.sourcesHealth,
    queryFn: async () =>
      ((await api.get<{ sources?: Record<string, SourceHealth> }>('/api/sources/health'))?.sources ?? {}),
  })
}

export function useResetSource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (name: string) => api.post<{ success: boolean; error?: string }>(`/api/sources/${name}/reset`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.sources })
      qc.invalidateQueries({ queryKey: keys.sourcesHealth })
    },
  })
}

/** Builtin rows come first; custom rows carry stable ids for DELETE. */
export function useDDLSources() {
  return useQuery({
    queryKey: keys.ddlSources,
    queryFn: async () => pickArray<DDLSource>(await api.get('/api/ddl-sources'), 'sources'),
  })
}

export function useAddDDLSource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (src: { name: string; url: string }) => api.post('/api/ddl-sources', src),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.ddlSources }),
  })
}

export function useDeleteDDLSource() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/ddl-sources/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.ddlSources }),
  })
}

/** Built-in source registry rows (vimm, archiveorg) — DB-backed, editable. */
export function useSourceRegistry() {
  return useQuery({
    queryKey: keys.sourceRegistry,
    queryFn: async () => pickArray<SourceRegistryRow>(await api.get('/api/source-registry'), 'sources'),
  })
}

export function useUpdateSourceSpec() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: ({ name, patch }: { name: string; patch: Partial<Pick<SourceRegistryRow, 'enabled' | 'base_url' | 'mapping'>> }) =>
      api.put(`/api/source-registry/${name}`, patch),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.sourceRegistry })
      qc.invalidateQueries({ queryKey: keys.sources })
      qc.invalidateQueries({ queryKey: keys.ddlSources })
    },
  })
}

export function useStats() {
  return useQuery({ queryKey: keys.stats, queryFn: () => api.get<Stats>('/api/stats') })
}

export function useActivity(page = 1) {
  return useQuery({
    queryKey: keys.activity(page),
    queryFn: () => api.get<ActivityPage>(`/api/activity?page=${page}`),
    placeholderData: keepPreviousData,
  })
}

export function useSettings() {
  return useQuery({ queryKey: keys.settings, queryFn: () => api.get<Settings>('/api/settings') })
}

/** Read-only env-derived runtime config (admin) — paths, scheduler, RomM, download handling. */
export function useSettingsEnv() {
  return useQuery({ queryKey: keys.settingsEnv, queryFn: () => api.get<SettingsEnv>('/api/settings/env') })
}

/** Topbar bell — unread notification count, tolerant of the response shape. */
export function useUnreadCount(pollMs = 20_000) {
  return useQuery({
    queryKey: keys.unread,
    queryFn: async () => {
      const data = await api.get<unknown>('/api/notifications/unread')
      return (
        pickNumber(data, 'count', 'unread', 'total') ??
        pickArray(data, 'notifications', 'items').length
      )
    },
    refetchInterval: pollMs,
    retry: false,
  })
}

// ---------- profiles + blocklist ----------

export function useQualityProfiles() {
  return useQuery({
    queryKey: keys.qualityProfiles,
    queryFn: async () => pickArray<QualityProfile>(await api.get('/api/quality-profiles'), 'profiles'),
  })
}

export function useSaveQualityProfile() {
  const qc = useQueryClient()
  return useMutation({
    // PUT is a full-body replace on the backend — always send the complete profile.
    mutationFn: (p: QualityProfile) =>
      p.id ? api.put(`/api/quality-profiles/${p.id}`, p) : api.post('/api/quality-profiles', p),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.qualityProfiles }),
  })
}

export function useDeleteQualityProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/quality-profiles/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.qualityProfiles }),
  })
}

export function useReleaseProfiles() {
  return useQuery({
    queryKey: keys.releaseProfiles,
    queryFn: async () => pickArray<ReleaseProfile>(await api.get('/api/release-profiles'), 'profiles'),
  })
}

export function useSaveReleaseProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (p: ReleaseProfile) =>
      p.id ? api.put(`/api/release-profiles/${p.id}`, p) : api.post('/api/release-profiles', p),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.releaseProfiles }),
  })
}

export function useDeleteReleaseProfile() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/release-profiles/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.releaseProfiles }),
  })
}

export function useBlocklist() {
  return useQuery({
    queryKey: keys.blocklist,
    queryFn: async () => {
      const data = await api.get<unknown>('/api/blocklist')
      return {
        items: pickArray<BlocklistItem>(data, 'items'),
        total: pickNumber(data, 'total') ?? 0,
      }
    },
    // No realtime backend — poll so entries appear without a manual reload.
    refetchInterval: 5000,
  })
}

export function useDeleteBlocklistEntry() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/blocklist/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.blocklist }),
  })
}

export function useClearBlocklist() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.del<{ success: boolean; deleted?: number }>('/api/blocklist/clear'),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.blocklist }),
  })
}

// ---------- mutations ----------

export function useSearch() {
  return useMutation({
    mutationFn: ({ q, platform }: { q: string; platform: string }) =>
      api.get<SearchResponse>(`/api/search${qs({ q, platform })}`),
  })
}

export function useDownloadGame() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (result: SearchResult & { force?: boolean }) =>
      api.post<{ success: boolean; error?: string }>('/api/download', result),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.downloads }),
  })
}

export function useQueueAction() {
  const qc = useQueryClient()
  const invalidate = () => qc.invalidateQueries({ queryKey: keys.downloads })
  return {
    retry: useMutation({ mutationFn: (jobId: string) => api.post(`/api/downloads/${jobId}/retry`), onSuccess: invalidate }),
    removeTorrent: useMutation({ mutationFn: (hash: string) => api.del(`/api/downloads/torrent/${hash}`), onSuccess: invalidate }),
    removeJob: useMutation({ mutationFn: (jobId: string) => api.del(`/api/downloads/${jobId}`), onSuccess: invalidate }),
    clear: useMutation({ mutationFn: () => api.post('/api/downloads/clear'), onSuccess: invalidate }),
    organize: useMutation({
      mutationFn: (v: { hash: string; platform: string; platform_slug: string; is_pc: boolean }) =>
        api.post(`/api/downloads/organize/${v.hash}`, { platform: v.platform, platform_slug: v.platform_slug, is_pc: v.is_pc }),
      onSuccess: invalidate,
    }),
  }
}

export function useDeleteLibraryItem() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/library/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['library'] })
      qc.invalidateQueries({ queryKey: keys.stats })
    },
  })
}

export function useAddWishlist() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { title: string; platform: string; platform_slug: string }) => api.post('/api/wishlist', v),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.wishlist }),
  })
}

export function useDeleteWishlist() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/wishlist/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.wishlist }),
  })
}

export function useSaveSetting() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (patch: Partial<Settings>) => api.put('/api/settings', patch),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.settings }),
  })
}

export function useTestConnection() {
  return useMutation({
    mutationFn: (service: string) => api.post<TestResult>(`/api/test/${service}`),
  })
}

// ---------- webhooks & tags (Settings > Connect / Tags) ----------

export function useWebhooks() {
  return useQuery({
    queryKey: keys.webhooks,
    queryFn: async () => pickArray<Webhook>(await api.get('/api/webhooks'), 'webhooks'),
  })
}

export function useAddWebhook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (hook: { name: string; url: string; type: string; events: string }) =>
      api.post('/api/webhooks', hook),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.webhooks }),
  })
}

export function useDeleteWebhook() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/webhooks/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.webhooks }),
  })
}

/** Fires a real HTTP request at the stored webhook's URL; {success:false,error} on delivery failure. */
export function useTestWebhook() {
  return useMutation({
    mutationFn: (req: { id: number } | { url: string; type?: string }) =>
      api.post<{ success: boolean; error?: string }>('/api/webhooks/test', req),
  })
}

export function useTags() {
  return useQuery({
    queryKey: keys.tags,
    queryFn: async () => pickArray<Tag>(await api.get('/api/tags'), 'tags'),
  })
}

export function useAddTag() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (tag: { name: string; color?: string }) => api.post('/api/tags', tag),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tags }),
  })
}

export function useDeleteTag() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/tags/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.tags }),
  })
}

// ---------- requests ----------

export function useRequests(status = '') {
  return useQuery({
    queryKey: keys.requests(status),
    queryFn: () => api.get<RequestsPage>(`/api/requests${qs({ status })}`),
    placeholderData: keepPreviousData,
  })
}

export function useCreateRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { title: string; platform: string; platform_slug: string; notes?: string }) =>
      api.post<{ success: boolean; request: GameRequest }>('/api/requests', v),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['requests'] }),
  })
}

export function useUpdateRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: string; status?: string; notes?: string; admin_notes?: string }) =>
      api.patch<{ success: boolean; request: GameRequest }>(`/api/requests/${v.id}`, {
        status: v.status,
        notes: v.notes,
        admin_notes: v.admin_notes,
      }),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['requests'] }),
  })
}

export function useDeleteRequest() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) => api.del(`/api/requests/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['requests'] }),
  })
}

/** POST /search — returns tier-sorted results; render in order, never re-sort. */
export function useRequestSearch() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: string) =>
      api.post<{ success: boolean; results: SearchResultType[]; request: GameRequest }>(
        `/api/requests/${id}/search`,
      ),
    onSuccess: () => qc.invalidateQueries({ queryKey: ['requests'] }),
  })
}

export function useRequestDownload() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: string; body: Record<string, unknown> }) =>
      api.post<{ success: boolean; job_id: string }>(`/api/requests/${v.id}/download`, v.body),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: ['requests'] })
      qc.invalidateQueries({ queryKey: keys.downloads })
    },
  })
}

// ---------- calendar ----------

export function useCalendar() {
  return useQuery({
    queryKey: keys.calendar,
    queryFn: async () => pickArray<CalendarEntry>(await api.get('/api/calendar'), 'entries'),
  })
}

export function useCalendarRecent() {
  return useQuery({
    queryKey: keys.calendarRecent,
    queryFn: async () => pickArray<CalendarEntry>(await api.get('/api/calendar/recent'), 'entries'),
  })
}

// ---------- play log ----------

export function usePlayHistory() {
  return useQuery({
    queryKey: keys.playHistory,
    queryFn: async () => {
      const data = await api.get<unknown>('/api/history')
      return {
        entries: pickArray<PlayHistoryEntry>(data, 'entries'),
        total: pickNumber(data, 'total') ?? 0,
      }
    },
  })
}

export function usePlayStats() {
  return useQuery({
    queryKey: keys.playStats,
    queryFn: async () =>
      (await api.get<{ success: boolean; stats: PlayHistoryStats }>('/api/history/stats')).stats,
  })
}

export function useAddPlayHistory() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { game_title: string; platform?: string; platform_slug?: string; rating?: number }) =>
      api.post<{ success: boolean; id: number }>('/api/history', v),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.playHistory })
      qc.invalidateQueries({ queryKey: keys.playStats })
    },
  })
}

export function useUpdatePlayHistory() {
  const qc = useQueryClient()
  return useMutation({
    // PATCH takes a sparse field map — only send what changed.
    mutationFn: (v: { id: number; fields: Record<string, unknown> }) =>
      api.patch(`/api/history/${v.id}`, v.fields),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.playHistory })
      qc.invalidateQueries({ queryKey: keys.playStats })
    },
  })
}

export function useDeletePlayHistory() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del(`/api/history/${id}`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.playHistory })
      qc.invalidateQueries({ queryKey: keys.playStats })
    },
  })
}

// ---------- notifications (bell panel) ----------

export function useNotifications(enabled: boolean) {
  return useQuery({
    queryKey: keys.notifications,
    queryFn: async () =>
      pickArray<AppNotification>(await api.get('/api/notifications'), 'notifications'),
    enabled, // fetched only while the panel is open
  })
}

export function useMarkNotificationRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.post(`/api/notifications/${id}/read`),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.notifications })
      qc.invalidateQueries({ queryKey: keys.unread })
    },
  })
}

export function useMarkAllNotificationsRead() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post('/api/notifications/read-all'),
    onSuccess: () => {
      qc.invalidateQueries({ queryKey: keys.notifications })
      qc.invalidateQueries({ queryKey: keys.unread })
    },
  })
}

// ---------- auth mutations ----------

export function useLogin() {
  return useMutation({
    mutationFn: (v: { username: string; password: string }) => api.post<LoginResponse>('/api/login', v),
  })
}

export function useLoginTotp() {
  return useMutation({
    mutationFn: (v: { session_pending: string; code: string }) => api.post<LoginResponse>('/api/login/totp', v),
  })
}

export function useRegister() {
  return useMutation({
    mutationFn: (v: { username: string; password: string }) => api.post<LoginResponse>('/api/register', v),
  })
}

export function useLogout() {
  return useMutation({ mutationFn: () => api.post('/api/logout') })
}

// ---------- system section (PR-G) ----------

export function useDashboard() {
  return useQuery({
    queryKey: keys.dashboard,
    queryFn: () => api.get<AdminDashboard>('/api/admin/dashboard'),
    retry: false,
  })
}

export function useSchedulerStatus() {
  return useQuery({
    queryKey: keys.scheduler,
    queryFn: () => api.get<SchedulerStatus>('/api/scheduler/status'),
    refetchInterval: 15_000,
  })
}

export function useRunScheduler() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<{ success: boolean; message?: string }>('/api/scheduler/run'),
    // Fire-and-forget on the backend — give the cycle a beat before refetching.
    onSuccess: () => setTimeout(() => qc.invalidateQueries({ queryKey: keys.scheduler }), 2000),
  })
}

export function useSyncStatus() {
  return useQuery({
    queryKey: keys.syncStatus,
    queryFn: () => api.get<LibrarySyncStatus>('/api/library/sync/status'),
  })
}

export function useTriggerSync() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (full: boolean) =>
      api.post<{ success: boolean; error?: string; message?: string }>(`/api/library/sync${full ? '?full=true' : ''}`),
    onSuccess: () => setTimeout(() => qc.invalidateQueries({ queryKey: keys.syncStatus }), 2000),
  })
}

export function useNormalizeStatus() {
  return useQuery({
    queryKey: keys.normalizeStatus,
    queryFn: () => api.get<NormalizeStatus>('/api/library/normalize/status'),
    refetchInterval: (q) => (q.state.data?.running ? 2000 : false),
  })
}

export function useNormalizeResults(page: number, enabled: boolean) {
  return useQuery({
    queryKey: keys.normalizeResults(page),
    queryFn: () =>
      api.get<{ success: boolean; items: NormalizePreviewRow[]; total: number }>(
        `/api/library/normalize/preview/results?page=${page}&page_size=100`,
      ),
    enabled,
  })
}

export function useNormalizePreview() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (platformSlug: string) =>
      api.post<{ success: boolean; error?: string }>('/api/library/normalize/preview', { platform_slug: platformSlug }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.normalizeStatus }),
  })
}

export function useNormalizeApply() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (excludeIds: number[]) =>
      api.post<{ success: boolean; error?: string }>('/api/library/normalize/apply', { exclude_ids: excludeIds }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.normalizeStatus }),
  })
}

export function useNormalizeStop() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post<{ success: boolean }>('/api/library/normalize/stop'),
    onSuccess: () => setTimeout(() => qc.invalidateQueries({ queryKey: keys.normalizeStatus }), 500),
  })
}

export function useUsers() {
  return useQuery({
    queryKey: keys.users,
    // In open mode there are zero users and the backend serializes null — coerce.
    queryFn: async () => pickArray<SafeUser>(await api.get('/api/users'), 'users'),
    retry: false,
  })
}

export function useUpdateUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { id: number; role?: string; password?: string }) =>
      api.patch<{ success: boolean }>(`/api/users/${v.id}`, { role: v.role, password: v.password }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.users }),
  })
}

export function useDeleteUser() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del<{ success: boolean }>(`/api/users/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.users }),
  })
}

export function useInvites() {
  return useQuery({
    queryKey: keys.invites,
    queryFn: async () => pickArray<InviteCode>(await api.get('/api/invites'), 'invites'),
    retry: false,
  })
}

export function useCreateInvite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (v: { role?: string; max_uses?: number; expiry_seconds?: number }) =>
      api.post<{ success: boolean; id: number; code: string; role: string; max_uses: number }>('/api/invites', v),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.invites }),
  })
}

export function useDeleteInvite() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (id: number) => api.del<{ success: boolean }>(`/api/invites/${id}`),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.invites }),
  })
}

export function useBackups() {
  return useQuery({
    queryKey: keys.backups,
    queryFn: async () => pickArray<BackupInfo>(await api.get('/api/backup/list'), 'backups'),
    retry: false,
  })
}

export function useRestoreBackup() {
  const qc = useQueryClient()
  return useMutation({
    // POST /api/restore takes the raw ZIP bytes as the request body (not multipart).
    mutationFn: async (file: File) => {
      const res = await fetch('/api/restore', { method: 'POST', body: file, credentials: 'include' })
      const data = (await res.json().catch(() => ({}))) as { success?: boolean; restored?: string[]; error?: string }
      if (!res.ok) throw new ApiError(res.status, data.error ?? `${res.status} restore failed`)
      return data
    },
    onSuccess: () => qc.invalidateQueries(),
  })
}

// TOTP self-service — all routes 401 without a real session user (open mode).
export function useTotpStatus() {
  return useQuery({
    queryKey: keys.totpStatus,
    queryFn: () => api.get<{ enabled: boolean }>('/api/totp/status'),
    retry: false,
  })
}

export function useTotpSetup() {
  return useMutation({ mutationFn: () => api.post<TotpSetupResponse>('/api/totp/setup') })
}

export function useTotpVerify() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => api.post<{ success: boolean }>('/api/totp/verify', { code }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.totpStatus }),
  })
}

export function useTotpDisable() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: (code: string) => api.post<{ success: boolean }>('/api/totp/disable', { code }),
    onSuccess: () => qc.invalidateQueries({ queryKey: keys.totpStatus }),
  })
}
