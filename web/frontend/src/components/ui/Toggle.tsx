interface Props {
  checked: boolean
  onChange: (checked: boolean) => void
  label: string
  hint?: string
  disabled?: boolean
  /** Arr convention: advanced fields carry the orange label accent. */
  advanced?: boolean
  // Allow data-* passthrough (e.g. data-testid) on this custom component.
  [key: `data-${string}`]: unknown
}

/** Labelled checkbox toggle, styled like the PR-A Settings option row. */
export function Toggle({ checked, onChange, label, hint, disabled, advanced, ...rest }: Props) {
  return (
    <label className={`flex items-start gap-3 ${disabled ? 'cursor-not-allowed opacity-60' : 'cursor-pointer'}`}>
      <input
        type="checkbox"
        checked={checked}
        onChange={(e) => onChange(e.target.checked)}
        disabled={disabled}
        className="mt-0.5 h-5 w-5 rounded-sm accent-accent-600"
        {...rest}
      />
      <span className="min-w-0">
        <span className={`block text-sm font-medium ${advanced ? 'text-[#ff902b]' : 'text-slate-200'}`}>{label}</span>
        {hint && <span className="mt-0.5 block text-xs text-slate-500">{hint}</span>}
      </span>
    </label>
  )
}
