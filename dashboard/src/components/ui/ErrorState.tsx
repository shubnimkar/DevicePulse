import type { ReactNode } from 'react';

interface Props {
  icon: ReactNode;
  title: string;
  message: ReactNode;
  onRetry: () => void;
}

export default function ErrorState({ icon, title, message, onRetry }: Props) {
  return (
    <div className="empty-state">
      <span className="empty-icon" aria-hidden="true">{icon}</span>
      <div className="empty-title">{title}</div>
      <div className="empty-sub" role="alert">{message}</div>
      <button type="button" className="action-btn primary" style={{ marginTop: '1rem' }} onClick={onRetry}>Retry</button>
    </div>
  );
}
