import { useEffect, useState } from 'react';
import { useAuth } from '../lib/auth';
import { getMyInvitations, type IncomingInvitation } from '../lib/api';
import { prettyRole } from '../lib/roles';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { Inbox, LogOut, Loader2, ArrowRight, Building2 } from 'lucide-react';
import { Button, Input } from '@/components/ui';
import { markTemplatePickerPending } from '../features/onboarding/templatePickerHandoff';

/**
 * The R2 zero-membership dead-end (P4). A user who authenticates but belongs to
 * no active workspace used to get a token bound to uuid.Nil and silent 403s
 * everywhere; now they land here with an explanation instead.
 *
 * U4 item 6: this is also where a brand-new Google invitee lands (no junk personal
 * org is forked for them) — so we surface the invitations addressed to their email
 * for EXPLICIT acceptance (never a silent auto-join), alongside the create-your-own
 * path. Accepting joins that workspace and lands them in it.
 */
export default function NoWorkspacePage() {
  useDocumentTitle('No Workspace');
  const { user, logout, createWorkspace, acceptMyInvitation } = useAuth();

  // Pending invitations addressed to this user's email (U4 item 6).
  const [invites, setInvites] = useState<IncomingInvitation[]>([]);
  const [invitesLoading, setInvitesLoading] = useState(true);
  const [invitesError, setInvitesError] = useState(false);
  const [acceptingId, setAcceptingId] = useState<string | null>(null);

  useEffect(() => {
    let cancelled = false;
    getMyInvitations()
      .then((list) => { if (!cancelled) { setInvites(list); setInvitesError(false); } })
      // Distinguish a fetch error from a genuinely-empty list: collapsing both to
      // "no invites" would tell a real invitee (a transient blip on this one call)
      // they have none and nudge them to create the junk workspace this feature
      // exists to prevent. On error we flag it and prompt a reload instead.
      .catch(() => { if (!cancelled) setInvitesError(true); })
      .finally(() => { if (!cancelled) setInvitesLoading(false); });
    return () => { cancelled = true; };
  }, []);

  // Inline create (U4) — the old "Create a workspace" link went to /register,
  // which PublicRoute bounced right back here for an already-authenticated user
  // (the redirect loop). Creating in place via POST /workspaces switches them
  // straight into the new workspace.
  const [name, setName] = useState('');
  const [creating, setCreating] = useState(false);
  const [error, setError] = useState('');

  const accept = async (inv: IncomingInvitation) => {
    setError('');
    setAcceptingId(inv.id);
    try {
      await acceptMyInvitation(inv.id);
      // saveAuth switched us into the joined workspace; go to the app.
      window.location.assign('/');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to accept invitation');
      setAcceptingId(null);
    }
  };

  const submit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (!name.trim()) return;
    setCreating(true);
    setError('');
    try {
      await createWorkspace({ name: name.trim() });
      // A brand-new workspace is exactly where a starter template pays off, so
      // arm the picker to open once we land. Not done on the accept-invitation
      // path above: that workspace is already someone else's, configured.
      markTemplatePickerPending();
      // saveAuth switched the active org; go to the app.
      window.location.assign('/');
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to create workspace');
      setCreating(false);
    }
  };
  // If a refresh 409 bounced us here off an org we just lost (and it was our only
  // one), name it so the message isn't a mysterious "you're in no workspace".
  const [lostWorkspace] = useState<string | null>(() => {
    const name = sessionStorage.getItem('lost_workspace_name');
    if (name) sessionStorage.removeItem('lost_workspace_name');
    return name;
  });

  const hasInvites = invites.length > 0;
  const busy = acceptingId !== null || creating;

  return (
    <div className="min-h-screen bg-muted/30 flex flex-col items-center justify-center px-4 py-10">
      <div className="w-full max-w-md text-center">
        <div className="w-16 h-16 mx-auto mb-6 rounded-xl border border-border bg-card flex items-center justify-center">
          <Inbox className="w-8 h-8 text-muted-foreground" />
        </div>
        <h1 className="text-2xl font-semibold tracking-tight text-foreground mb-2">
          {hasInvites ? "You've been invited" : "You're not in any workspace"}
        </h1>
        {hasInvites ? (
          <p className="text-sm text-muted-foreground mb-6">
            {user?.email && <>You're signed in as <span className="font-medium text-foreground">{user.email}</span>. </>}
            Join a workspace you've been invited to, or create your own.
          </p>
        ) : (
          <>
            <p className="text-sm text-muted-foreground mb-2">
              {lostWorkspace ? (
                <>You no longer have access to <span className="font-medium text-foreground">{lostWorkspace}</span>, and you're </>
              ) : user?.email ? (
                <>You're signed in as <span className="font-medium text-foreground">{user.email}</span>, but you're </>
              ) : (
                'You '
              )}
              not currently a member of any active workspace.
            </p>
            <p className="text-sm text-muted-foreground mb-8">
              Ask a workspace admin to invite you, or create your own to get started.
            </p>
          </>
        )}

        {invitesError && (
          <div className="rounded-lg border border-amber-500/30 bg-amber-500/10 px-3 py-2 text-sm text-amber-600 dark:text-amber-400 mb-4 text-left">
            We couldn't check your invitations right now.{' '}
            <button type="button" onClick={() => window.location.reload()} className="underline hover:no-underline">
              Reload to try again
            </button>{' '}
            before creating a new workspace.
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-destructive/30 bg-destructive/10 px-3 py-2 text-sm text-destructive mb-4 text-left">{error}</div>
        )}

        {invitesLoading ? (
          <div className="flex items-center justify-center gap-2 text-sm text-muted-foreground py-6">
            <Loader2 className="w-4 h-4 animate-spin" /> Checking for invitations…
          </div>
        ) : (
          hasInvites && (
            <div className="flex flex-col gap-3 mb-6">
              {invites.map((inv) => (
                <div
                  key={inv.id}
                  className="flex items-center gap-3 rounded-lg border border-border bg-card p-4 text-left hover:bg-accent transition-colors"
                >
                  <div className="w-9 h-9 shrink-0 rounded-lg bg-primary/10 flex items-center justify-center">
                    <Building2 className="w-4 h-4 text-primary" />
                  </div>
                  <div className="min-w-0 flex-1">
                    <div className="text-foreground font-medium truncate">{inv.org_name}</div>
                    {inv.role_name && (
                      <div className="text-xs text-muted-foreground">Join as {prettyRole(inv.role_name)}</div>
                    )}
                  </div>
                  <Button
                    type="button"
                    onClick={() => accept(inv)}
                    disabled={busy}
                    className="shrink-0"
                  >
                    {acceptingId === inv.id ? <Loader2 className="animate-spin" /> : 'Join'}
                  </Button>
                </div>
              ))}
            </div>
          )
        )}

        <form onSubmit={submit} className="flex flex-col gap-3">
          {hasInvites && (
            <div className="flex items-center gap-3 text-xs text-muted-foreground my-1">
              <span className="h-px flex-1 bg-border" /> or create your own <span className="h-px flex-1 bg-border" />
            </div>
          )}
          <Input
            value={name}
            onChange={(e) => setName(e.target.value)}
            placeholder="Name your workspace"
            aria-label="Workspace name"
          />
          <Button type="submit" variant="secondary" disabled={busy || !name.trim()} className="w-full">
            {creating ? <><Loader2 className="animate-spin" /> Creating…</> : <>Create a workspace <ArrowRight /></>}
          </Button>
          <button
            type="button"
            onClick={() => logout()}
            className="mx-auto flex items-center gap-2 text-sm text-muted-foreground hover:text-foreground transition-colors"
          >
            <LogOut className="w-4 h-4" /> Sign out
          </button>
        </form>
      </div>
    </div>
  );
}
