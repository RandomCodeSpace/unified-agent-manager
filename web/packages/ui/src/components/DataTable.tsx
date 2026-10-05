import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, Filter } from 'lucide-react';
import { Children, isValidElement, memo, useMemo, useState, type CSSProperties, type ReactNode } from 'react';
import { cn } from '../lib/cn';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Popover } from './ui/popover';

type Value = string | number | null;
export interface TableColumn { name: string; heading?: ReactNode; align?: CSSProperties['textAlign'] }
export interface TableRow { id: number; values: readonly Value[]; cells: readonly ReactNode[] }
const PAGE_SIZE = 100;
const collator = new Intl.Collator(undefined, { sensitivity: 'base' });
// Treat only plain decimal/scientific numbers as numeric. IDs, dates, units
// and locale-dependent formats remain text rather than guessed values.
const decimal = /^[+-]?(?:0|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?$/;
const isNumber = (v: Value) => typeof v === 'number' ? Number.isFinite(v) : typeof v === 'string' && decimal.test(v) && Number.isFinite(Number(v));
const alignment = (align: CSSProperties['textAlign']) => align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left';

/** Local view over immutable rows. No data fetch, transcript update or model call. */
export const DataTable = memo(function DataTable({ columns, rows, label = 'Table', rowHeaders = false }: Readonly<{
  columns: readonly TableColumn[]; rows: readonly TableRow[]; label?: string; rowHeaders?: boolean;
}>) {
  const [sort, setSort] = useState<{ column: number; descending: boolean } | null>(null);
  const [filters, setFilters] = useState<Record<number, string>>({});
  const [page, setPage] = useState(0);
  const active = useMemo(() => Object.entries(filters).filter(([, text]) => text.trim()).map(([column, text]) => [Number(column), text.trim().toLocaleLowerCase()] as const), [filters]);
  const visible = useMemo(() => {
    const filtered = active.length ? rows.filter(row => active.every(([column, text]) => String(row.values[column] ?? '').toLocaleLowerCase().includes(text))) : rows;
    if (!sort) return filtered;
    const { column, descending } = sort;
    const numeric = rows.every(row => row.values[column] == null || row.values[column] === '' || isNumber(row.values[column]));
    return [...filtered].sort((a, b) => {
      const av = a.values[column], bv = b.values[column];
      // Missing values stay last in either direction.
      if (av == null || av === '') return bv == null || bv === '' ? 0 : 1;
      if (bv == null || bv === '') return -1;
      return (numeric ? Number(av) - Number(bv) : collator.compare(String(av), String(bv))) * (descending ? -1 : 1);
    });
  }, [rows, sort, active]);
  const start = Math.min(page, Math.max(0, Math.ceil(visible.length / PAGE_SIZE) - 1)) * PAGE_SIZE;
  const paginated = visible.length > PAGE_SIZE;
  return (
    <div className="my-2.5 min-w-0 max-w-full">
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The labelled scroll region accepts keyboard scrolling. */}
      <div role="region" aria-label={label} tabIndex={0} className="max-h-[32rem] max-w-full overflow-auto [overflow-wrap:normal]">
        <table className="data-table w-full">
          <thead><tr>{columns.map((column, index) => {
            const selected = sort?.column === index;
            const SortIcon = selected ? sort.descending ? ArrowDown : ArrowUp : ArrowUpDown;
            return (
              <th key={index} scope="col" aria-sort={selected ? sort.descending ? 'descending' : 'ascending' : 'none'} className={alignment(column.align)}>
                <div className={cn('flex items-center gap-0.5', column.align === 'right' && 'justify-end')}>
                  <span className="min-w-0">{column.heading ?? column.name}</span>
                  <Button size="icon" aria-label={`Sort by ${column.name}`} title={selected ? sort.descending ? 'Restore original order' : 'Sort descending' : 'Sort ascending'} className={cn('size-7 shrink-0 pointer-coarse:min-h-11 pointer-coarse:min-w-11', selected ? 'text-accent' : 'text-muted')} onClick={() => { setSort(selected ? sort.descending ? null : { column: index, descending: true } : { column: index, descending: false }); setPage(0); }}><SortIcon className="size-3.5" /></Button>
                  <Popover.Root>
                    <Popover.Trigger render={<Button size="icon" aria-label={`Filter ${column.name}`} title={filters[index] ? `Contains: ${filters[index]}` : `Filter ${column.name}`} className={cn('size-7 shrink-0 pointer-coarse:min-h-11 pointer-coarse:min-w-11', filters[index]?.trim() ? 'bg-accent-wash text-accent' : 'text-muted')} />}><Filter className="size-3.5" /></Popover.Trigger>
                    <Popover.Content side="bottom" align="end">
                      <Popover.Title>Filter {column.name}</Popover.Title>
                      <Input size="md" aria-label={`${column.name} contains`} placeholder="Contains…" value={filters[index] ?? ''} onChange={event => { setFilters({ ...filters, [index]: event.target.value }); setPage(0); }} />
                      <Button size="sm" disabled={!filters[index]} onClick={() => { setFilters({ ...filters, [index]: '' }); setPage(0); }}>Clear this filter</Button>
                    </Popover.Content>
                  </Popover.Root>
                </div>
              </th>
            );
          })}</tr></thead>
          <tbody>{visible.slice(start, start + PAGE_SIZE).map(row => <tr key={row.id}>{columns.map((column, index) => {
            const Cell = rowHeaders && index === 0 ? 'th' : 'td';
            return <Cell key={index} scope={Cell === 'th' ? 'row' : undefined} className={cn(alignment(column.align), column.align === 'right' && 'tabular-nums')}>{row.cells[index]}</Cell>;
          })}</tr>)}</tbody>
        </table>
        {visible.length === 0 && <p className="px-2 py-4 text-center text-caption text-muted">No matching rows</p>}
      </div>
      {(active.length > 0 || paginated) && <div className="flex items-center gap-2 pt-1 text-caption text-muted">
        <span role="status">{paginated ? `${start + 1}–${Math.min(start + PAGE_SIZE, visible.length)} of ${visible.length} rows` : `${visible.length} of ${rows.length} rows`}{paginated && active.length > 0 ? ` (${rows.length} total)` : ''}</span>
        {active.length > 0 && <Button size="sm" onClick={() => { setFilters({}); setPage(0); }}>Clear filters</Button>}
        {paginated && <div className="ml-auto flex gap-1"><Button size="icon" aria-label="Previous rows" disabled={start === 0} onClick={() => setPage(start / PAGE_SIZE - 1)}><ChevronLeft /></Button><Button size="icon" aria-label="Next rows" disabled={start + PAGE_SIZE >= visible.length} onClick={() => setPage(start / PAGE_SIZE + 1)}><ChevronRight /></Button></div>}
      </div>}
    </div>
  );
});

