import {
  columnResizingFeature,
  columnSizingFeature,
  columnVisibilityFeature,
  createSortedRowModel,
  flexRender,
  rowSortingFeature,
  sortFn_alphanumeric,
  sortFn_basic,
  sortFn_datetime,
  sortFn_text,
  tableFeatures,
  useTable,
} from '@tanstack/react-table'
import type { CellData, ColumnDef, RowData, TableFeatures, SortingState, ColumnVisibilityState } from '@tanstack/react-table'
import { useVirtualizer } from '@tanstack/react-virtual'
import { useEffect, useId, useRef, useState } from 'react'
import { useRem } from '../lib/rem'
import { EmptyState, ErrorMessage, LoadingState } from './states'
import { useRowNavigation } from './useRowNavigation'

declare module '@tanstack/react-table' {
  // The type parameters must match TanStack's declaration.
  // eslint-disable-next-line @typescript-eslint/no-unused-vars
  interface ColumnMeta<TFeatures extends TableFeatures, TData extends RowData, TValue extends CellData> {
    /** A number column: right-aligned with tabular figures, header included. */
    numeric?: boolean
  }
}

/** The table features DataTable uses (v9 registers features explicitly). */
const features = tableFeatures({
  columnVisibilityFeature,
  columnSizingFeature,
  columnResizingFeature,
  rowSortingFeature,
  sortedRowModel: createSortedRowModel(),
  sortFns: {
    alphanumeric: sortFn_alphanumeric,
    text: sortFn_text,
    datetime: sortFn_datetime,
    basic: sortFn_basic,
  },
})

/** Column definition for a DataTable row type. */
export type DataTableColumn<T extends RowData> = ColumnDef<typeof features, T, any> // eslint-disable-line @typescript-eslint/no-explicit-any

const cellClass = (base: string, numeric: boolean | undefined) => (numeric ? `${base} num` : base)

export interface DataTableProps<T extends RowData> {
  data: T[]
  columns: DataTableColumn<T>[]
  getRowId: (row: T) => string
  /** Accessible name of the table. */
  label: string
  isLoading?: boolean
  error?: unknown
  onRetry?: () => void
  emptyMessage?: React.ReactNode
  onRowClick?: (row: T) => void
  /** Called when the last rows scroll into view: fetch the next page. */
  onEndReached?: () => void
  /** Whether another page exists; onEndReached is only called when true. */
  hasNextPage?: boolean
  isFetchingNextPage?: boolean
  /** Row height in rem: the virtualiser needs a fixed height, and rem keeps it with the text size. */
  rowSize?: number
  /** Height of the scroll area in px. */
  height?: number
  /**
   * Roving focus over the rows: one row is in the tab order and the arrow
   * keys, j and k, Page Up, Page Down, Home and End move between rows.
   */
  keyboardNavigation?: boolean
  /**
   * Controlled sorting, for a sort kept in the URL. A header click always
   * gives a column and a direction: the sort is never removed, only flipped.
   */
  sorting?: SortingState
  onSortingChange?: (next: SortingState) => void
}

const OVERSCAN = 8
const END_THRESHOLD = 5

/**
 * Table for long lists (ADR 0030): TanStack Table for columns, sorting of the
 * loaded rows, visibility and sizing; TanStack Virtual keeps only visible rows
 * in the DOM.
 */
