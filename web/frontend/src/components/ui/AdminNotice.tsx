import { ShieldAlert } from 'lucide-react'
import { Card } from './Card'

/** Inline notice for admin-gated screens when the API answers 403. */
export function AdminNotice() {
  return (
    <Card>
      <div className="flex items-center gap-3 text-sm text-slate-400" data-testid="admin-required">
        <ShieldAlert className="h-5 w-5 flex-shrink-0 text-yellow-500" />
        <span>Admin access required — sign in with an admin account to view this page.</span>
      </div>
    </Card>
  )
}