type ElementProps = { children?: ReactNode; style?: CSSProperties; alt?: string };
const elements = (children: ReactNode) => Children.toArray(children).filter(isValidElement<ElementProps>);
function cellText(node: ReactNode): string {
  return Children.toArray(node).map(child => isValidElement<ElementProps>(child) ? child.props.alt ?? cellText(child.props.children) : String(child)).join('');
}

/** Use the parser's React cells unchanged; only row order and visibility change. */
export function MarkdownTable({ children, streaming }: Readonly<{ children?: ReactNode; streaming: boolean }>) {
  const data = useMemo(() => {
    if (streaming) return null;
    const sections = elements(children);
    const head = sections.find(section => section.type === 'thead');
    const body = sections.find(section => section.type === 'tbody');
    const headers = elements(elements(head?.props.children)[0]?.props.children);
    if (!headers.length) return null;
    return {
      columns: headers.map((cell, index) => ({ name: cellText(cell.props.children).trim() || `Column ${index + 1}`, heading: cell.props.children, align: cell.props.style?.textAlign })),
      rows: elements(body?.props.children).map((row, id) => {
        const cells = elements(row.props.children).map(cell => cell.props.children);
        return { id, cells, values: cells.map(cell => cellText(cell).trim()) };
      }),
    };
  }, [children, streaming]);
  if (data) return <DataTable {...data} />;
  // While streaming, skip indexing, controls and sorting entirely.
  // eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- Keyboard-accessible overflow, as for completed tables.
  return <div role="region" aria-label="Table" tabIndex={0} className="my-2.5 max-w-full overflow-auto"><table className="!my-0 w-full">{children}</table></div>;
}
