import { Component, type ReactNode } from 'react';

/**
 * A drawn visual (a test strip, a table's bars) over content that reads without it: a throw while
 * drawing shows `fallback`, today's plain row or cell, and never takes the transcript down. A
 * function fallback gets what was thrown (the app's own boundary shows its message). React logs
 * the error to the console either way.
 */
export class VisualBoundary extends Component<{ fallback: ReactNode | ((error: unknown) => ReactNode); children: ReactNode }, { failed: boolean; error?: unknown }> {
  state: { failed: boolean; error?: unknown } = { failed: false };

  static getDerivedStateFromError(error: unknown): { failed: boolean; error: unknown } {
    return { failed: true, error };
  }

  render() {
    if (!this.state.failed) return this.props.children;
    const { fallback } = this.props;
    return typeof fallback === 'function' ? fallback(this.state.error) : fallback;
  }
}
