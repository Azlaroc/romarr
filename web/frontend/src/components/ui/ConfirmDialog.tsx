import type { ReactNode } from 'react'
import { Modal } from './Modal'
import { Button } from './Button'

interface Props {
  open: boolean
  title: string
  message: ReactNode
  confirmLabel?: string
  danger?: boolean
  busy?: boolean
  onConfirm: () => void
  onCancel: () => void
}

export function ConfirmDialog({ open, title, message, confirmLabel = 'Confirm', danger, busy, onConfirm, onCancel }: Props) {
  return (
    <Modal open={open} onClose={onCancel} title={title}>
      <div className="text-sm text-slate-300">{message}</div>
      <div className="mt-4 flex justify-end gap-2 border-t border-slate-800 pt-4">
        <Button variant="secondary" size="sm" onClick={onCancel} data-testid="confirm-cancel">
          Cancel
        </Button>
        <Button
          variant={danger ? 'danger' : 'primary'}
          size="sm"
          disabled={busy}
          onClick={onConfirm}
          className={danger ? 'border border-red-500/40 text-red-400' : ''}
          data-testid="confirm-ok"
        >
          {confirmLabel}
        </Button>
      </div>
    </Modal>
  )
}
