import {ReactNode} from 'react'
import {EmptyState} from './ui'

export type Column<T> = {
  key: string
  header: ReactNode
  align?: 'left' | 'right'
  /** mono renders the cell in the data face (monospace, slightly larger). */
  mono?: boolean
  /** pin marks the tinted identity column that anchors each row. */
  pin?: boolean
  width?: string
  render: (row: T) => ReactNode
}

/**
 * DataTable: the one table treatment — hairline header band, hover-lit rows,
 * a tinted pinned identity column, and a designed empty state so a table with
 * no rows never looks broken. Presentational only; sorting/merging stays with
 * the caller.
 */
export function DataTable<T>({columns, rows, rowKey, empty, dense = false, stickyHeader = false, className = ''}: {
  columns: Column<T>[]
  rows: T[]
  rowKey: (r: T) => string | number
  empty: {icon?: ReactNode; title: string; hint?: ReactNode; action?: ReactNode}
  dense?: boolean
  stickyHeader?: boolean
  className?: string
}) {
  if (rows.length === 0) {
    return (
      <div className={`px-4 pb-4 ${className}`}>
        <EmptyState icon={empty.icon} title={empty.title} hint={empty.hint} action={empty.action} />
      </div>
    )
  }
  const cellPad = dense ? 'px-4 py-1.5' : 'px-4 py-2.5'
  return (
    <div className={`overflow-x-auto ${className}`}>
      <table className="w-full text-sm">
        <thead>
          <tr className="border-y border-[var(--border)] text-left text-xs uppercase tracking-wide text-[var(--text-3)]">
            {columns.map(c => (
              <th
                key={c.key}
                className={`${stickyHeader ? 'sticky top-0 z-10 bg-[var(--panel)]' : ''} px-4 py-2 font-medium ${c.align === 'right' ? 'text-right' : ''}`}
                style={c.width ? {width: c.width} : undefined}
              >{c.header}</th>
            ))}
          </tr>
        </thead>
        <tbody>
          {rows.map(r => (
            <tr key={rowKey(r)} className="border-b border-[var(--border)] transition-colors duration-200 last:border-0 hover:bg-[var(--panel-2)]/50">
              {columns.map(c => (
                <td
                  key={c.key}
                  className={`${cellPad} ${c.align === 'right' ? 'text-right tabular-nums' : ''} ${c.mono ? 'font-mono text-[13px]' : ''} ${c.pin ? 'bg-[var(--panel-2)]/40' : ''}`}
                >{c.render(r)}</td>
              ))}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  )
}
