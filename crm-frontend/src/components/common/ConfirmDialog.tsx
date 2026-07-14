import { useCallback, useRef, useState, type ReactNode } from 'react';
import Modal from './Modal';

// The app's one confirmation dialog (U1.3) — replaces the window.confirm/alert
// mix across settings. Promise-based so call sites read like the native API:
//
//   const { confirm, dialog } = useConfirm();
//   ...
//   if (!(await confirm({ title: 'Remove member', body: '…', tone: 'danger' }))) return;
//   ...
//   return (<>{…}{dialog}</>);
//
// U7: the keyboard handling is now the shared Radix-backed <Modal> instead of a
// hand-rolled trap. That fixed three real defects at once: the old container had
// role="dialog" + onKeyDown but no tabIndex (so once focus landed on <body>,
// Escape stopped working AND Tab escaped to the live controls behind the
// overlay), focus was never restored after settling, and the trap only cycled
// `button` elements even though `body` is a ReactNode that can hold an input.

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

  const dialog = opts ? (
    // Escape, the overlay click and the focus trap/restore all come from Modal.
    <Modal
      open
      onClose={() => settle(false)}
      title={opts.title}
      description={opts.body}
      size="sm"
      hideClose
    >
      <div className="mt-2 flex gap-2">
        {/* No autoFocus: Radix already focuses the first tabbable control (this
            Cancel button), and an autoFocus lands before Modal captures the
            element to restore focus to on close. */}
        <button
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
    </Modal>
  ) : null;

  return { confirm, dialog };
}
