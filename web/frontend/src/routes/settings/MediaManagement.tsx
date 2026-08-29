import { Link } from 'react-router-dom'
import { usePlatformRegistry, useSaveSetting, useSettings, useSettingsEnv } from '../../api/queries'
import { isForbidden } from '../../api/client'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Card } from '../../components/ui/Card'
import { Toggle } from '../../components/ui/Toggle'
import { useToast } from '../../components/ui/Toast'

const PIPELINE_TOGGLES = [
  {
    key: 'extract_archives',
    testId: 'setting-extract',
    label: 'Extract archives after download',
    help: 'Auto-extract RAR/ZIP/7z in ROM downloads',
  },
  {
    key: 'normalize_roms',
    testId: 'setting-normalize',
    label: 'Normalize ROM names at import',
    help: 'DAT-canonical rename (No-Intro/Redump) + .m3u playlists for disc sets',
  },
  {
    key: 'normalize_online_fallback',
    testId: 'setting-online-fallback',
    label: 'Online naming fallback (Playmatch)',
    help: 'Consult the online Playmatch engine when the local DAT snapshot has no match — its proposals land as review, never auto-applied',
  },
  {
    key: 'convert_roms',
    testId: 'setting-convert',
    label: 'Convert disc images to CHD',
    help: 'Disc systems only; verify-before-replace, originals kept on mismatch',
  },
] as const

const NAMING_AUTHORITIES = [
  { lane: 'Cartridges & handhelds', authority: 'No-Intro', note: 'Verified-dump names: Title (Region) (Languages) (Rev N)' },
  { lane: 'Optical media', authority: 'Redump', note: 'Disc-preservation names for PSX/PS2/PSP/DC/GC/Wii/Saturn dumps' },
  { lane: 'Arcade', authority: 'MAME DAT', note: 'Filenames are machine identifiers the emulator resolves — never renamed' },
  { lane: 'Switch & forwarders', authority: 'Shop-native', note: 'Title-ID-tagged shop naming — excluded from rename passes' },
]

function formatBytes(n: number | undefined): string {
  if (!n || n <= 0) return '—'
  const units = ['B', 'KB', 'MB', 'GB', 'TB']
  let v = n
  let u = 0
  while (v >= 1024 && u < units.length - 1) {
    v /= 1024
    u++
  }
  return `${v.toFixed(v >= 100 || u === 0 ? 0 : 1)} ${units[u]}`
}

export function MediaManagement() {
  const { data: settings, error: settingsError } = useSettings()
  const { data: env, error: envError } = useSettingsEnv()
  // The CHD list used to be a copy of the backend's map, kept in sync by
  // hand. It comes from the platform registry now, so the screen cannot drift
  // from what actually converts.
  const { data: platformRows } = usePlatformRegistry()
  const chdPlatforms = (platformRows ?? []).filter((p) => p.converts_to_chd).map((p) => p.slug)
  const save = useSaveSetting()
  const { toast } = useToast()

  const forbidden = isForbidden(settingsError) || isForbidden(envError)

  const toggle = async (key: string, checked: boolean) => {
    try {
      await save.mutateAsync({ [key]: checked })
      toast('Settings saved', 'success')
    } catch {
      toast('Failed to save', 'error')
    }
  }

  return (
    <>
      <PageHeader title="Settings" subtitle="Media Management" />
      <div className="space-y-6" data-testid="mm-cards">
        {forbidden && <AdminNotice />}

        {!forbidden && (
          <Card title="Importing">
            <div className="space-y-3" data-testid="mm-importing">
              {PIPELINE_TOGGLES.map((t) => (
                <Toggle
                  key={t.key}
                  checked={!!settings?.[t.key]}
                  onChange={(checked) => toggle(t.key, checked)}
                  label={t.label}
                  hint={t.help}
                  data-testid={t.testId}
                />
              ))}
            </div>
          </Card>
        )}

        <Card title="ROM Naming">
          <div className="space-y-3" data-testid="mm-naming">
            <p className="text-xs text-slate-500">
              Naming follows the preservation standard for each platform lane. Names are applied only on an exact
              content-hash match against the DAT (Playmatch lookup) — there is no freeform rename pattern.
            </p>
            <div className="space-y-2">
              {NAMING_AUTHORITIES.map((row) => (
                <div key={row.lane} className="flex items-start justify-between gap-4 rounded bg-slate-800 p-3">
                  <div className="min-w-0">
                    <div className="text-sm text-white">{row.lane}</div>
                    <div className="mt-0.5 text-xs text-slate-500">{row.note}</div>
                  </div>
                  <span className="shrink-0 text-sm font-medium text-accent-fg">{row.authority}</span>
                </div>
              ))}
            </div>
            <Link to="/library/rename" className="inline-block text-sm text-accent-fg hover:underline" data-testid="mm-rename-link">
              Rename existing library files
            </Link>
          </div>
        </Card>

        <Card title="Format Conversion">
          <div className="space-y-2" data-testid="mm-conversion">
            <p className="text-sm text-slate-300">
              Disc images convert to CHD after import on: {chdPlatforms.length ? chdPlatforms.join(', ') : '—'}
            </p>
            <p className="text-xs text-slate-500">
              Converted files are verified before the source is removed; on any verification failure the original is
              kept.
            </p>
          </div>
        </Card>

        {!forbidden && (
          <Card title="Root Folders">
            <div className="space-y-2" data-testid="mm-root-folders">
              <div className="flex items-center justify-between rounded bg-slate-800 p-3">
                <div className="min-w-0">
                  <div className="truncate text-sm text-white" data-testid="mm-roms-path">{env?.paths.roms_path ?? '…'}</div>
                  <div className="mt-0.5 text-xs text-slate-500">
                    ROM library{env ? ` · ${env.paths.platform_dir_count} platform folders` : ''}
                  </div>
                </div>
                <span className="shrink-0 text-sm text-slate-300">{formatBytes(env?.paths.roms_free_bytes)} free</span>
              </div>
              <div className="flex items-center justify-between rounded bg-slate-800 p-3">
                <div className="min-w-0">
                  <div className="truncate text-sm text-white" data-testid="mm-vault-path">{env?.paths.vault_path ?? '…'}</div>
                  <div className="mt-0.5 text-xs text-slate-500">PC game vault</div>
                </div>
                <span className="shrink-0 text-sm text-slate-300">{formatBytes(env?.paths.vault_free_bytes)} free</span>
              </div>
              <p className="text-xs text-slate-500">Paths are set by the container environment and require a restart to change.</p>
            </div>
          </Card>
        )}
      </div>
    </>
  )
}
