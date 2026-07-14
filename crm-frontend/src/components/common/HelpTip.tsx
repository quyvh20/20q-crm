import * as Popover from '@radix-ui/react-popover';
import { CircleQuestionMark } from 'lucide-react';
import type { ReactNode } from 'react';

// The app's one contextual-help primitive (U7.5). There was no tooltip/popover
// primitive before this, so every explanation had to be shipped as permanent body
// copy (or, more often, not shipped at all).
//
// Deliberately a POPOVER, not a tooltip: a hover tooltip is invisible to touch
// users, dismisses the moment the pointer leaves, and can't hold a link or a list.
// A popover is a real disclosure — click or Enter/Space to open, Escape or an
// outside click to close, focus moves into the panel — which is what a
// multi-sentence explanation needs. Radix supplies all of that behaviour; the
// trigger carries an accessible NAME (`label`) so a screen reader announces what
// the "?" is about instead of just "button".
//
//   <HelpTip label="How permissions fit together" title="How this fits together">
//     <p>…</p>
//   </HelpTip>

export interface HelpTipProps {
  /** Accessible name of the "?" button — say what it explains, e.g.
   *  "How permissions fit together". Never just "Help". */
  label: string;
  /** Optional bold heading inside the panel. */
  title?: string;
  /** The explanation. Keep it short and jargon-free. */
  children: ReactNode;
  side?: 'top' | 'right' | 'bottom' | 'left';
  align?: 'start' | 'center' | 'end';
  /** Extra classes on the trigger (e.g. margins). */
  className?: string;
}

export default function HelpTip({
  label,
  title,
  children,
  side = 'bottom',
  align = 'start',
  className = '',
}: HelpTipProps) {
  return (
    <Popover.Root>
      <Popover.Trigger asChild>
        <button
          type="button"
          aria-label={label}
          className={`inline-flex h-5 w-5 shrink-0 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-accent hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${className}`.trim()}
        >
          <CircleQuestionMark className="h-4 w-4" aria-hidden="true" />
        </button>
      </Popover.Trigger>
      <Popover.Portal>
        <Popover.Content
          side={side}
          align={align}
          sideOffset={6}
          collisionPadding={12}
          className="z-50 w-80 max-w-[calc(100vw-2rem)] rounded-lg border border-border bg-popover p-3.5 text-popover-foreground shadow-lg focus:outline-none"
        >
          {title && <p className="mb-1.5 text-sm font-semibold text-foreground">{title}</p>}
          <div className="space-y-2 text-xs leading-relaxed text-muted-foreground">{children}</div>
          <Popover.Arrow className="fill-border" />
        </Popover.Content>
      </Popover.Portal>
    </Popover.Root>
  );
}
