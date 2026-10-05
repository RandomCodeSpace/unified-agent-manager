import { ArrowDown, ArrowUp, ArrowUpDown, ChevronLeft, ChevronRight, Filter } from 'lucide-react';
import { Children, isValidElement, memo, useCallback, useMemo, useState, type CSSProperties, type KeyboardEvent, type ReactElement, type ReactNode } from 'react';
import { cn } from '../lib/cn';
import { Button } from './ui/button';
import { Input } from './ui/input';
import { Popover } from './ui/popover';

type Value = string | number | null;
export interface TableColumn { name: string; heading?: ReactNode; align?: CSSProperties['textAlign'] }
export interface TableRow { id: number; values: readonly Value[]; cells: readonly ReactNode[] }
const PAGE_SIZE = 100;
// Text sorts naturally: "item 9" before "item 10".
const collator = new Intl.Collator(undefined, { sensitivity: 'base', numeric: true });
// Numbers as tables print them: a sign, a leading currency symbol, thousands separators and a
// trailing percent. Leading zeros (IDs), dates and other units remain text.
const formatted = /^([+-]?)\p{Sc}?((?:0|[1-9]\d{0,2}(?:,\d{3})+|[1-9]\d*)(?:\.\d+)?(?:[eE][+-]?\d+)?)%?$/u;
function numeric(v: Value): number | null {
  if (typeof v === 'number') return Number.isFinite(v) ? v : null;
  const match = v == null ? null : formatted.exec(v);
  const n = match ? Number(match[1] + match[2].replaceAll(',', '')) : NaN;
  return Number.isFinite(n) ? n : null;
}
const alignment = (align: CSSProperties['textAlign']) => align === 'right' ? 'text-right' : align === 'center' ? 'text-center' : 'text-left';

/** One tab stop for a row of controls: Tab returns to the last one used; arrow keys, Home and End move along. */
export function useRovingFocus(count: number) {
  const [current, setCurrent] = useState(0);
  const active = Math.min(current, count - 1);
  return {
    onKeyDown(event: KeyboardEvent<HTMLElement>) {
      const items = [...event.currentTarget.querySelectorAll<HTMLElement>('[data-roving]')];
      const index = items.indexOf(event.target as HTMLElement);
      const next = event.key === 'Home' ? 0 : event.key === 'End' ? items.length - 1 : event.key === 'ArrowLeft' ? index - 1 : event.key === 'ArrowRight' ? index + 1 : -1;
      // Keys typed in a control's popup (portalled, so not among these items) are left alone.
      if (index < 0 || next < 0 || next >= items.length) return;
      event.preventDefault();
      items[next].focus();
    },
    item: (index: number) => ({ 'data-roving': '', tabIndex: index === active ? 0 : -1, onFocus: () => setCurrent(index) }),
  };
}

/** A heading, or a paragraph of bold text alone, directly above a table names it. */
function headingBefore(element: Element | null): string {
  const before = element?.previousElementSibling;
  if (!before) return '';
  const bold = before.tagName === 'P' && before.childElementCount === 1 && before.firstElementChild?.tagName === 'STRONG' && before.textContent === before.firstElementChild.textContent;
  return /^H[1-6]$/.test(before.tagName) || bold ? before.textContent?.trim() ?? '' : '';
}

