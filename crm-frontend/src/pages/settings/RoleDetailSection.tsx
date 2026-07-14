import { useState, useMemo } from 'react';
import { Link, useParams } from 'react-router-dom';
import { ArrowLeft, Check, Minus, Lock, AlertTriangle, Users, Pencil } from 'lucide-react';
import { ALL_CAPABILITIES, CAPABILITY_LABELS, type DataScope } from '../../lib/api';
import { useRoleAccess, useRolesCatalog, useSetRoleCapabilities, useUpdateRole } from '../../features/settings/queries';
import { useAuth } from '../../lib/auth';
import { prettyRole, SENSITIVE_CAPABILITIES } from '../../lib/roles';
import { useDocumentTitle } from '../../lib/useDocumentTitle';
import { useConfirm } from '../../components/common/ConfirmDialog';
import HelpTip from '../../components/common/HelpTip';

// Row scope in plain words (U6). 'team' is the third value: a team IS a user
// group, so a team-scoped member sees the records owned by everyone they share
// a group with — no separate "team" entity to maintain.
const SCOPE_SUMMARY: Record<DataScope, string> = {
  own: 'Only records they own, plus records shared with them.',
  team: 'Records owned by anyone on their teams, plus records shared with them.',
  all: 'All records in the workspace.',
};

const SCOPE_HELP: Record<DataScope, string> = {
  own: 'They see the records they own. Anything else must be shared with them one record at a time.',
  team: 'Teams are User Groups — a member sees every record owned by someone in a group they belong to. Manage them in Settings → User Groups.',
  all: 'They see every record of every object they have access to below.',
};

