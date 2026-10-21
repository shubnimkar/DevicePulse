'use client';

import { Component, ErrorInfo, ReactNode } from 'react';

interface Props {
  children: ReactNode;
}

interface State {
  error: Error | null;
}

/**
 * Top-level error boundary: a render crash shows a recoverable screen
 * instead of a blank page.
 */
export default class ErrorBoundary extends Component<Props, State> {
  state: State = { error: null };

  static getDerivedStateFromError(error: Error): State {
    return { error };
  }

  componentDidCatch(error: Error, info: ErrorInfo) {
    console.error('UI crashed:', error, info.componentStack);
  }

  render() {
    if (this.state.error) {
      return (
        <div className="error-boundary">
          <div className="empty-icon" aria-hidden="true">
            <svg width="40" height="40" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="1.6" strokeLinecap="round" strokeLinejoin="round"><path d="M12 3l10 18H2L12 3z"/><path d="M12 10v5M12 18v.5"/></svg>
          </div>
          <h1>Something went wrong</h1>
          <p className="error-detail">The dashboard hit an unexpected error. Reloading usually fixes it.</p>
          <button
            type="button"
            className="action-btn"
            onClick={() => window.location.reload()}
          >
            Reload dashboard
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}