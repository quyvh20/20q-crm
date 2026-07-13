// Shared role/capability display helpers (U3.5). One prettifier for the whole
// app — replaces the private copies that had drifted across MembersList,
// InviteMemberModal, and the raw role.name renders elsewhere.
import { CAPABILITY_LABELS } from './api';

// prettyRole title-cases a role name for display ("sales_rep" → "Sales Rep").
// Custom role names are admin-typed free text ("Support Agent") — splitting on
// '_' leaves them untouched.
export const prettyRole = (name: string | undefined | null): string =>
  (name ?? '')
    .split('_')
    .map((w) => w.charAt(0).toUpperCase() + w.slice(1))
    .join(' ');

// SENSITIVE_CAPABILITIES mirrors the sensitive=true entries of the backend
// catalog (domain.CapabilityCatalog) so the "Sensitive" chip survives a failed
// GET /api/roles/catalog instead of silently disappearing in the fallback
// capability list. The live catalog wins whenever it loads.
export const SENSITIVE_CAPABILITIES: ReadonlySet<string> = new Set([
  'members.manage',
  'roles.manage',
  'org.settings',
  'workflows.manage',
  'workflows.run_any',
  'data.export',
]);

// capabilityLabel resolves a capability code to its human label, preferring the
// live catalog (GET /api/roles/catalog) when the caller has it, falling back to
// the built-in label map, then the raw code. Used for friendly denied states
// ("You need *Manage roles* — ask an admin").
export const capabilityLabel = (
  code: string,
  catalog?: Array<{ code: string; label: string }>,
): string =>
  catalog?.find((c) => c.code === code)?.label ?? CAPABILITY_LABELS[code] ?? code;
