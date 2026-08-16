interface Props {
  label?: string
  /** Arr convention: advanced fields carry the orange label accent. */
  advanced?: boolean
  value: string
  onChange: (v: string) => void
  options: { value: string; label: string }[]
  disabled?: boolean
  className?: string
  // Allow data-* passthrough (e.g. data-testid) on this custom component.
  [key: `data-${string}`]: unknown
}

export function Select({ label, advanced, value, onChange, options, disabled, className = '', ...rest }: Props) {
  return (
    <label className="block">
      {label && <span className={`mb-1 block text-xs font-medium ${advanced ? 'text-[#ff902b]' : 'text-slate-400'}`}>{label}</span>}
      <select
        value={value}
        onChange={(e) => onChange(e.target.value)}
        disabled={disabled}
        className={`w-full rounded border border-slate-700 bg-slate-800 px-3 py-1.5 text-sm text-slate-300 focus:border-accent-500 focus:outline-none focus:ring-1 focus:ring-accent-500 disabled:cursor-not-allowed disabled:opacity-60 ${className}`}
        {...rest}
      >
        {options.map((o) => (
          <option key={o.value} value={o.value}>
            {o.label}
          </option>
        ))}
      </select>
    </label>
  )
}
