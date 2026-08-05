import { Loader2 } from 'lucide-react'

export function Spinner({ label, className = '' }: { label?: string; className?: string }) {
  return (
    <div className={`flex items-center gap-2 text-slate-400 ${className}`}>
      <Loader2 className="h-5 w-5 animate-spin text-accent-400" />
      {label && <span className="text-sm">{label}</span>}
    </div>
  )
}