// RoleDetailSection is one role's whole story on a single page with a single
// pivot (U3.1): identity + capabilities + effective object/field access + data
// scope + layout routing + member count. The "What can this role see?" table is
// the U3.2 effective-access view — merged OLS bits (an object with no grant
// shows all-off, so the default of no access is visible, not implied) and field
// restrictions joined to display labels, answering "what would Jane actually
// see?" without a test account.
//
// Reads through react-query (U7.3): the page shares the roles cache with
// RolesManager, and every save invalidates BOTH this role's access payload and
// the roles list — so a rename here shows in the list, and an object grant saved
// on the OLS grid refreshes the effective-access table below.
export default function RoleDetailSection() {
  const { id = '' } = useParams();
  const { hasCapability } = useAuth();
  const [saveError, setSaveError] = useState('');
  // Edit-in-place for a custom role's name/description (built-ins stay fixed).
  const [editingIdentity, setEditingIdentity] = useState(false);
  const [editName, setEditName] = useState('');
  const [editDesc, setEditDesc] = useState('');
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

  const { data, isLoading, error: loadError } = useRoleAccess(id);
  const { data: rolesCatalog } = useRolesCatalog();
  const catalog = useMemo(() => rolesCatalog?.capabilities ?? [], [rolesCatalog]);
  const groups = useMemo(() => rolesCatalog?.groups ?? [], [rolesCatalog]);

  const capsMut = useSetRoleCapabilities();
  const updateMut = useUpdateRole();
  const busy = capsMut.isPending || updateMut.isPending;

  // Capabilities grouped for display; falls back to one flat group when the
  // catalog endpoint is unavailable.
  const grouped = useMemo(() => {
    if (catalog.length === 0) {
      return [{ group: 'Capabilities', caps: ALL_CAPABILITIES.map((code) => ({ code, label: CAPABILITY_LABELS[code] || code, description: '', group: '', sensitive: SENSITIVE_CAPABILITIES.has(code) })) }];
    }
    return groups
      .map((g) => ({ group: g, caps: catalog.filter((c) => c.group === g) }))
      .filter((s) => s.caps.length > 0);
  }, [catalog, groups]);

  const role = data?.role;

  // /settings/roles/:id isn't a SETTINGS_SECTIONS entry, so SettingsLayout leaves
  // the tab title to us (U7.2). Titled from the LOADED role name — never from
  // `editName`, the bound rename input, which would retitle the tab on every
  // keystroke. Null until it loads: the hook then shows the bare app name rather
  // than "undefined".
  useDocumentTitle(role?.name ? `${prettyRole(role.name)} · Roles · Settings` : null);

  // Layout routing per object, for the Layout column ("Default" when unset).
  const layoutBySlug = useMemo(() => {
    const m = new Map<string, string>();
    data?.layouts.forEach((l) => m.set(l.object_slug, l.layout_name));
    return m;
  }, [data]);

  const zeroRead = !!role && !role.is_owner && !!data && data.objects.length > 0 && data.objects.every((o) => !o.read);

  const toggleCapability = async (cap: string) => {
    if (!role || role.is_system) return;
    const has = role.capabilities.includes(cap);
    // Removing roles.manage is a big deal: everyone holding this role loses the
    // whole permissions surface. Name the consequence before saving; the server
    // additionally refuses to let you strip it from your OWN role (U0.5).
    if (has && cap === 'roles.manage') {
      const holders = role.member_count === 1 ? '1 member' : `${role.member_count} members`;
      if (!(await confirmDialog({
        title: 'Remove permission management?',
        body: `${role.member_count > 0 ? `${holders} holding the "${prettyRole(role.name)}" role` : `Members with the "${prettyRole(role.name)}" role`} will no longer be able to open the Roles, Object Access, or Field Access settings.`,
        confirmLabel: 'Remove it',
      }))) return;
    }
    const next = has ? role.capabilities.filter((c) => c !== cap) : [...role.capabilities, cap];
    setSaveError('');
    capsMut.mutate(
      { id: role.id, capabilities: next },
      { onError: (e) => setSaveError(e instanceof Error ? e.message : 'Failed to save capability') },
    );
  };

  const startEditIdentity = () => {
    if (!role || role.is_system) return;
    setEditName(role.name);
    setEditDesc(role.description || '');
    setEditingIdentity(true);
  };

  const saveIdentity = () => {
    if (!role || !editName.trim()) return;
    setSaveError('');
    updateMut.mutate(
      {
        id: role.id,
        patch: { name: editName.trim(), description: editDesc.trim() },
      },
      {
        onSuccess: () => setEditingIdentity(false),
        onError: (e) => setSaveError(e instanceof Error ? e.message : 'Failed to rename role'),
      },
    );
  };

  const changeScope = (scope: DataScope) => {
    if (!role || role.is_system) return;
    setSaveError('');
    updateMut.mutate(
      { id: role.id, patch: { data_scope: scope } },
      { onError: (e) => setSaveError(e instanceof Error ? e.message : 'Failed to change data access') },
    );
  };

  if (isLoading) return <div className="text-sm text-muted-foreground py-8">Loading role…</div>;

  if (loadError || !role || !data) {
    return (
      <div className="space-y-3">
        <Link to="/settings/roles" className="inline-flex items-center gap-1.5 text-sm text-blue-600 hover:underline">
          <ArrowLeft className="w-4 h-4" /> All roles
        </Link>
        <div className="bg-red-50 text-red-700 text-sm rounded-md px-3 py-2">
          {loadError instanceof Error ? loadError.message : 'Role not found.'}
        </div>
      </div>
    );
  }

  const memberCountText = `${role.member_count} member${role.member_count === 1 ? '' : 's'}`;
  const canSeeMembers = hasCapability('members.manage') || hasCapability('members.invite');

  return (
    <div className="space-y-5">
      {/* Breadcrumb + identity */}
      <div>
        <Link to="/settings/roles" className="inline-flex items-center gap-1.5 text-sm text-blue-600 hover:underline">
          <ArrowLeft className="w-4 h-4" /> All roles
        </Link>
        {editingIdentity ? (
          <div className="mt-2 space-y-2 max-w-md">
            <input
              value={editName}
              onChange={(e) => setEditName(e.target.value)}
              aria-label="Role name"
              className="w-full border rounded-md px-2.5 py-1.5 text-sm bg-background font-semibold"
            />
            <input
              value={editDesc}
              onChange={(e) => setEditDesc(e.target.value)}
              aria-label="Role description"
              placeholder="What this role is for (optional)"
              className="w-full border rounded-md px-2.5 py-1.5 text-sm bg-background"
            />
            <div className="flex gap-2">
              <button
                onClick={saveIdentity}
                disabled={busy || !editName.trim()}
                className="px-3 py-1.5 text-sm rounded-md bg-blue-500 text-white hover:bg-blue-600 disabled:opacity-50"
              >
                Save
              </button>
              <button
                onClick={() => setEditingIdentity(false)}
                disabled={busy}
                className="px-3 py-1.5 text-sm rounded-md border hover:bg-accent disabled:opacity-50"
              >
                Cancel
              </button>
            </div>
          </div>
        ) : (
          <>
            <div className="mt-2 flex items-center gap-2 flex-wrap">
              <h2 className="text-lg font-semibold">{prettyRole(role.name)}</h2>
              {/* The four layers on this page look independent but multiply
                  together, and nothing said so (U7.5). Plain words on purpose — an
                  earlier phase deliberately took the jargon out of this screen. */}
              <HelpTip label="How the permission settings on this page fit together" title="How these settings fit together">
                <p>Four things decide what someone with this role actually sees:</p>
                <ul className="ml-4 list-disc space-y-1">
                  <li><span className="font-medium text-foreground">What they can manage</span> — actions like inviting people or editing workflows.</li>
                  <li><span className="font-medium text-foreground">Which objects</span> — can they open contacts? deals? properties?</li>
                  <li><span className="font-medium text-foreground">Which fields</span> — some fields inside an object can be hidden or read-only.</li>
                  <li><span className="font-medium text-foreground">Which records</span> — only the ones they own, their teams', or all of them.</li>
                </ul>
                <p>
                  All four have to line up. Someone can have full access to contacts and still see an empty page if
                  no contacts are theirs — and a field they can't see stays hidden even on a record they own.
                </p>
              </HelpTip>
              {!role.is_system && (
                <button
                  onClick={startEditIdentity}
                  aria-label="Edit role name and description"
                  className="text-muted-foreground hover:text-foreground"
                >
                  <Pencil className="w-3.5 h-3.5" aria-hidden="true" />
                </button>
              )}
              {role.is_owner && (
                <span className="inline-flex items-center gap-1 text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                  <Lock className="w-3 h-3" aria-hidden="true" /> Full access
                </span>
              )}
              <span className="text-xs px-1.5 py-0.5 rounded bg-muted text-muted-foreground">
                {role.is_system ? 'Built-in role' : 'Custom role'}
              </span>
              {canSeeMembers ? (
                <Link
                  to={`/settings/members?role=${role.id}`}
                  className="inline-flex items-center gap-1 text-xs text-blue-600 hover:underline"
                >
                  <Users className="w-3.5 h-3.5" aria-hidden="true" /> {memberCountText}
                </Link>
              ) : (
                <span className="inline-flex items-center gap-1 text-xs text-muted-foreground">
                  <Users className="w-3.5 h-3.5" aria-hidden="true" /> {memberCountText}
                </span>
              )}
            </div>
            {role.description && <p className="text-sm text-muted-foreground mt-1">{role.description}</p>}
          </>
        )}
        {role.is_system && !role.is_owner && (
          <p className="text-xs text-muted-foreground mt-1">
            Built-in roles can't be edited — duplicate this role from the roles list to customize a copy.
          </p>
        )}
        {role.is_owner && (
          <p className="text-xs text-muted-foreground mt-1">
            The owner always has every permission and full access to all records and fields. To hand it over,
            transfer ownership from the Members page.
          </p>
        )}
      </div>

      {saveError && <div className="bg-red-50 text-red-700 text-sm rounded-md px-3 py-2">{saveError}</div>}

      {/* Data access (row scope, in plain words) */}
      <section className="border rounded-lg p-4">
        <h3 className="text-sm font-semibold">Which records can they see?</h3>
        <p className="text-xs text-muted-foreground mt-0.5 mb-2">
          Applies on top of the object access below, per record. Records shared with them individually are always visible.
        </p>
        {role.is_owner || role.is_system ? (
          <p className="text-sm">{SCOPE_SUMMARY[role.data_scope]}</p>
        ) : (
          <>
            <select
              value={role.data_scope}
              disabled={busy}
              onChange={(e) => changeScope(e.target.value as DataScope)}
              aria-label="Which records members with this role can see"
              className="border rounded-md px-2.5 py-1.5 text-sm bg-background disabled:opacity-60"
            >
              <option value="all">All records in the workspace</option>
              <option value="team">Records owned by anyone on their teams</option>
              <option value="own">Only records they own</option>
            </select>
            <p className="text-xs text-muted-foreground mt-2">
              {SCOPE_HELP[role.data_scope]}
            </p>
          </>
        )}
      </section>

      {/* Effective access: the merged "what can this role see?" table (U3.2) */}
      <section className="border rounded-lg p-4 space-y-3">
        <div className="flex items-start justify-between gap-3 flex-wrap">
          <div>
            <h3 className="text-sm font-semibold">What can this role see and do?</h3>
            <p className="text-xs text-muted-foreground mt-0.5">
              Access per object, with any field limits. An object with no checkmarks is invisible to this role.
            </p>
          </div>
          {!role.is_owner && (
            <div className="flex gap-3 text-xs">
              <Link to={`/settings/object-access?role=${role.id}`} className="text-blue-600 hover:underline">
                Edit object access
              </Link>
              <Link to={`/settings/field-access?role=${role.id}`} className="text-blue-600 hover:underline">
                Edit field access
              </Link>
              {/* Layout assignments live on the ObjectsManager's Layouts tab. */}
              <Link to="/settings/objects" className="text-blue-600 hover:underline">
                Edit layouts
              </Link>
            </div>
          )}
        </div>

        {zeroRead && (
          <div className="flex items-start gap-2 bg-amber-50 text-amber-800 text-sm rounded-md px-3 py-2 border border-amber-200">
            <AlertTriangle className="h-4 w-4 mt-0.5 shrink-0" aria-hidden="true" />
            <span>
              This role can't see any objects yet — members with it will find every page empty.{' '}
              <Link to={`/settings/object-access?role=${role.id}`} className="font-medium underline">
                Grant access
              </Link>
            </span>
          </div>
        )}

        <div className="border rounded-lg overflow-x-auto">
          <table className="w-full text-sm">
            <thead className="bg-muted/40">
              <tr>
                <th className="text-left font-medium px-3 py-2">Object</th>
                <th className="font-medium px-3 py-2 text-center">Read</th>
                <th className="font-medium px-3 py-2 text-center">Create</th>
                <th className="font-medium px-3 py-2 text-center">Edit</th>
                <th className="font-medium px-3 py-2 text-center">Delete</th>
                <th className="text-left font-medium px-3 py-2">Field limits</th>
                <th className="text-left font-medium px-3 py-2">Layout</th>
              </tr>
            </thead>
            <tbody>
              {data.objects.map((o) => (
                <tr key={o.slug} className="border-t">
                  <td className="px-3 py-2 whitespace-nowrap">
                    <span className="mr-1.5">{o.icon}</span>{o.label}
                    {!o.is_system && <span className="ml-1.5 text-xs text-muted-foreground">(custom)</span>}
                  </td>
                  {(['read', 'create', 'edit', 'delete'] as const).map((action) => (
                    <td key={action} className="px-3 py-2 text-center">
                      {o[action] ? (
                        <Check className="w-4 h-4 text-green-600 inline" role="img" aria-label={`Can ${action} ${o.label}`} />
                      ) : (
                        <Minus className="w-4 h-4 text-muted-foreground/50 inline" role="img" aria-label={`Cannot ${action} ${o.label}`} />
                      )}
                    </td>
                  ))}
                  <td className="px-3 py-2">
                    {o.restricted_fields.length === 0 ? (
                      <span className="text-xs text-muted-foreground">All fields</span>
                    ) : (
                      <div className="flex flex-wrap gap-1">
                        {o.restricted_fields.map((f) => (
                          <span
                            key={f.key}
                            className={`text-[11px] px-1.5 py-0.5 rounded border ${
                              f.level === 'hidden'
                                ? 'bg-amber-500/10 text-amber-700 border-amber-500/30'
                                : 'bg-muted text-muted-foreground border-border'
                            }`}
                          >
                            {f.label} · {f.level === 'hidden' ? 'Hidden' : 'Read-only'}
                          </span>
                        ))}
                      </div>
                    )}
                  </td>
                  <td className="px-3 py-2 text-xs text-muted-foreground whitespace-nowrap">
                    {layoutBySlug.get(o.slug) ?? 'Default'}
                  </td>
                </tr>
              ))}
            </tbody>
          </table>
        </div>
      </section>

      {/* Capabilities — grouped, descriptions visible, editable for custom roles */}
      <section className="border rounded-lg p-4 space-y-3">
        <div>
          <h3 className="text-sm font-semibold">What can they manage?</h3>
          <p className="text-xs text-muted-foreground mt-0.5">
            Permissions for workspace features and settings, separate from record access above.
          </p>
        </div>
        <div className="space-y-4">
          {grouped.map((section) => (
            <div key={section.group}>
              {section.group && catalog.length > 0 && (
                <div className="text-[11px] font-semibold uppercase tracking-wide text-muted-foreground mb-1.5">{section.group}</div>
              )}
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-x-6 gap-y-2">
                {section.caps.map((cap) => {
                  const checked = role.is_owner || role.capabilities.includes(cap.code);
                  return (
                    <label key={cap.code} className="flex items-start gap-2 text-sm">
                      <input
                        type="checkbox"
                        checked={checked}
                        disabled={role.is_system || busy}
                        onChange={() => toggleCapability(cap.code)}
                        className="h-4 w-4 mt-0.5 cursor-pointer disabled:cursor-not-allowed"
                      />
                      <span className={role.is_system ? 'text-muted-foreground' : ''}>
                        <span className="inline-flex items-center gap-1.5">
                          {cap.label}
                          {cap.sensitive && (
                            <span className="text-[10px] font-medium px-1.5 py-px rounded-full bg-amber-500/15 text-amber-700">
                              Sensitive
                            </span>
                          )}
                        </span>
                        {cap.description && (
                          <span className="block text-xs text-muted-foreground">{cap.description}</span>
                        )}
                      </span>
                    </label>
                  );
                })}
              </div>
            </div>
          ))}
        </div>
      </section>
      {confirmDialogEl}
    </div>
  );
}
