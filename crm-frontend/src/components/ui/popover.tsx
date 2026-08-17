import { useEffect, useRef, type ReactNode } from 'react';

// Popover is a minimal, dependency-free anchored panel: the caller owns the
// open state (controlled), renders its own trigger, and the panel closes on
// outside pointer-down or Escape. Deliberately NOT a focus trap — it is a
// disclosure, not a modal — so keyboard users can tab out naturally; Escape
// returns nothing because the caller decides where focus should land.
//
// Positioning is CSS-only (absolute, below the trigger) rather than a
// floating-ui port: filter panels anchor to a toolbar near the top of the
// page, so collision handling has nothing to earn yet.
export function Popover({
  open,
  onClose,
  align = 'start',
  className = '',
  children,
}: {
  open: boolean;
  onClose: () => void;
  /** Horizontal anchoring relative to the trigger wrapper. */
  align?: 'start' | 'end';
  className?: string;
  children: ReactNode;
}) {
  const ref = useRef<HTMLDivElement>(null);

  useEffect(() => {
    if (!open) return;
    const onPointerDown = (e: PointerEvent) => {
      // The wrapper (trigger + panel) is the inside; anything else closes.
      const root = ref.current?.parentElement;
      if (root && e.target instanceof Node && !root.contains(e.target)) onClose();
    };
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    document.addEventListener('pointerdown', onPointerDown);
    document.addEventListener('keydown', onKey);
    return () => {
      document.removeEventListener('pointerdown', onPointerDown);
      document.removeEventListener('keydown', onKey);
    };
  }, [open, onClose]);

  if (!open) return null;
  return (
    <div
      ref={ref}
      role="dialog"
      className={`absolute top-full z-30 mt-1 rounded-lg border border-border bg-popover p-3 text-popover-foreground shadow-lg ${
        align === 'end' ? 'right-0' : 'left-0'
      } ${className}`}
    >
      {children}
    </div>
  );
}

// PopoverAnchor is the relative wrapper a Popover positions against. Split out
// so call sites read as <PopoverAnchor><Trigger/><Popover/></PopoverAnchor>.
export function PopoverAnchor({ className = '', children }: { className?: string; children: ReactNode }) {
  return <div className={`relative ${className}`}>{children}</div>;
}
