import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { inviteMember, getRoleOptions, type RoleOption } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { prettyRole } from '../../lib/roles';
import Modal from '../common/Modal';

interface Props {
  onClose: () => void;
  onInvited: () => void;
}

export default function InviteMemberModal({ onClose, onInvited }: Props) {
  const { hasCapability } = useAuth();
  const [email, setEmail] = useState('');
  const [roles, setRoles] = useState<RoleOption[]>([]);
  const [roleId, setRoleId] = useState('');
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState('');
  const [successLink, setSuccessLink] = useState('');

  // Roles are fetched dynamically (P6) so custom roles appear alongside the system
  // ones; the owner role is never an invite target (ownership is transferred).
  useEffect(() => {
    getRoleOptions()
      .then((rs) => {
        setRoles(rs);
        const def = rs.find((r) => r.name === 'viewer' && !r.is_owner) ?? rs.find((r) => !r.is_owner);
        if (def) setRoleId(def.id);
      })
      .catch(() => setRoles([]));
  }, []);

  const handleSubmit = async (e: React.FormEvent) => {
    e.preventDefault();
    if (successLink) {
      onClose();
      return;
    }
    if (!roleId) {
      setError('Select a role for the new member.');
      return;
    }

    setError('');
    setLoading(true);
    try {
      const { debug_token } = await inviteMember(email, roleId);
      onInvited();
      // The debug accept-link is a dev convenience; even if a misconfigured
      // backend returns the token, a production build never renders it.
      if (debug_token && import.meta.env.DEV) {
        setSuccessLink(`${window.location.origin}/accept-invite?token=${debug_token}`);
      } else {
        onClose();
      }
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to send invite');
    } finally {
      setLoading(false);
    }
  };

  return (
    // Shared Radix modal (U7): Escape, focus trap/restore and aria for free.
    // Dismissal is blocked while the invite is being sent.
    <Modal
      open
      onClose={onClose}
      title={successLink ? 'Invite Sent' : 'Invite Team Member'}
      size="md"
      dismissable={!loading}
    >
      <>
        {error && (
          <div className="mb-4 p-3 rounded-xl bg-red-500/10 border border-red-500/20 text-red-400 text-sm">
            {error}
          </div>
        )}

        {successLink ? (
          <div className="space-y-4">
            <div className="p-4 rounded-xl bg-blue-500/10 border border-blue-500/20 text-blue-400 text-sm break-all font-mono">
              {successLink}
            </div>
            <p className="text-sm text-muted-foreground">
              You're running a local build, so no email went out — share this link directly, or
              open it to walk through the accept flow yourself.
            </p>
            <div className="flex gap-3 pt-2">
              <button
                type="button"
                onClick={onClose}
                className="flex-1 px-4 py-2.5 border border-border rounded-xl text-sm font-medium text-muted-foreground hover:bg-accent transition-colors"
              >
                Close
              </button>
              <a
                href={successLink}
                target="_blank"
                rel="noreferrer"
                className="flex-1 text-center px-4 py-2.5 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity"
              >
                Open Link
              </a>
            </div>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <label htmlFor="invite-email" className="block text-sm font-medium text-muted-foreground mb-1.5">
                Email Address
              </label>
              <input
                id="invite-email"
                type="email"
                value={email}
                onChange={e => setEmail(e.target.value)}
                required
                className="w-full px-4 py-3 bg-background border border-border rounded-xl text-foreground placeholder-muted-foreground focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary transition-all"
                placeholder="colleague@company.com"
              />
            </div>

            <div>
              <label htmlFor="invite-role" className="block text-sm font-medium text-muted-foreground mb-1.5">
                Role
              </label>
              <select
                id="invite-role"
                value={roleId}
                onChange={e => setRoleId(e.target.value)}
                className="w-full px-4 py-3 bg-background border border-border rounded-xl text-foreground focus:outline-none focus:ring-2 focus:ring-primary/50 focus:border-primary transition-all"
              >
                {roles.map(r => (
                  <option key={r.id} value={r.id} disabled={r.is_owner}>
                    {prettyRole(r.name)}{r.is_owner ? ' — transfer ownership instead' : ''}
                  </option>
                ))}
              </select>
              {/* What the picked role means, right where it's being assigned
                  (U3.3) — description from the catalog, plus a jump into the
                  role's detail page for admins who can open it. */}
              {(() => {
                const selected = roles.find((r) => r.id === roleId);
                if (!selected) return null;
                return (
                  <p className="mt-1.5 text-xs text-muted-foreground">
                    {selected.description || 'No description for this role yet.'}{' '}
                    {hasCapability('roles.manage') && (
                      <Link to={`/settings/roles/${selected.id}`} className="text-blue-500 hover:underline whitespace-nowrap">
                        What does this grant?
                      </Link>
                    )}
                  </p>
                );
              })()}
            </div>

            <div className="flex gap-3 pt-2">
              <button
                type="button"
                onClick={onClose}
                className="flex-1 px-4 py-2.5 border border-border rounded-xl text-sm font-medium text-muted-foreground hover:bg-accent transition-colors"
              >
                Cancel
              </button>
              <button
                type="submit"
                disabled={loading}
                className="flex-1 px-4 py-2.5 bg-primary text-primary-foreground rounded-xl text-sm font-semibold hover:opacity-90 transition-opacity disabled:opacity-50"
              >
                {loading ? 'Sending...' : 'Send Invite'}
              </button>
            </div>
          </form>
        )}
      </>
    </Modal>
  );
}
