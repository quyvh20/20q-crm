import * as Dialog from '@radix-ui/react-dialog';
import { X } from 'lucide-react';
import { useRef, type ReactNode } from 'react';

// The app's ONE modal primitive (U7 a11y). Every overlay used to be a hand-rolled
// `fixed inset-0` div: no Escape, no focus trap, no focus restore, no aria — a
// keyboard user could Tab straight out of a form onto the page behind it and had
// no way to dismiss without a mouse. Radix gives all of that for free (portal,
// scroll lock, focus trap, focus restore, Escape, aria-modal/labelledby), so new
// modals should compose this instead of re-inventing the overlay.
//
//   <Modal open={open} onClose={close} title="New Deal" size="lg">…</Modal>
//
// Styling deliberately mirrors what the hand-rolled overlays looked like
// (bg-card / border-border / rounded-2xl), so converting a modal is invisible to
// the eye and only changes its keyboard behaviour.

type ModalSize = 'sm' | 'md' | 'lg' | 'xl' | '2xl' | '3xl';

const SIZE: Record<ModalSize, string> = {
  sm: 'max-w-sm',
  md: 'max-w-md',
  lg: 'max-w-lg',
  xl: 'max-w-xl',
  // The wide end exists for panels that carry a side-by-side body (the AI
  // composer's form|preview grid, a prompt dump) — clamping those to xl would
  // reflow the columns, and a conversion is supposed to be invisible.
  '2xl': 'max-w-2xl',
  '3xl': 'max-w-3xl',
};

export interface ModalProps {
  open: boolean;
  /** Called for every dismissal path: the X, Escape, and an outside click. */
  onClose: () => void;
  title: ReactNode;
  /** Rendered as the dialog's aria-description. Any ReactNode (rendered in a div). */
  description?: ReactNode;
  children?: ReactNode;
  size?: ModalSize;
  /** 'center' = centered card; 'drawer' = full-height panel on a screen edge. */
  variant?: 'center' | 'drawer';
  /** Which edge a drawer is anchored to. Ignored when variant='center'. */
  side?: 'left' | 'right';
  /** Width override for panels whose width is a fixed design constant rather
   *  than a step on the size scale (the mobile nav is w-72, capped at 85vw so a
   *  strip of scrim stays tappable on a narrow phone). Replaces `size`. */
  widthClass?: string;
  /** Drop the built-in header row — no visible title, no X — and expose the title
   *  to screen readers only. For surfaces that own their chrome: the mobile nav
   *  already carries its own brand block and close button. The title is still
   *  REQUIRED; it's what names the dialog for assistive tech. */
  hideHeader?: boolean;
  /** Wrap children in the standard body padding. Turn off when the content owns
   *  its own edge-to-edge layout (a form with a full-bleed footer bar). */
  padded?: boolean;
  /** Hide the close X — for dialogs whose buttons ARE the only exits (confirm). */
  hideClose?: boolean;
  /** Set false to block Escape / outside-click dismissal (e.g. mid-mutation). */
  dismissable?: boolean;
  /** Extra classes on the dialog panel itself. */
  className?: string;
}

export default function Modal({
  open,
  onClose,
  title,
  description,
  children,
  size = 'md',
  variant = 'center',
  side = 'right',
  widthClass,
  hideHeader = false,
  padded = true,
  hideClose = false,
  dismissable = true,
  className = '',
}: ModalProps) {
  const isDrawer = variant === 'drawer';
  // Focus restore is on us: Radix's modal Content re-focuses its <Dialog.Trigger>
  // on close, and these dialogs are opened from ordinary buttons via an `open`
  // prop — no Trigger, so its restore is a no-op and focus would fall to <body>.
  // onOpenAutoFocus fires before the focus scope moves focus, so activeElement is
  // still whatever opened us.
  const restoreTo = useRef<HTMLElement | null>(null);

  const edge = side === 'left' ? 'left-0 border-r' : 'right-0 border-l';
  const panel = isDrawer
    ? `fixed ${edge} top-0 z-50 flex h-full ${widthClass ?? `w-full ${SIZE[size]}`} flex-col overflow-y-auto border-border bg-card text-card-foreground shadow-2xl focus:outline-none`
    : `fixed left-1/2 top-1/2 z-50 ${widthClass ?? `w-[calc(100%-2rem)] ${SIZE[size]}`} max-h-[90vh] -translate-x-1/2 -translate-y-1/2 overflow-y-auto rounded-2xl border border-border bg-card text-card-foreground shadow-xl focus:outline-none`;

  const header = isDrawer
    ? 'sticky top-0 z-10 flex items-start justify-between gap-3 border-b border-border bg-card px-6 py-4'
    : 'flex items-start justify-between gap-3 px-6 pt-6 pb-4';

  return (
    <Dialog.Root
      open={open}
      onOpenChange={(next) => {
        if (!next) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="fixed inset-0 z-50 bg-black/50 backdrop-blur-sm" />
        <Dialog.Content
          className={`${panel} ${className}`.trim()}
          aria-modal="true"
          onOpenAutoFocus={(e) => {
            const active = document.activeElement;
            // Ignore an autoFocus'd control inside the panel — React commits that
            // before this fires, and it isn't the element we should return to.
            const inside = active instanceof Node && e.currentTarget instanceof Node && e.currentTarget.contains(active);
            restoreTo.current = !inside && active instanceof HTMLElement ? active : null;
          }}
          onCloseAutoFocus={(e) => {
            // preventDefault also skips Radix's own (no-op) trigger restore.
            e.preventDefault();
            const el = restoreTo.current;
            if (el && document.contains(el)) el.focus();
          }}
          onEscapeKeyDown={(e) => {
            if (!dismissable) e.preventDefault();
          }}
          onInteractOutside={(e) => {
            if (!dismissable) e.preventDefault();
          }}
          // Radix warns when a dialog has no description; opting out explicitly
          // (rather than shipping an empty one) keeps the console clean.
          {...(description === undefined ? { 'aria-describedby': undefined } : {})}
        >
          {/* hideHeader still emits Title/Description — Radix needs them to name
              and describe the dialog — just visually hidden. */}
          {hideHeader ? (
            <>
              <Dialog.Title className="sr-only">{title}</Dialog.Title>
              {description !== undefined && (
                <Dialog.Description asChild>
                  <div className="sr-only">{description}</div>
                </Dialog.Description>
              )}
            </>
          ) : (
            <div className={header}>
              <div className="min-w-0">
                {/* wrap, never truncate: a clipped title is a lost question. */}
                <Dialog.Title className="break-words text-lg font-semibold text-foreground">{title}</Dialog.Title>
                {description !== undefined && (
                  // asChild + div: callers pass rich ReactNode bodies, which are
                  // illegal inside the <p> Radix renders by default.
                  <Dialog.Description asChild>
                    <div className="mt-1.5 text-sm text-muted-foreground">{description}</div>
                  </Dialog.Description>
                )}
              </div>
              {!hideClose && (
                <Dialog.Close
                  aria-label="Close"
                  className="-mr-1.5 -mt-1 shrink-0 rounded-md p-1.5 text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
                >
                  <X className="h-[18px] w-[18px]" />
                </Dialog.Close>
              )}
            </div>
          )}

          {padded ? <div className="px-6 pb-6">{children}</div> : children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
