import { useEffect, useState } from 'react';
import { Link } from 'react-router-dom';
import { inviteMember, getRoleOptions, type RoleOption } from '../../lib/api';
import { useAuth } from '../../lib/auth';
import { prettyRole } from '../../lib/roles';
import Modal from '../common/Modal';
import { Button, buttonVariants, Input, Label, Select } from '@/components/ui';
import { cn } from '@/lib/utils';

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
          <div className="mb-4 rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
            {error}
          </div>
        )}

        {successLink ? (
          <div className="space-y-4">
            <div className="rounded-lg border border-primary/20 bg-primary/10 p-4 text-sm text-primary break-all font-mono">
              {successLink}
            </div>
            <p className="text-sm text-muted-foreground">
              You're running a local build, so no email went out — share this link directly, or
              open it to walk through the accept flow yourself.
            </p>
            <div className="flex gap-3 pt-2">
              <Button type="button" variant="outline" onClick={onClose} className="flex-1">
                Close
              </Button>
              <a href={successLink} target="_blank" rel="noreferrer" className={cn(buttonVariants(), 'flex-1')}>
                Open Link
              </a>
            </div>
          </div>
        ) : (
          <form onSubmit={handleSubmit} className="space-y-4">
            <div>
              <Label htmlFor="invite-email" className="mb-1.5 block text-sm">
                Email Address
              </Label>
              <Input
                id="invite-email"
                type="email"
                value={email}
                onChange={e => setEmail(e.target.value)}
                required
                placeholder="colleague@company.com"
              />
            </div>

            <div>
              <Label htmlFor="invite-role" className="mb-1.5 block text-sm">
                Role
              </Label>
              <Select
                id="invite-role"
                value={roleId}
                onChange={e => setRoleId(e.target.value)}
              >
                {roles.map(r => (
                  <option key={r.id} value={r.id} disabled={r.is_owner}>
                    {prettyRole(r.name)}{r.is_owner ? ' — transfer ownership instead' : ''}
                  </option>
                ))}
              </Select>
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
                      <Link to={`/settings/roles/${selected.id}`} className="text-primary hover:underline whitespace-nowrap">
                        What does this grant?
                      </Link>
                    )}
                  </p>
                );
              })()}
            </div>

            <div className="flex gap-3 pt-2">
              <Button type="button" variant="outline" onClick={onClose} className="flex-1">
                Cancel
              </Button>
              <Button type="submit" disabled={loading} className="flex-1">
                {loading ? 'Sending...' : 'Send Invite'}
              </Button>
            </div>
          </form>
        )}
      </>
    </Modal>
  );
}
