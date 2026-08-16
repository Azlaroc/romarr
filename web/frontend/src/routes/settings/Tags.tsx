import { useState } from 'react'
import { useAddTag, useDeleteTag, useTags } from '../../api/queries'
import { PageHeader } from '../../components/layout/PageHeader'
import { Button } from '../../components/ui/Button'
import { Card } from '../../components/ui/Card'
import { ConfirmDialog } from '../../components/ui/ConfirmDialog'
import { Input } from '../../components/ui/Input'
import { useToast } from '../../components/ui/Toast'

export function Tags() {
  const { data: tags = [] } = useTags()
  const add = useAddTag()
  const del = useDeleteTag()
  const { toast } = useToast()

  const [name, setName] = useState('')
  const [deleting, setDeleting] = useState<{ id: number; name: string } | null>(null)

  const submit = async () => {
    try {
      await add.mutateAsync({ name: name.trim() })
      toast('Tag created', 'success')
      setName('')
    } catch {
      toast('Failed to create tag', 'error')
    }
  }

  const confirmDelete = async () => {
    if (!deleting) return
    try {
      await del.mutateAsync(deleting.id)
      toast('Tag deleted', 'success')
    } catch {
      toast('Failed to delete tag', 'error')
    } finally {
      setDeleting(null)
    }
  }

  return (
    <>
      <PageHeader title="Settings" subtitle="Tags" />
      <div className="space-y-6">
        <Card title="Tags">
          <div className="space-y-3">
            <p className="text-xs text-slate-500">
              Tags label library items; apply or remove them from the Library screen. Deleting a tag removes it from
              every item that carries it.
            </p>
            <div className="space-y-2" data-testid="tag-list">
              {tags.map((t) => (
                <div key={t.id} className="flex items-center justify-between gap-3 rounded-lg bg-slate-800 p-3" data-testid={`tag-row-${t.id}`}>
                  <div className="flex min-w-0 items-center gap-3">
                    <span className="h-3 w-3 shrink-0 rounded-full" style={{ backgroundColor: t.color || '#6366f1' }} />
                    <span className="truncate text-sm text-white">{t.name}</span>
                  </div>
                  <Button size="sm" variant="danger" onClick={() => setDeleting({ id: t.id, name: t.name })} data-testid={`tag-delete-${t.id}`}>
                    Delete
                  </Button>
                </div>
              ))}
            </div>
            <div className="flex items-end gap-2">
              <Input label="Name" value={name} onChange={(e) => setName(e.target.value)} placeholder="family" className="max-w-48" data-testid="tag-name" />
              <Button size="sm" onClick={submit} disabled={add.isPending || !name.trim()} data-testid="tag-add">
                Add tag
              </Button>
            </div>
          </div>

          <ConfirmDialog
            open={deleting !== null}
            title="Delete tag"
            message={<p>Delete “{deleting?.name}”? It is removed from every library item that carries it.</p>}
            confirmLabel="Delete"
            danger
            busy={del.isPending}
            onConfirm={confirmDelete}
            onCancel={() => setDeleting(null)}
          />
        </Card>
      </div>
    </>
  )
}
