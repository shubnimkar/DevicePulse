import type { ReactNode } from 'react';

interface Props {
  icon: ReactNode;
  title: string;
  description?: ReactNode;
  action?: ReactNode;
}

export default function EmptyState({ icon, title, description, action }: Props) {
  return (
    <div className="empty-state">
      <span className="empty-icon" aria-hidden="true">{icon}</span>
      <div className="empty-title">{title}</div>
      {description && <div className="empty-sub">{description}</div>}
      {action}
    </div>
  );
}
