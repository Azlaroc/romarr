// The backend wraps collections under a per-resource key and is inconsistent
// about it. These helpers normalize a response to the array/value we want,
// tolerating both the wrapped and bare shapes.

export function pickArray<T>(data: unknown, ...keys: string[]): T[] {
  if (Array.isArray(data)) return data as T[]
  if (data && typeof data === 'object') {
    for (const key of keys) {
      const v = (data as Record<string, unknown>)[key]
      if (Array.isArray(v)) return v as T[]
    }
  }
  return []
}

export function pickNumber(data: unknown, ...keys: string[]): number | undefined {
  if (data && typeof data === 'object') {
    for (const key of keys) {
      const v = (data as Record<string, unknown>)[key]
      if (typeof v === 'number') return v
    }
  }
  return undefined
}
