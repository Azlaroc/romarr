// TanStack Query hooks over the RomArr API. Polling (there is no realtime on
// the backend) is expressed with refetchInterval.

import {
  useQuery,
  useMutation,
  useQueryClient,
  keepPreviousData,
  type UseQueryResult,
} from '@tanstack/react-query'
import { api, qs } from './client'
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
  Stats,
  ActivityPage,
  MonitorStatus,
  Settings,
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
} from './types'

export const keys = {
  config: ['config'] as const,
  platforms: ['platforms'] as const,
  library: (p: LibraryParams) => ['library', p] as const,
  downloads: ['downloads'] as const,
  wishlist: ['wishlist'] as const,
  sources: ['sources'] as const,
  stats: ['stats'] as const,
  activity: (page: number) => ['activity', page] as const,
  monitor: ['monitor'] as const,
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

export function useMonitor() {
  return useQuery({ queryKey: keys.monitor, queryFn: () => api.get<MonitorStatus>('/api/monitor/status') })
}

export function useSettings() {
  return useQuery({ queryKey: ['settings'], queryFn: () => api.get<Settings>('/api/settings') })
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
    mutationFn: (result: SearchResult) => api.post<{ success: boolean; error?: string }>('/api/download', result),
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
    onSuccess: () => qc.invalidateQueries({ queryKey: ['settings'] }),
  })
}

export function useTestConnection() {
  return useMutation({
    mutationFn: (service: string) => api.post<TestResult>(`/api/test/${service}`),
  })
}

export function useTriggerAnalysis() {
  const qc = useQueryClient()
  return useMutation({
    mutationFn: () => api.post('/api/monitor/analyze'),
    onSuccess: () => setTimeout(() => qc.invalidateQueries({ queryKey: keys.monitor }), 600),
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
