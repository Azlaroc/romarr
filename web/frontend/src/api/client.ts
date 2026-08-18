// Thin fetch wrapper for the RomArr API.
//   - credentials:'include' so the HttpOnly gamarr_session cookie flows.
//   - dev-only X-Api-Key (VITE_DEV_API_KEY) for the Vite-origin dev server.
//   - a global 401 hook so an expired/absent session bounces to the login gate.
//   - errors surface the backend's {success:false,error} string.

export class ApiError extends Error {
  status: number
  /** Parsed response body, when the backend sent structured details (e.g. the 409 duplicate_hash payload). */
  body: unknown
  constructor(status: number, message: string, body?: unknown) {
    super(message)
    this.status = status
    this.body = body
    this.name = 'ApiError'
  }
}

let onUnauthorized: (() => void) | null = null
/** AuthProvider registers this to react to 401s from any request. */
export function setUnauthorizedHandler(fn: (() => void) | null) {
  onUnauthorized = fn
}

const devApiKey = import.meta.env.VITE_DEV_API_KEY

function buildHeaders(extra?: HeadersInit): Headers {
  const h = new Headers(extra)
  if (devApiKey) h.set('X-Api-Key', devApiKey)
  return h
}

async function parse(res: Response): Promise<unknown> {
  const text = await res.text()
  if (!text) return null
  try {
    return JSON.parse(text)
  } catch {
    return text
  }
}

async function request<T>(method: string, path: string, body?: unknown): Promise<T> {
  const headers = buildHeaders()
  let payload: BodyInit | undefined
  if (body !== undefined) {
    headers.set('Content-Type', 'application/json')
    payload = JSON.stringify(body)
  }

  const res = await fetch(path, {
    method,
    headers,
    body: payload,
    credentials: 'include',
  })

  // Never fire the global handler for the status probe itself — the gate reads
  // it directly and a 401 there is meaningful, not a session-expiry event.
  // Same for the TOTP routes: they are session-user-only and 401 by design in
  // open mode while the rest of the app runs as anonymous admin — bouncing to
  // the auth gate would hijack the whole shell. The Security card handles
  // their 401s locally (isUnauthorized).
  if (res.status === 401 && path !== '/api/auth/status' && !path.startsWith('/api/totp/')) {
    onUnauthorized?.()
  }

  const data = await parse(res)

  if (!res.ok) {
    const msg =
      (data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : null) || `${res.status} ${res.statusText}`
    throw new ApiError(res.status, msg, data)
  }

  return data as T
}

/**
 * Multipart POST. Deliberately does not set Content-Type — the browser has to
 * add the boundary. The DAT upload endpoint accepts multipart only: raw bodies
 * are pre-rejected at 1MB by the request-size middleware, and a catalog pack is
 * far larger than that.
 */
async function requestForm<T>(path: string, form: FormData): Promise<T> {
  const res = await fetch(path, {
    method: 'POST',
    headers: buildHeaders(),
    body: form,
    credentials: 'include',
  })
  if (res.status === 401) onUnauthorized?.()
  const data = await parse(res)
  if (!res.ok) {
    const msg =
      (data && typeof data === 'object' && 'error' in data && typeof (data as { error: unknown }).error === 'string'
        ? (data as { error: string }).error
        : null) || `${res.status} ${res.statusText}`
    throw new ApiError(res.status, msg, data)
  }
  return data as T
}

export const api = {
  get: <T>(path: string) => request<T>('GET', path),
  postForm: <T>(path: string, form: FormData) => requestForm<T>(path, form),
  post: <T>(path: string, body?: unknown) => request<T>('POST', path, body),
  put: <T>(path: string, body?: unknown) => request<T>('PUT', path, body),
  patch: <T>(path: string, body?: unknown) => request<T>('PATCH', path, body),
  del: <T>(path: string, body?: unknown) => request<T>('DELETE', path, body),
}

/** encodeURIComponent shorthand for query params. */
export const qs = (params: Record<string, string | number | undefined>): string => {
  const p = new URLSearchParams()
  for (const [k, v] of Object.entries(params)) {
    if (v !== undefined && v !== '') p.set(k, String(v))
  }
  const s = p.toString()
  return s ? `?${s}` : ''
}

/** True when the error is an admin-gated 403 from the API. */
export function isForbidden(e: unknown): boolean {
  return e instanceof ApiError && e.status === 403
}

/** True when the error is a 401 (no session user — e.g. TOTP routes in open mode). */
export function isUnauthorized(e: unknown): boolean {
  return e instanceof ApiError && e.status === 401
}