export function DataTable<T extends RowData>({
  data,
  columns,
  getRowId,
  label,
  isLoading = false,
  error,
  onRetry,
  emptyMessage = 'No rows.',
  onRowClick,
  onEndReached,
  hasNextPage = false,
  isFetchingNextPage = false,
  rowSize = 2.25,
  height = 480,
  keyboardNavigation = false,
  sorting: controlledSorting,
  onSortingChange,
}: DataTableProps<T>) {
  const rem = useRem()
  const [localSorting, setLocalSorting] = useState<SortingState>([])
  const controlled = controlledSorting !== undefined && onSortingChange !== undefined
  const sorting = controlled ? controlledSorting : localSorting
  const [columnVisibility, setColumnVisibility] = useState<ColumnVisibilityState>({})
  const scrollRef = useRef<HTMLDivElement>(null)
  const menuId = useId()
  // An anchor name is a CSS dashed ident: useId output has colons, so drop them.
  const menuAnchor = `--columns-${menuId.replace(/[^a-zA-Z0-9_-]/g, '')}`

  const table = useTable({
    features,
    data,
    columns,
    getRowId,
    state: { sorting, columnVisibility },
    onSortingChange: (updater) => {
      const next = typeof updater === 'function' ? updater(sorting) : updater
      if (controlled) onSortingChange(next)
      else setLocalSorting(next)
    },
    enableSortingRemoval: !controlled,
    onColumnVisibilityChange: setColumnVisibility,
    columnResizeMode: 'onChange',
  })
  const rows = table.getRowModel().rows

  // TanStack Virtual returns unmemoizable functions; the React Compiler skips this component.
  // eslint-disable-next-line react-hooks/incompatible-library
  const virtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => scrollRef.current,
    estimateSize: () => rowSize * rem,
    overscan: OVERSCAN,
  })
  // The row height is a count of rem: measure again when the text size changes.
  useEffect(() => {
    virtualizer.measure()
  }, [virtualizer, rem])
  const nav = useRowNavigation({
    count: rows.length,
    containerRef: scrollRef,
    scrollToIndex: (index) => virtualizer.scrollToIndex(index),
  })
  const items = virtualizer.getVirtualItems()
  const lastIndex = items.length > 0 ? items[items.length - 1].index : -1

  useEffect(() => {
    if (onEndReached && hasNextPage && !isFetchingNextPage && lastIndex >= rows.length - END_THRESHOLD && rows.length > 0) {
      onEndReached()
    }
  }, [lastIndex, rows.length, onEndReached, hasNextPage, isFetchingNextPage])

  const leaf = table.getVisibleLeafColumns()
  const gridTemplate = leaf.map((c) => `${c.getSize()}px`).join(' ')
  const totalWidth = leaf.reduce((sum, c) => sum + c.getSize(), 0)

  return (
    <div className="data-table">
      <div className="column-menu">
        <button type="button" popoverTarget={menuId} style={{ anchorName: menuAnchor }}>
          Columns
        </button>
        <div
          id={menuId}
          className="column-list"
          role="group"
          aria-label="Columns"
          popover="auto"
          style={{ positionAnchor: menuAnchor }}
        >
          {table.getAllLeafColumns().map((column) => (
            <label key={column.id}>
              <input
                type="checkbox"
                checked={column.getIsVisible()}
                onChange={column.getToggleVisibilityHandler()}
              />
              {typeof column.columnDef.header === 'string' ? column.columnDef.header : column.id}
            </label>
          ))}
        </div>
      </div>
      <div className="table-scroll">
        <div
          role="table"
          aria-label={label}
          aria-rowcount={rows.length + 1}
          aria-busy={isLoading || isFetchingNextPage}
          style={{ minWidth: totalWidth }}
        >
          <div role="rowgroup" className="thead">
            {table.getHeaderGroups().map((group) => (
              <div role="row" key={group.id} className="tr" style={{ gridTemplateColumns: gridTemplate }}>
                {group.headers.map((header) => {
                  const sorted = header.column.getIsSorted()
                  return (
                    <div
                      role="columnheader"
                      key={header.id}
                      className={cellClass('th', header.column.columnDef.meta?.numeric)}
                      aria-sort={sorted === 'asc' ? 'ascending' : sorted === 'desc' ? 'descending' : undefined}
                    >
                      {header.column.getCanSort() ? (
                        <button type="button" onClick={header.column.getToggleSortingHandler()}>
                          {flexRender(header.column.columnDef.header, header.getContext())}
                          {sorted && (
                            <span aria-hidden="true">{sorted === 'asc' ? ' ▲' : ' ▼'}</span>
                          )}
                        </button>
                      ) : (
                        flexRender(header.column.columnDef.header, header.getContext())
                      )}
                      {header.column.getCanResize() && (
                        <div
                          className="resizer"
                          aria-hidden="true"
                          onMouseDown={header.getResizeHandler()}
                          onTouchStart={header.getResizeHandler()}
                        />
                      )}
                    </div>
                  )
                })}
              </div>
            ))}
          </div>
          {error ? (
            <StateRow>
              <ErrorMessage error={error} onRetry={onRetry} />
            </StateRow>
          ) : isLoading ? (
            <StateRow>
              <LoadingState />
            </StateRow>
          ) : rows.length === 0 ? (
            <StateRow>
              <EmptyState>{emptyMessage}</EmptyState>
            </StateRow>
          ) : (
            <div ref={scrollRef} className="scroll" style={{ height, overflow: 'auto' }}>
              <div role="rowgroup" style={{ height: virtualizer.getTotalSize(), position: 'relative' }}>
                {items.map((item) => {
                  const row = rows[item.index]
                  return (
                    <div
                      role="row"
                      key={row.id}
                      aria-rowindex={item.index + 2}
                      data-index={item.index}
                      className={onRowClick ? 'tr clickable' : 'tr'}
                      tabIndex={
                        keyboardNavigation ? (item.index === nav.active ? 0 : -1) : onRowClick ? 0 : undefined
                      }
                      onFocus={keyboardNavigation ? () => nav.setActive(item.index) : undefined}
                      onClick={onRowClick ? () => onRowClick(row.original) : undefined}
                      onKeyDown={
                        onRowClick || keyboardNavigation
                          ? (e) => {
                              if (keyboardNavigation && nav.onRowKeyDown(e, item.index)) return
                              if (e.key === 'Enter' && onRowClick) onRowClick(row.original)
                            }
                          : undefined
                      }
                      style={{
                        position: 'absolute',
                        top: 0,
                        height: item.size,
                        transform: `translateY(${item.start}px)`,
                        gridTemplateColumns: gridTemplate,
                      }}
                    >
                      {row.getVisibleCells().map((cell) => (
                        <div role="cell" key={cell.id} className={cellClass('td', cell.column.columnDef.meta?.numeric)}>
                          {flexRender(cell.column.columnDef.cell, cell.getContext())}
                        </div>
                      ))}
                    </div>
                  )
                })}
              </div>
              {isFetchingNextPage && <LoadingState label="Loading more" />}
            </div>
          )}
        </div>
      </div>
    </div>
  )
}

/** A table must hold rows: a loading, error or empty message sits in one full-width row. */
function StateRow({ children }: { children: React.ReactNode }) {
  return (
    <div role="rowgroup">
      <div role="row">
        <div role="cell">{children}</div>
      </div>
    </div>
  )
}