/** Local view over immutable rows. No data fetch, transcript update or model call. */
export const DataTable = memo(function DataTable({ columns, rows, label, rowHeaders = false }: Readonly<{
  columns: readonly TableColumn[]; rows: readonly TableRow[]; label?: string; rowHeaders?: boolean;
}>) {
  const [sort, setSort] = useState<{ column: number; descending: boolean } | null>(null);
  const [filters, setFilters] = useState<Record<number, string>>({});
  const [page, setPage] = useState(0);
  const [menu, setMenu] = useState<number | null>(null);
  const [heading, setHeading] = useState('');
  const named = useCallback((element: HTMLDivElement | null) => { if (element) setHeading(headingBefore(element)); }, []);
  const roving = useRovingFocus(columns.length);
  const active = useMemo(() => Object.entries(filters).filter(([, text]) => text.trim()).map(([column, text]) => [Number(column), text.trim().toLocaleLowerCase()] as const), [filters]);
  const visible = useMemo(() => {
    const filtered = active.length ? rows.filter(row => active.every(([column, text]) => String(row.values[column] ?? '').toLocaleLowerCase().includes(text))) : rows;
    if (!sort) return filtered;
    const { column, descending } = sort;
    const numbers = rows.every(row => row.values[column] == null || row.values[column] === '' || numeric(row.values[column]) !== null);
    return [...filtered].sort((a, b) => {
      const av = a.values[column], bv = b.values[column];
      // Missing values stay last in either direction.
      if (av == null || av === '') return bv == null || bv === '' ? 0 : 1;
      if (bv == null || bv === '') return -1;
      return (numbers ? numeric(av)! - numeric(bv)! : collator.compare(String(av), String(bv))) * (descending ? -1 : 1);
    });
  }, [rows, sort, active]);
  const start = Math.min(page, Math.max(0, Math.ceil(visible.length / PAGE_SIZE) - 1)) * PAGE_SIZE;
  const paginated = visible.length > PAGE_SIZE;
  const order = (next: typeof sort) => { setSort(next); setPage(0); setMenu(null); };
  return (
    <div ref={named} className="my-2.5 min-w-0 max-w-full">
      {/* eslint-disable-next-line jsx-a11y/no-noninteractive-tabindex -- The labelled scroll region accepts keyboard scrolling. */}
      <div role="region" aria-label={label ?? `Table: ${heading || columns.slice(0, 3).map(column => column.name).join(', ')}`} tabIndex={0} className="max-h-[32rem] max-w-full overflow-y-auto overflow-x-hidden [overflow-wrap:normal] pointer-coarse:max-h-none">
        <table className="data-table w-full">
          <thead><tr onKeyDown={roving.onKeyDown}>{columns.map((column, index) => {
            const selected = sort?.column === index;
            const filter = filters[index]?.trim();
            const Icon = selected ? sort.descending ? ArrowDown : ArrowUp : filter ? Filter : ArrowUpDown;
            const state = [selected ? `Sorted ${sort.descending ? 'descending' : 'ascending'}` : '', filter ? `Contains: ${filter}` : ''].filter(Boolean).join(', ');
            return (
              <th key={index} scope="col" aria-sort={selected ? sort.descending ? 'descending' : 'ascending' : 'none'} className={alignment(column.align)}>
                <div className={cn('flex items-center gap-1.5', column.align === 'right' && 'justify-end')}>
                  <span className="min-w-0">{column.heading ?? column.name}</span>
                  {/* One small control per column; its hit area grows to 44px on a coarse pointer without taking width. */}
                  <Popover.Root open={menu === index} onOpenChange={open => setMenu(open ? index : null)}>
                    <Popover.Trigger render={<Button size="icon-sm" {...roving.item(index)} aria-label={`Sort or filter ${column.name}`} title={state || 'Sort or filter'} className={cn(selected || filter ? 'text-accent' : 'text-muted', filter && 'bg-accent-wash')} />}><Icon className="size-3.5" /></Popover.Trigger>
                    <Popover.Content side="bottom" align="end">
                      <Popover.Title>{column.name}</Popover.Title>
                      <div className="flex flex-col">
                        <Button size="md" aria-pressed={selected && !sort.descending} className="justify-start px-2" onClick={() => order({ column: index, descending: false })}><ArrowUp />Sort ascending</Button>
                        <Button size="md" aria-pressed={selected && sort.descending} className="justify-start px-2" onClick={() => order({ column: index, descending: true })}><ArrowDown />Sort descending</Button>
                        <Button size="md" disabled={!selected} className="justify-start px-2" onClick={() => order(null)}><ArrowUpDown />Original order</Button>
                      </div>
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
const elements = (children: ReactNode) => Children.toArray(children).filter((child): child is ReactElement<ElementProps> => isValidElement<ElementProps>(child));
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
  return <div role="region" aria-label="Table" tabIndex={0} className="my-2.5 max-w-full overflow-y-auto overflow-x-hidden"><table className="!my-0 w-full">{children}</table></div>;
}
