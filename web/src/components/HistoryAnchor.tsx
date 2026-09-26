import { Component, createRef, type ReactNode, type RefObject } from 'react';
import { bandHeight, retainBands, windowEdges, type HistoryBand } from '../lib/historyBands';

interface Props {
  scroller: RefObject<HTMLElement | null>;
  firstItem: string;
  lastItem?: string;
  itemIds?: string[];
  knownIds?: string[];
  resetKey?: string | number;
  className?: string;
  children: ReactNode;
}
interface Anchor {
  scroller: HTMLElement;
  node?: HTMLElement;
  key?: string;
  id?: string;
  offset?: number;
  height: number;
  top: number;
  edges?: ReturnType<typeof windowEdges>;
  firstOffset?: number;
  lastBottom?: number;
  contentHeight?: number;
  reset?: boolean;
  focused?: HTMLElement;
  groupItem?: string;
}

/** Capture at React's pre-commit boundary, so history rendering can yield. */
export class HistoryAnchor extends Component<Props, Record<string, never>, Anchor | null> {
  private content = createRef<HTMLDivElement>();
  private topSpacer = createRef<HTMLDivElement>();
  private bottomSpacer = createRef<HTMLDivElement>();
  private before: HistoryBand[] = [];
  private after: HistoryBand[] = [];

  private row(id: string): HTMLElement | null {
    const content = this.content.current;
    if (!content) return null;
    const exact = content.querySelector<HTMLElement>(`[data-history-anchor="${CSS.escape(id)}"], #${CSS.escape(`item-${id}`)}`);
    if (exact && !exact.closest('[inert]')) return exact;
    return this.group(id);
  }

  private group(id: string): HTMLElement | null {
    // A collapsed activity row represents several items at the same position.
    return [...(this.content.current?.querySelectorAll<HTMLElement>('[data-history-items]') ?? [])].find(node => {
      if (node.closest('[inert]')) return false;
      try { return (JSON.parse(node.dataset.historyItems ?? '[]') as string[]).includes(id); }
      catch { return false; }
    }) ?? null;
  }

  getSnapshotBeforeUpdate(previous: Props): Anchor | null {
    const el = this.props.scroller.current;
    const reset = previous.resetKey !== this.props.resetKey;
    const knownChanged = previous.knownIds?.length !== this.props.knownIds?.length || previous.knownIds?.some((id, i) => id !== this.props.knownIds?.[i]);
    if (!el || (!reset && !knownChanged && previous.firstItem === this.props.firstItem && previous.lastItem === this.props.lastItem)) return null;
    const viewport = el.getBoundingClientRect();
    const active = this.props.itemIds && new Set(this.props.itemIds);
    const previousIds = new Set(previous.itemIds);
    const rows = Array.from(el.querySelectorAll<HTMLElement>('[data-history-anchor], [data-history-items], [id^="item-"]')).filter(row => {
      const rect = row.getBoundingClientRect();
      if (row.closest('[inert]') || rect.bottom <= viewport.top || rect.top >= viewport.bottom) return false;
      if (!active) return true;
      if (!row.hasAttribute('data-history-items')) {
        const id = row.dataset.historyAnchor ?? row.id.slice(5);
        return !previousIds.has(id) || active.has(id);
      }
      try { return (JSON.parse(row.dataset.historyItems ?? '[]') as string[]).some(id => active.has(id)); }
      catch { return false; }
    });
    const node = rows.find(row => !row.hasAttribute('data-history-items')) ?? rows[0];
    const snapshot: Anchor = { scroller: el, node, key: node?.dataset.historyAnchor, id: node?.id, offset: node?.getBoundingClientRect().top, height: el.scrollHeight, top: el.scrollTop, reset };
    if (node?.dataset.historyItems) {
      try {
        const active = new Set(this.props.itemIds);
        snapshot.groupItem = (JSON.parse(node.dataset.historyItems) as string[]).find(id => active.has(id));
      } catch { /* A missing group identity uses the ordinary height fallback. */ }
    }
    const focused = el.ownerDocument.activeElement;
    if (this.props.itemIds && focused instanceof HTMLElement && this.content.current?.contains(focused)) snapshot.focused = focused;
    if (previous.itemIds && this.props.itemIds && this.content.current) {
      const edges = windowEdges(previous.itemIds, this.props.itemIds);
      const rect = this.content.current.getBoundingClientRect();
      snapshot.edges = edges;
      snapshot.contentHeight = rect.height;
      const first = edges.first && this.row(edges.first), last = edges.last && this.row(edges.last);
      if (first) snapshot.firstOffset = first.getBoundingClientRect().top - rect.top;
      if (last) snapshot.lastBottom = last.getBoundingClientRect().bottom - rect.top;
      if (!edges.first && !reset) {
        const known = this.props.knownIds ?? [];
        const oldIndex = known.indexOf(previous.firstItem), nextIndex = known.indexOf(this.props.firstItem);
        if (oldIndex >= 0 && nextIndex >= 0) {
          if (nextIndex > oldIndex) edges.droppedBefore = previous.itemIds;
          else edges.droppedAfter = previous.itemIds;
        } else snapshot.reset = true;
      }
    }
    return snapshot;
  }

