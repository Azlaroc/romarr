import { useState } from 'react'

const KEY = 'romarr.showAdvanced'

/**
 * Show-Advanced toggle state (arr convention: advanced fields hide behind a
 * per-user toggle and render with the orange advanced accent). Shared across
 * settings pages via localStorage; each page reads it independently.
 */
export function useShowAdvanced(): [boolean, (v: boolean) => void] {
  const [show, setShow] = useState(() => {
    try {
      return localStorage.getItem(KEY) === '1'
    } catch {
      return false
    }
  })
  const set = (v: boolean) => {
    try {
      localStorage.setItem(KEY, v ? '1' : '0')
    } catch {
      // Storage unavailable (private mode): state stays per-mount.
    }
    setShow(v)
  }
  return [show, set]
}
