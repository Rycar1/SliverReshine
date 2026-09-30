import { useEffect, useMemo, useRef, useState, type ReactNode } from 'react'
import { useTranslation } from 'react-i18next'

export interface Column<T> {
  key: string
  label: string
  render?: (row: T) => ReactNode
  sortable?: boolean
  sortValue?: (row: T) => string | number
  className?: string
  width?: string
}

interface Props<T> {
  columns: Column<T>[]
  rows: T[]
  rowKey: (row: T, index: number) => string | number
  searchable?: boolean
  searchPlaceholder?: string
  searchText?: (row: T) => string
  loading?: boolean
  loadingText?: string
  empty?: ReactNode
  onRowClick?: (row: T) => void
  onRowDoubleClick?: (row: T) => void
  onRowContextMenu?: (e: React.MouseEvent, row: T) => void
  onSelectedChange?: (row: T | null) => void
  navigable?: boolean
  defaultSort?: { key: string; dir?: 'asc' | 'desc' }
}

export default function DataTable<T>({
  columns,
  rows,
  rowKey,
  searchable = false,
  searchPlaceholder,
  searchText,
  loading = false,
  loadingText,
  empty,
  onRowClick,
  onRowDoubleClick,
  onRowContextMenu,
  onSelectedChange,
  navigable = false,
  defaultSort,
}: Props<T>) {
  const { t } = useTranslation()
  const [query, setQuery] = useState('')
  const [sort, setSort] = useState<{ key: string; dir: 'asc' | 'desc' } | null>(
    defaultSort ? { key: defaultSort.key, dir: defaultSort.dir || 'asc' } : null,
  )
  // Selection is tracked by row identity, not by position.
  //
  // An index would be silently wrong here: every consumer of this table polls
  // and replaces the row array, and any refresh that reorders the rows without
  // changing their count leaves a stale index pointing at a different row. The
  // highlight follows the index, so the operator would see host B selected while
  // believing it was host A -- and the key handlers on these pages open a shell
  // or a file browser against whatever is selected. Acting on the wrong
  // compromised host is the worst failure this table can produce.
  const [selectedKey, setSelectedKey] = useState<string | number | null>(null)
  const bodyRef = useRef<HTMLTableSectionElement>(null)

  const filtered = useMemo(() => {
    let out = rows
    const q = query.trim().toLowerCase()
    if (q && searchText) {
      out = out.filter((r) => searchText(r).toLowerCase().includes(q))
    }
    if (sort) {
      const col = columns.find((c) => c.key === sort.key)
      const val = (r: T) =>
        (col?.sortValue ? col.sortValue(r) : (r as Record<string, unknown>)[sort.key]) as string | number
      out = [...out].sort((a, b) => {
        const av = val(a)
        const bv = val(b)
        const cmp = typeof av === 'number' && typeof bv === 'number' ? av - bv : String(av).localeCompare(String(bv))
        return sort.dir === 'asc' ? cmp : -cmp
      })
    }
    return out
  }, [rows, query, searchText, sort, columns])

  // Resolve the selected row from its key. -1 means "the selected row is not in
  // the current view" -- filtered out, or gone from the host.
  const selectedIndex = useMemo(() => {
    if (selectedKey === null) return -1
    return filtered.findIndex((r, i) => rowKey(r, i) === selectedKey)
  }, [filtered, selectedKey, rowKey])

  useEffect(() => {
    // Drop a selection whose row no longer exists, rather than leaving the
    // highlight on whichever row inherited its position.
    if (selectedKey !== null && selectedIndex === -1) setSelectedKey(null)
  }, [selectedKey, selectedIndex])

  useEffect(() => {
    onSelectedChange?.(selectedIndex >= 0 ? filtered[selectedIndex] : null)
  }, [selectedIndex, filtered, onSelectedChange])

  const toggleSort = (col: Column<T>) => {
    if (!col.sortable) return
    setSort((prev) =>
      prev?.key === col.key
        ? { key: col.key, dir: prev.dir === 'asc' ? 'desc' : 'asc' }
        : { key: col.key, dir: 'asc' },
    )
  }
	const move = (delta: number) => {
		if (filtered.length === 0) return
		// With nothing selected, either arrow key selects the first row.
		const from = selectedIndex < 0 ? (delta > 0 ? -1 : 0) : selectedIndex
		const next = Math.min(Math.max(from + delta, 0), filtered.length - 1)
		setSelectedKey(rowKey(filtered[next], next))
	}

	const onKeyDown = (e: React.KeyboardEvent) => {
		if (!navigable || filtered.length === 0) return
		if (e.key === 'ArrowDown') {
			e.preventDefault()
			move(1)
		} else if (e.key === 'ArrowUp') {
			e.preventDefault()
			move(-1)
		} else if (e.key === 'Enter' && selectedIndex >= 0) {
			e.preventDefault()
			const row = filtered[selectedIndex]
			if (onRowDoubleClick) onRowDoubleClick(row)
			else if (onRowClick) onRowClick(row)
		}
	}

  useEffect(() => {
    const el = bodyRef.current?.querySelector('[data-selected="true"]')
    el?.scrollIntoView({ block: 'nearest' })
  }, [selectedIndex])

  return (
    <div className="data-table" onKeyDown={onKeyDown} tabIndex={navigable ? 0 : undefined}>
      {searchable && (
        <div className="data-table-toolbar">
          <input
            className="data-table-search"
            value={query}
            onChange={(e) => setQuery(e.target.value)}
            placeholder={searchPlaceholder || t('common.search')}
          />
        </div>
      )}
      <div className="data-table-scroll">
        <table className="data">
          <thead>
            <tr>
              {columns.map((col) => (
                <th
                  key={col.key}
                  className={col.sortable ? 'sortable' : undefined}
                  scope="col"
                  aria-sort={
                    col.sortable
                      ? sort?.key === col.key
                        ? sort.dir === 'asc'
                          ? 'ascending'
                          : 'descending'
                        : 'none'
                      : undefined
                  }
                  style={col.width ? { width: col.width } : undefined}
                  onClick={() => toggleSort(col)}
                >
                  {col.label}
                  {sort?.key === col.key && <span className="sort-arrow">{sort.dir === 'asc' ? '▲' : '▼'}</span>}
                </th>
              ))}
            </tr>
          </thead>
          <tbody ref={bodyRef}>
            {loading ? (
              <tr>
                <td colSpan={columns.length} className="empty">
                  {loadingText || t('common.loading')}
                </td>
              </tr>
            ) : filtered.length === 0 ? (
              <tr>
                <td colSpan={columns.length} className="empty">
                  {empty || t('common.empty')}
                </td>
              </tr>
            ) : (
              filtered.map((row, i) => {
                const key = rowKey(row, i)
                const isSelected = navigable && i === selectedIndex
                return (
                  <tr
                    key={key}
                    data-selected={isSelected || undefined}
                    className={isSelected ? 'selected' : undefined}
                    onClick={() => onRowClick?.(row)}
                    onDoubleClick={() => onRowDoubleClick?.(row)}
                    onContextMenu={(e) => onRowContextMenu?.(e, row)}
                  >
                    {columns.map((col) => (
                      <td key={col.key} className={col.className}>
                        {col.render ? col.render(row) : String((row as Record<string, unknown>)[col.key] ?? '-')}
                      </td>
                    ))}
                  </tr>
                )
              })
            )}
          </tbody>
        </table>
      </div>
    </div>
  )
}