  componentDidUpdate(_previous: Props, _state: Record<string, never>, anchor: Anchor | null) {
    if (!anchor) return;
    const el = anchor.scroller;
    if (this.props.itemIds) {
      const active = new Set(this.props.itemIds), known = this.props.knownIds && new Set(this.props.knownIds);
      const keep = (id: string) => !active.has(id) && (!known || known.has(id));
      this.before = anchor.reset ? [] : retainBands(this.before, keep);
      this.after = anchor.reset ? [] : retainBands(this.after, keep);
      const edges = !anchor.reset && anchor.edges;
      const content = this.content.current;
      if (edges && content) {
        const rect = content.getBoundingClientRect();
        if (edges.droppedBefore.length) {
          const first = edges.first && this.row(edges.first);
          const height = first && anchor.firstOffset !== undefined
            ? anchor.firstOffset - (first.getBoundingClientRect().top - rect.top)
            : anchor.contentHeight ?? 0;
          const ids = edges.droppedBefore.filter(keep);
          if (ids.length) this.before.push({ ids, height: Math.max(0, height) * ids.length / edges.droppedBefore.length });
        }
        if (edges.droppedAfter.length) {
          const last = edges.last && this.row(edges.last);
          const height = last && anchor.lastBottom !== undefined
            ? (anchor.contentHeight ?? 0) - anchor.lastBottom - (rect.height - (last.getBoundingClientRect().bottom - rect.top))
            : anchor.contentHeight ?? 0;
          const ids = edges.droppedAfter.filter(keep);
          if (ids.length) this.after.unshift({ ids, height: Math.max(0, height) * ids.length / edges.droppedAfter.length });
        }
      }
      if (this.topSpacer.current) this.topSpacer.current.style.height = `${bandHeight(this.before)}px`;
      if (this.bottomSpacer.current) this.bottomSpacer.current.style.height = `${bandHeight(this.after)}px`;
    } else { this.before = []; this.after = []; }
    const focused = anchor.focused;
    anchor.focused = undefined;
    if (focused && !focused.isConnected && el.ownerDocument.activeElement === el.ownerDocument.body) {
      if (!el.hasAttribute('tabindex')) el.tabIndex = -1;
      el.focus({ preventScroll: true });
    }
    if (anchor.reset) { anchor.node = undefined; return; }
    const kept = (anchor.node?.isConnected && el.contains(anchor.node) ? anchor.node
      : anchor.key ? el.querySelector<HTMLElement>(`[data-history-anchor="${CSS.escape(anchor.key)}"]`)
        : anchor.id ? el.querySelector<HTMLElement>(`#${CSS.escape(anchor.id)}`) : null)
      ?? (anchor.groupItem ? this.group(anchor.groupItem) : null);
    anchor.node = undefined;
    el.scrollTop = kept && anchor.offset !== undefined
      ? el.scrollTop + kept.getBoundingClientRect().top - anchor.offset
      : anchor.top + el.scrollHeight - anchor.height;
  }

  render() {
    if (!this.props.itemIds) return this.props.children;
    return <div>
      <div ref={this.topSpacer} data-history-top-spacer="" aria-hidden="true" style={{ height: bandHeight(this.before) }} />
      <div ref={this.content} data-history-window="" data-history-content="" className={this.props.className ?? 'flex flex-col gap-6'}>{this.props.children}</div>
      <div ref={this.bottomSpacer} data-history-bottom-spacer="" aria-hidden="true" style={{ height: bandHeight(this.after) }} />
    </div>;
  }
}
