import { Link } from 'react-router-dom';
import { ArrowLeft, FileWarning } from 'lucide-react';
import { APP_NAME } from '../../lib/useDocumentTitle';

// Shared chrome for the in-app legal pages (U7.6).
//
// These are the FALLBACK destinations: they render only when the operator has not
// pointed VITE_TERMS_URL / VITE_PRIVACY_URL at their real, lawyer-written pages.
// They therefore must not pretend to BE terms — inventing plausible-sounding legal
// text is worse than having none, because it reads as binding to a user and to a
// court while protecting nobody. So the page states plainly what it is: a template
// the operator has to replace, plus the outline of what belongs here.
//
// Routed PUBLICLY but deliberately NOT behind PublicRoute — a signed-in user must
// still be able to read the terms they agreed to, and PublicRoute would bounce
// them to the dashboard.

/** The date this placeholder copy was last touched. Operators replacing the
 *  content should bump it (or, better, point the env vars at their real pages). */
export const LEGAL_LAST_UPDATED = 'July 14, 2026';

interface LegalPageShellProps {
  heading: string;
  /** What a completed version of this document needs to cover. */
  outline: string[];
  children?: React.ReactNode;
}

export default function LegalPageShell({ heading, outline, children }: LegalPageShellProps) {
  return (
    <div className="min-h-screen bg-background text-foreground">
      <div className="mx-auto max-w-3xl px-6 py-12 sm:py-16">
        <Link
          to="/login"
          className="mb-8 inline-flex items-center gap-1.5 text-sm font-medium text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" />
          Back to sign in
        </Link>

        <h1 className="text-3xl font-bold tracking-tight">{heading}</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          {APP_NAME} · Last updated {LEGAL_LAST_UPDATED}
        </p>

        {/* The honesty banner. This is the point of the page. */}
        <div className="mt-8 flex gap-3 rounded-xl border border-amber-500/30 bg-amber-500/10 p-4">
          <FileWarning className="mt-0.5 h-5 w-5 shrink-0 text-amber-600 dark:text-amber-400" />
          <div className="text-sm">
            <p className="font-semibold text-amber-700 dark:text-amber-300">
              This is a placeholder, not a legal agreement.
            </p>
            <p className="mt-1 text-amber-700/90 dark:text-amber-200/90">
              {APP_NAME} ships without pre-written legal copy on purpose: terms depend on who is
              operating the product, where, and under which laws — none of which a template can
              know. Whoever runs this workspace must replace this page with a document reviewed by
              their own counsel, or point <code className="rounded bg-black/10 px-1 py-0.5 font-mono text-xs dark:bg-white/10">VITE_TERMS_URL</code>{' '}
              and <code className="rounded bg-black/10 px-1 py-0.5 font-mono text-xs dark:bg-white/10">VITE_PRIVACY_URL</code>{' '}
              at the real ones.
            </p>
          </div>
        </div>

        {children && <div className="mt-8 text-sm leading-relaxed text-muted-foreground">{children}</div>}

        <div className="mt-8">
          <h2 className="text-lg font-semibold">What this document needs to cover</h2>
          <ul className="mt-3 space-y-2">
            {outline.map((item) => (
              <li key={item} className="flex gap-2.5 text-sm text-muted-foreground">
                <span aria-hidden="true" className="mt-2 h-1.5 w-1.5 shrink-0 rounded-full bg-muted-foreground/50" />
                <span>{item}</span>
              </li>
            ))}
          </ul>
        </div>

        <p className="mt-10 border-t border-border pt-6 text-xs text-muted-foreground">
          Questions about this page belong to the operator of this {APP_NAME} workspace, not to the
          software itself.
        </p>
      </div>
    </div>
  );
}
