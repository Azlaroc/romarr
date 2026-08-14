import { useState } from 'react'
import { Copy, Trash2, UserPlus } from 'lucide-react'
import {
  useCreateInvite,
  useDeleteInvite,
  useDeleteUser,
  useInvites,
  useUpdateUser,
  useUsers,
} from '../../api/queries'
import { isForbidden } from '../../api/client'
import type { SafeUser } from '../../api/types'
import { PageHeader } from '../../components/layout/PageHeader'
import { AdminNotice } from '../../components/ui/AdminNotice'
import { Badge } from '../../components/ui/Badge'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { Select } from '../../components/ui/Select'
import { useToast } from '../../components/ui/Toast'

export function Users() {
  const { data: users, error } = useUsers()
  const update = useUpdateUser()
  const delUser = useDeleteUser()
  const { data: invites = [] } = useInvites()
  const createInvite = useCreateInvite()
  const delInvite = useDeleteInvite()
  const { toast } = useToast()

  const [deleteTarget, setDeleteTarget] = useState<SafeUser | null>(null)
  const [inviteRole, setInviteRole] = useState('user')
  const [lastCode, setLastCode] = useState('')

  if (isForbidden(error)) {
    return (
      <>
        <PageHeader title="System" subtitle="Users" />
        <AdminNotice />
      </>
    )
  }

  const setRole = async (u: SafeUser, role: string) => {
    try {
      await update.mutateAsync({ id: u.id, role })
      toast(`${u.username} is now ${role}`, 'success')
    } catch {
      toast('Failed to update role', 'error')
    }
  }

  const confirmDelete = async () => {
    if (!deleteTarget) return
    try {
      await delUser.mutateAsync(deleteTarget.id)
      toast('User deleted', 'success')
    } catch (e) {
      toast(e instanceof Error ? e.message : 'Failed to delete user', 'error')
    } finally {
      setDeleteTarget(null)
    }
  }

  const mintInvite = async () => {
    try {
      const res = await createInvite.mutateAsync({ role: inviteRole })
      setLastCode(res.code)
      toast('Invite created', 'success')
    } catch {
      toast('Failed to create invite', 'error')
    }
  }

  const copy = (text: string) => {
    void navigator.clipboard?.writeText(text).then(
      () => toast('Copied', 'success'),
      () => toast('Copy failed — select it manually', 'error'),
    )
  }

  return (
    <>
      <PageHeader title="System" subtitle="Users" />
      <div className="space-y-6">
        <Card title="Users">
          {/* Container stays mounted; open mode has zero users. */}
          <div className="space-y-2" data-testid="users-table">
            {(users ?? []).map((u) => (
              <div key={u.id} className="flex items-center gap-3 rounded-lg bg-slate-800 p-3" data-testid={`user-row-${u.id}`}>
                <div className="min-w-0 flex-1">
                  <div className="flex items-center gap-2">
                    <span className="truncate text-sm font-medium text-white">{u.username}</span>
                    <Badge color={u.role === 'admin' ? 'accent' : 'slate'}>{u.role}</Badge>
                  </div>
                  <div className="mt-0.5 text-xs text-slate-500">
                    Created {u.created_at.split('T')[0]}
                    {u.last_login ? ` · last login ${u.last_login.split('T')[0]}` : ''}
                  </div>
                </div>
                <Select
                  value={u.role}
                  onChange={(role) => setRole(u, role)}
                  options={[
                    { value: 'user', label: 'user' },
                    { value: 'admin', label: 'admin' },
                  ]}
                  data-testid={`user-role-${u.id}`}
                />
                <Button size="sm" variant="danger" onClick={() => setDeleteTarget(u)} data-testid={`user-delete-${u.id}`}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
          </div>
          {(users ?? []).length === 0 && (
            <p className="text-sm text-slate-500">
              No user accounts — the app runs in open mode until the first account is registered.
            </p>
          )}
        </Card>

        <Card
          title="Invites"
          action={
            <div className="flex items-center gap-2">
              <Select
                value={inviteRole}
                onChange={setInviteRole}
                options={[
                  { value: 'user', label: 'invite: user' },
                  { value: 'admin', label: 'invite: admin' },
                ]}
                data-testid="invite-role"
              />
              <Button size="sm" variant="secondary" onClick={mintInvite} disabled={createInvite.isPending} data-testid="invite-create">
                <UserPlus className="h-3.5 w-3.5" /> New invite
              </Button>
            </div>
          }
        >
          {lastCode && (
            <div className="mb-3 flex items-center gap-2 rounded-lg border border-accent-600/40 bg-accent-600/10 p-3">
              <code className="flex-1 break-all text-sm text-accent-fg" data-testid="invite-code">{lastCode}</code>
              <Button size="sm" variant="ghost" onClick={() => copy(lastCode)}>
                <Copy className="h-3.5 w-3.5" />
              </Button>
            </div>
          )}
          <div className="space-y-2" data-testid="invites-list">
            {invites.map((inv) => (
              <div key={inv.id} className="flex items-center gap-3 rounded-lg bg-slate-800 p-3">
                <code className="min-w-0 flex-1 truncate text-xs text-slate-300">{inv.code}</code>
                <Badge color={inv.role === 'admin' ? 'accent' : 'slate'}>{inv.role}</Badge>
                <span className="text-xs text-slate-500">
                  {inv.uses}/{inv.max_uses} used
                </span>
                <Button size="sm" variant="danger" onClick={() => delInvite.mutate(inv.id)} data-testid={`invite-delete-${inv.id}`}>
                  <Trash2 className="h-3.5 w-3.5" />
                </Button>
              </div>
            ))}
          </div>
          {invites.length === 0 && <p className="text-sm text-slate-500">No open invites.</p>}
        </Card>
      </div>

      <ConfirmDialog
        open={!!deleteTarget}
        title="Delete user"
        message={`Delete “${deleteTarget?.username}”? Their sessions end immediately.`}
        confirmLabel="Delete"
        danger
        busy={delUser.isPending}
        onConfirm={confirmDelete}
        onCancel={() => setDeleteTarget(null)}
      />
    </>
  )
}
