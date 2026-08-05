import { Link } from 'react-router-dom'
import { Ghost } from 'lucide-react'

export function NotFound() {
  return (
    <div className="flex flex-col items-center justify-center py-24 text-center">
      <Ghost className="mb-4 h-12 w-12 text-slate-700" />
      <h1 className="text-xl font-bold text-white">Page not found</h1>
      <p className="mt-1 text-sm text-slate-500">That route doesn’t exist in RomArr.</p>
      <Link to="/" className="mt-5 rounded-lg bg-accent-600 px-4 py-2 text-sm font-semibold text-white hover:bg-accent-500">
        Back to Library
      </Link>
    </div>
  )
}
