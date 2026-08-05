/// <reference types="vite/client" />

interface ImportMetaEnv {
  /** Dev-only: injected as X-Api-Key on API calls when the backend has auth on. */
  readonly VITE_DEV_API_KEY?: string
}

interface ImportMeta {
  readonly env: ImportMetaEnv
}
