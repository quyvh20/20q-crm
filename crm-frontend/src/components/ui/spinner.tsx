import { Loader2 } from "lucide-react";
import { cn } from "@/lib/utils";

interface SpinnerProps extends React.HTMLAttributes<HTMLDivElement> {
  size?: "sm" | "default" | "lg";
  /** Optional text rendered next to the spinner. */
  label?: string;
}

const sizeClass = {
  sm: "h-4 w-4",
  default: "h-5 w-5",
  lg: "h-8 w-8",
} as const;

/** The app-wide loading indicator — one spinner, token colors. */
function Spinner({ size = "default", label, className, ...props }: SpinnerProps) {
  return (
    <div role="status" className={cn("inline-flex items-center gap-2 text-muted-foreground", className)} {...props}>
      <Loader2 aria-hidden className={cn("animate-spin text-primary", sizeClass[size])} />
      {label ? <span className="text-sm">{label}</span> : <span className="sr-only">Loading…</span>}
    </div>
  );
}

/** Full-area centered variant for page/section loads. */
function SpinnerBlock({ label, className, ...props }: Omit<SpinnerProps, "size">) {
  return (
    <div className={cn("flex items-center justify-center py-16", className)} {...props}>
      <Spinner size="lg" label={label} />
    </div>
  );
}

export { Spinner, SpinnerBlock };
