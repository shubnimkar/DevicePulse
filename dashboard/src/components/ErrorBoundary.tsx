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
          <div className="empty-icon">⚠️</div>
          <h1>Something went wrong</h1>
          <p className="error-detail">{this.state.error.message}</p>
          <button
            type="button"
            className="action-btn"
            onClick={() => {
              this.setState({ error: null });
              window.location.reload();
            }}
          >
            Reload dashboard
          </button>
        </div>
      );
    }
    return this.props.children;
  }
}