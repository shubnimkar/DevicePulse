'use client';

import { useEffect, useRef } from 'react';
import { AnimatePresence, motion } from 'motion/react';

interface Props {
  open: boolean;
  title: string;
  message: string;
  confirmLabel?: string;
  cancelLabel?: string;
  danger?: boolean;
  busy?: boolean;
  onConfirm: () => void;
  onCancel: () => void;
}

/**
 * Accessible confirmation dialog: Escape closes, focus is trapped inside,
 * focus is restored to the trigger on close, background scroll is locked.
 */
export default function ConfirmDialog({
  open,
  title,
  message,
  confirmLabel = 'Confirm',
  cancelLabel = 'Cancel',
  danger = false,
  busy = false,
  onConfirm,
  onCancel,
}: Props) {
  const panelRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;

    const previouslyFocused =
      document.activeElement instanceof HTMLElement ? document.activeElement : null;
    document.body.style.overflow = 'hidden';

    const focusables = () =>
      Array.from(
        panelRef.current?.querySelectorAll<HTMLElement>('button, input, select, textarea, a[href]') ?? []
      ).filter(el => !el.hasAttribute('disabled'));

    // Initial focus goes to the safe action (Cancel).
    focusables()[0]?.focus();

    const onKeyDown = (e: KeyboardEvent) => {
      if (e.key === 'Escape') {
        e.preventDefault();
        if (!busy) onCancel();
        return;
      }
      if (e.key !== 'Tab') return;
      const items = focusables();
      if (!items.length) return;
      const first = items[0];
      const last = items[items.length - 1];
      const activeEl = document.activeElement;
      const inside = panelRef.current?.contains(activeEl);
      if (e.shiftKey && (activeEl === first || !inside)) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && (activeEl === last || !inside)) {
        e.preventDefault();
        first.focus();
      }
    };

    window.addEventListener('keydown', onKeyDown);
    return () => {
      window.removeEventListener('keydown', onKeyDown);
      document.body.style.overflow = '';
      previouslyFocused?.focus();
    };
  }, [open, busy, onCancel]);

  return (
    <AnimatePresence>
      {open && (
        <motion.div
          className="modal-backdrop"
          role="presentation"
          initial={{ opacity: 0 }}
          animate={{ opacity: 1 }}
          exit={{ opacity: 0 }}
          onMouseDown={e => {
            if (e.target === e.currentTarget && !busy) onCancel();
          }}
        >
          <motion.div
            ref={panelRef}
            className="modal-panel confirm-panel"
            role="dialog"
            aria-modal="true"
            aria-labelledby="confirm-dialog-title"
            aria-describedby="confirm-dialog-message"
            initial={{ opacity: 0, y: 14, scale: 0.98 }}
            animate={{ opacity: 1, y: 0, scale: 1 }}
            exit={{ opacity: 0, y: 10, scale: 0.98 }}
            transition={{ type: 'spring', stiffness: 360, damping: 32 }}
          >
            <div className="modal-header">
              <h2 id="confirm-dialog-title">{title}</h2>
            </div>
            <p id="confirm-dialog-message" className="confirm-message">{message}</p>
            <div className="modal-actions">
              <button type="button" className="modal-btn" onClick={onCancel} disabled={busy}>
                {cancelLabel}
              </button>
              <button
                type="button"
                className={`modal-btn ${danger ? 'danger' : ''}`}
                onClick={onConfirm}
                disabled={busy}
              >
                {confirmLabel}
              </button>
            </div>
          </motion.div>
        </motion.div>
      )}
    </AnimatePresence>
  );
}
