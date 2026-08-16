/**
 * The arr-style Show Advanced toggle, rendered in a settings page's header
 * actions. Advanced fields use the arr advanced-orange accent (#ff902b).
 */
export function ShowAdvancedButton({ show, onChange }: { show: boolean; onChange: (v: boolean) => void }) {
  return (
    <button
      type="button"
      onClick={() => onChange(!show)}
      data-testid="show-advanced"
      className={`rounded border px-3 py-1.5 text-xs font-medium transition-colors ${
        show ? 'border-[#ff902b]/50 text-[#ff902b]' : 'border-slate-700 text-slate-400 hover:text-slate-200'
      }`}
    >
      {show ? 'Hide Advanced' : 'Show Advanced'}
    </button>
  )
}
