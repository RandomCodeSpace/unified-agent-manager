import { Component, type ReactNode } from 'react';

type Props = Readonly<{ fallback: ReactNode | ((error: unknown) => ReactNode); /** A change shows the children again after a throw. */ resetKey?: unknown; children: ReactNode }>;

/**
 * A drawn visual (a test strip, a table's bars) over content that reads without it: a throw while
 * drawing shows `fallback`, today's plain row or cell, and never takes the transcript down. A
 * function fallback gets what was thrown (the app's own boundary shows its message). React logs
 * the error to the console either way.
 */
export class VisualBoundary extends Component<Props, { failed: boolean; error?: unknown }> {
  state: { failed: boolean; error?: unknown } = { failed: false };

  static getDerivedStateFromError(error: unknown): { failed: boolean; error: unknown } {
    return { failed: true, error };
  }

  componentDidUpdate(prev: Props) {
    if (this.state.failed && prev.resetKey !== this.props.resetKey) this.setState({ failed: false, error: undefined });
  }

  render() {
    if (!this.state.failed) return this.props.children;
    const { fallback } = this.props;
    return typeof fallback === 'function' ? fallback(this.state.error) : fallback;
  }
}

/** One part of the page (a transcript row, the conversation, a side panel) that fails alone: one muted line in its place until `of` changes. */
export function Contained({ what, of, children }: Readonly<{ what: string; of: unknown; children: ReactNode }>) {
  return <VisualBoundary resetKey={of} fallback={<p className="text-ui text-muted">{what} could not be shown.</p>}>{children}</VisualBoundary>;
}
