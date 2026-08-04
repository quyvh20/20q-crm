import * as React from "react";
import { AlertTriangle, RefreshCw } from "lucide-react";
import { cn } from "@/lib/utils";
import { Button } from "./button";

interface ErrorStateProps extends Omit<React.HTMLAttributes<HTMLDivElement>, "title"> {
  /** Headline. Say what failed to load, not "Error". */
  title?: React.ReactNode;
  /**
   * The thrown value, rendered as its message. Every api call throws an
   * `ApiError` whose message already carries the server's own correlation id
   * ("… (reference: 9f3c1a2b)"), so passing the raw error through is what puts
   * that reference in front of the user.
   */
  error?: unknown;
  /** Shown when `error` carries no usable message (network abort, non-Error throw). */
  fallback?: string;
  /** Re-runs the failed request. Omit when the caller offers its own retry. */
  onRetry?: () => void;
  retryLabel?: string;
  /** One-line inline banner instead of the full dashed block. */
  compact?: boolean;
}

function messageOf(error: unknown, fallback: string): string {
  if (error instanceof Error && error.message) return error.message;
  if (typeof error === "string" && error) return error;
  return fallback;
}

/**
 * ErrorState is the counterpart to EmptyState: "we could not load this",
 * as opposed to "there is nothing here".
 *
 * Keeping them distinct is the whole point. A list that catches a failed fetch
 * into an empty array renders "No deals yet. Click Add Deal to create one." —
 * advice that is wrong, unactionable, and indistinguishable from a brand-new
 * workspace. Anything that can render EmptyState off a fetch should be able to
 * render this instead.
 */
function ErrorState({
  title = "Something went wrong.",
  error,
  fallback = "The request failed. Check your connection and try again.",
  onRetry,
  retryLabel = "Retry",
  compact = false,
  className,
  ...props
}: ErrorStateProps) {
  const message = messageOf(error, fallback);

  if (compact) {
    return (
      <div
        role="alert"
        className={cn(
          "flex w-full flex-wrap items-center gap-x-2 gap-y-1 rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive",
          className,
        )}
        {...props}
      >
        <AlertTriangle aria-hidden className="h-4 w-4 shrink-0" />
        <span className="font-medium">{title}</span>
        <span className="text-destructive/80">{message}</span>
        {onRetry ? (
          <Button
            variant="ghost"
            size="sm"
            onClick={onRetry}
            className="ml-auto h-7 text-destructive hover:bg-destructive/10 hover:text-destructive"
          >
            <RefreshCw aria-hidden /> {retryLabel}
          </Button>
        ) : null}
      </div>
    );
  }

  return (
    <div
      role="alert"
      className={cn(
        "flex flex-col items-center justify-center rounded-xl border border-dashed border-destructive/40 bg-destructive/5 px-6 py-12 text-center",
        className,
      )}
      {...props}
    >
      <div className="mb-3 flex h-10 w-10 items-center justify-center rounded-full bg-destructive/10">
        <AlertTriangle aria-hidden className="h-5 w-5 text-destructive" />
      </div>
      <p className="text-sm font-medium text-foreground">{title}</p>
      <p className="mt-1 max-w-md text-sm text-muted-foreground">{message}</p>
      {onRetry ? (
        <Button variant="outline" size="sm" onClick={onRetry} className="mt-4">
          <RefreshCw aria-hidden /> {retryLabel}
        </Button>
      ) : null}
    </div>
  );
}

export { ErrorState };
