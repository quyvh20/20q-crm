import { AlertCircle, CheckCircle2, X } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "./button";
import { dismissToast, useToasts, type Toast } from "@/lib/useToast";

// The viewport for the toast store in lib/useToast.ts. Mounted ONCE, at the app
// root (App.tsx) — deliberately OUTSIDE the <Suspense> that wraps <Routes>, so a
// toast raised just before a lazy route transition is still on screen while the
// fallback is showing. Anything mounted inside AppLayout would unmount with it.
//
// ── Why two live regions, and not one ────────────────────────────────────────
// A region's politeness is read when the region is announced, and swapping
// aria-live on a live region at the moment its content changes is unreliable
// across screen readers. So each politeness gets its own permanently-mounted
// region and toasts are routed into the right one:
//
//   success → role="status" / aria-live="polite". "Saved" is not worth cutting
//     off whatever the user is currently having read to them; it can wait for
//     the next pause. Interrupting to confirm the expected is noise.
//   error → role="alert" / aria-live="assertive". The user's action did NOT
//     take effect. A polite announcement queues behind the rest of the current
//     utterance, which is long enough for someone to move on believing the save
//     worked. This is exactly the case assertive exists for.
//
// Both regions stay in the DOM when empty — that is the fix for the bug every
// hand-rolled version shipped, which mounted the region and its text in the
// same commit and so was frequently announced as nothing at all. aria-atomic is
// left false so only the newly inserted toast is read, not the whole stack.
//
// Errors render above confirmations, matching the announcement priority.

function ToastCard({ toast }: { toast: Toast }) {
  const isError = toast.tone === "error";
  const Icon = isError ? AlertCircle : CheckCircle2;

  return (
    <div
      className={cn(
        "pointer-events-auto flex items-start gap-3 rounded-lg border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg",
        isError ? "border-destructive/40" : "border-border",
      )}
    >
      <Icon
        aria-hidden
        className={cn("mt-0.5 h-4 w-4 shrink-0", isError ? "text-destructive" : "text-primary")}
      />
      {/* min-w-0 + break-words: a server error message can be long and must wrap
          rather than force the card wider than the viewport. */}
      <span className="min-w-0 flex-1 break-words">{toast.message}</span>
      {toast.action ? (
        <Button
          variant="link"
          size="sm"
          className="h-auto shrink-0 p-0 text-xs"
          onClick={() => {
            toast.action?.onClick();
            dismissToast(toast.id);
          }}
        >
          {toast.action.label}
        </Button>
      ) : null}
      {/* Auto-dismiss must never be the only exit: a magnified or screen-reader
          user can need longer than the timer, and a burst of toasts should be
          clearable. */}
      <Button
        variant="ghost"
        size="icon"
        aria-label={`Dismiss notification: ${toast.message}`}
        className="-mr-1.5 -mt-1 h-6 w-6 shrink-0 text-muted-foreground hover:text-foreground"
        onClick={() => dismissToast(toast.id)}
      >
        <X aria-hidden className="h-3.5 w-3.5" />
      </Button>
    </div>
  );
}

export function Toaster() {
  const toasts = useToasts();
  const errors = toasts.filter((t) => t.tone === "error");
  const confirmations = toasts.filter((t) => t.tone !== "error");

  return (
    // pointer-events-none on the stack so the empty regions never swallow clicks
    // on the page beneath; each card turns them back on for itself.
    <div className="pointer-events-none fixed right-4 top-4 z-[100] flex w-[calc(100vw-2rem)] max-w-sm flex-col gap-2">
      <div role="alert" aria-live="assertive" aria-atomic="false" className="flex flex-col gap-2">
        {errors.map((t) => (
          <ToastCard key={t.id} toast={t} />
        ))}
      </div>
      <div role="status" aria-live="polite" aria-atomic="false" className="flex flex-col gap-2">
        {confirmations.map((t) => (
          <ToastCard key={t.id} toast={t} />
        ))}
      </div>
    </div>
  );
}
