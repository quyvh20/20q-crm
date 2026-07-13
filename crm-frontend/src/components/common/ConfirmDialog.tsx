import { useCallback, useRef, useState, type ReactNode } from 'react';

// The app's one confirmation dialog (U1.3) — replaces the window.confirm/alert
// mix across settings. Promise-based so call sites read like the native API:
//
//   const { confirm, dialog } = useConfirm();
//   ...
//   if (!(await confirm({ title: 'Remove member', body: '…', tone: 'danger' }))) return;
//   ...
//   return (<>{…}{dialog}</>);

export interface ConfirmOptions {
  title: string;
  body: ReactNode;
  confirmLabel?: string;
  cancelLabel?: string;
  /** 'danger' renders a red confirm button; 'default' the primary one. */
  tone?: 'danger' | 'default';
}

export function useConfirm() {
  const [opts, setOpts] = useState<ConfirmOptions | null>(null);
  const resolver = useRef<((ok: boolean) => void) | null>(null);
  const boxRef = useRef<HTMLDivElement | null>(null);

  const confirm = useCallback((o: ConfirmOptions) => {
    // Re-entrancy guard: a confirm() while another is pending settles the
    // first as cancelled instead of stranding its awaiting handler forever.
    resolver.current?.(false);
    setOpts(o);
    return new Promise<boolean>((resolve) => {
      resolver.current = resolve;
    });
  }, []);

  const settle = (ok: boolean) => {
    resolver.current?.(ok);
    resolver.current = null;
    setOpts(null);
  };

  // Keyboard parity with window.confirm (which this replaced): Escape cancels,
  // and Tab is trapped inside the dialog so focus can't wander onto the live
  // controls behind the overlay.
  const onKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === 'Escape') {
      e.stopPropagation();
      settle(false);
      return;
    }
    if (e.key === 'Tab' && boxRef.current) {
      const focusables = boxRef.current.querySelectorAll<HTMLElement>('button');
      if (focusables.length === 0) return;
      const first = focusables[0];
      const last = focusables[focusables.length - 1];
      if (e.shiftKey && document.activeElement === first) {
        e.preventDefault();
        last.focus();
      } else if (!e.shiftKey && document.activeElement === last) {
        e.preventDefault();
        first.focus();
      }
    }
  };

  const dialog = opts ? (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center p-4"
      role="dialog"
      aria-modal="true"
      aria-label={opts.title}
      onKeyDown={onKeyDown}
    >
      <div className="absolute inset-0 bg-black/60 backdrop-blur-sm" onClick={() => settle(false)} />
      <div ref={boxRef} className="relative bg-card border border-border rounded-2xl shadow-xl w-full max-w-sm p-6">
        <h3 className="text-lg font-bold text-foreground mb-2">{opts.title}</h3>
        <div className="text-sm text-muted-foreground mb-6">{opts.body}</div>
        <div className="flex gap-2">
          <button
            autoFocus
            onClick={() => settle(false)}
            className="flex-1 px-4 py-2 border border-border rounded-xl text-sm font-medium hover:bg-accent transition"
          >
            {opts.cancelLabel ?? 'Cancel'}
          </button>
          <button
            onClick={() => settle(true)}
            className={`flex-1 px-4 py-2 rounded-xl text-sm font-bold transition ${
              (opts.tone ?? 'danger') === 'danger'
                ? 'bg-red-500/20 text-red-500 border border-red-500/50 hover:bg-red-500/30'
                : 'bg-primary text-primary-foreground hover:opacity-90'
            }`}
          >
            {opts.confirmLabel ?? 'Confirm'}
          </button>
        </div>
      </div>
    </div>
  ) : null;

  return { confirm, dialog };
}
