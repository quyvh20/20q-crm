import { useState } from 'react';
import { Send } from 'lucide-react';

// A slide-to-confirm send guard: the user must drag the thumb all the way to fire
// onConfirm — a real "are you sure" for an irreversible bulk send (a stray click can't
// launch). Keyboard-accessible: arrow keys move the thumb; releasing below the end
// snaps back. Uses a native range input so focus/keyboard/touch all work; a fill +
// label overlay give it the slide-to-send look.
interface Props {
  label?: string;
  confirmingLabel?: string;
  disabled?: boolean;
  confirming?: boolean; // parent's in-flight state (keeps it locked after firing)
  onConfirm: () => void;
}

export default function SlideToConfirm({
  label = 'Slide to send', confirmingLabel = 'Sending…', disabled = false, confirming = false, onConfirm,
}: Props) {
  const [val, setVal] = useState(0);
  const locked = disabled || confirming;

  const settle = () => {
    if (val >= 100 && !confirming) {
      onConfirm();
    }
    // Snap back unless the parent has taken over (confirming) — so a partial drag
    // never leaves the thumb stranded, and a completed drag resets for next time.
    if (!confirming) setVal(0);
  };

  return (
    <div className={`relative h-12 w-full select-none overflow-hidden rounded-full border border-border bg-muted ${locked ? 'opacity-70' : ''}`}>
      {/* progress fill */}
      <div
        className="pointer-events-none absolute inset-y-0 left-0 bg-primary/25 transition-[width]"
        style={{ width: `${confirming ? 100 : val}%` }}
      />
      {/* centered label */}
      <div className="pointer-events-none absolute inset-0 flex items-center justify-center gap-2 text-sm font-medium text-foreground">
        <Send aria-hidden className="h-4 w-4" />
        {confirming ? confirmingLabel : label}
      </div>
      <input
        type="range"
        min={0}
        max={100}
        value={confirming ? 100 : val}
        disabled={locked}
        aria-label={label}
        onChange={(e) => setVal(Number(e.target.value))}
        onPointerUp={settle}
        onMouseUp={settle}
        onTouchEnd={settle}
        onKeyUp={settle}
        className="slide-to-confirm absolute inset-0 h-full w-full cursor-grab appearance-none bg-transparent disabled:cursor-not-allowed"
      />
    </div>
  );
}
