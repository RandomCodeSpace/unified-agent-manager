import { Component, type ReactNode } from 'react';

/**
 * A drawn visual (a test strip, a table's bars) over content that reads without it: a throw while
 * drawing shows `fallback`, today's plain row or cell, and never takes the transcript down.
 */
export class VisualBoundary extends Component<{ fallback: ReactNode; children: ReactNode }, { failed: boolean }> {
  state = { failed: false };

  static getDerivedStateFromError(): { failed: boolean } {
    return { failed: true };
  }

  render() {
    return this.state.failed ? this.props.fallback : this.props.children;
  }
}
