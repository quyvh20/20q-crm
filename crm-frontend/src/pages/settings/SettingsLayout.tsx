import { Suspense } from 'react';
import { NavLink, Navigate, Outlet, useLocation } from 'react-router-dom';
import { useAuth } from '../../lib/auth';
import { DocumentTitle } from '../../lib/useDocumentTitle';
import { Skeleton, SpinnerBlock } from '@/components/ui';
import { SETTINGS_SECTIONS, defaultSectionPath, visibleSections, type SettingsSection } from './sections';

// The unified settings shell (U1): ONE routed area, grouped into Personal vs
// Workspace, every section URL-addressable and capability-gated. A section a
// member can't use doesn't render at all (the server still enforces every
// action); the email-templates entry links out to the existing A5 library.
//
// The section REGISTRY itself lives in ./sections.ts, not here: the global
// command palette needs it and the palette is in the eager entry chunk, so a
// static import of this file from there would cancel this route's lazy split.

// SettingsIndexRedirect sends a bare /settings to the member's default section
// — once capabilities have loaded, so an admin isn't misrouted to Security.
export function SettingsIndexRedirect() {
  const { hasCapability, permsLoaded } = useAuth();
  if (!permsLoaded) return <SettingsSkeleton />;
  return <Navigate to={`/settings/${defaultSectionPath(hasCapability)}`} replace />;
}

function SettingsSkeleton() {
  return (
    <div className="space-y-3 py-4">
      {[...Array(4)].map((_, i) => (
        <Skeleton key={i} className="h-10 rounded-lg" />
      ))}
    </div>
  );
}

function NavGroup({ title, sections }: { title: string; sections: SettingsSection[] }) {
  if (sections.length === 0) return null;
  return (
    <div>
      <p className="px-3 pb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">{title}</p>
      <div className="flex md:flex-col gap-1">
        {sections.map((s) => {
          const Icon = s.icon;
          const cls = ({ isActive }: { isActive: boolean }) =>
            `flex items-center gap-2 px-3 py-2 rounded-lg text-sm whitespace-nowrap transition-colors ${
              isActive && !s.externalTo
                ? 'bg-primary/10 text-primary font-medium'
                : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
            }`;
          // No `end`: nested pages (e.g. /settings/roles/:id) keep their parent
          // section highlighted. Section paths never prefix each other.
          return (
            <NavLink key={s.path} to={s.externalTo ?? `/settings/${s.path}`} className={cls}>
              <Icon className="w-4 h-4 shrink-0" />
              {s.label}
            </NavLink>
          );
        })}
      </div>
    </div>
  );
}

export default function SettingsLayout() {
  const { hasCapability, permsLoaded } = useAuth();
  const location = useLocation();

  // One title for all ~14 settings sub-routes (U7.2): the section that owns the
  // current sub-path names the tab. /settings/roles/:id is the exception — it is
  // NOT a SETTINGS_SECTIONS entry, and RoleDetailSection titles the tab with the
  // role's own name. So when the path is nested BELOW a section we render no
  // title at all and let the child own it: this layout is the child's parent, and
  // a parent's effect runs after its children's, so setting one here would
  // overwrite the role name a moment after RoleDetailSection wrote it.
  const pathParts = location.pathname.replace(/^\/settings\/?/, '').split('/').filter(Boolean);
  const [segment, ...nested] = pathParts;
  const activeSection = SETTINGS_SECTIONS.find((s) => s.path === segment && !s.externalTo);
  const documentTitle =
    nested.length > 0 ? null : activeSection ? `${activeSection.label} · Settings` : 'Settings';
  const titleEl = documentTitle ? <DocumentTitle title={documentTitle} /> : null;

  // Until the capability fetch settles, hasCapability is false for EVERYTHING —
  // deciding nav or redirecting now would bounce a deep-linked admin off a page
  // they're allowed on. Render the frame with a skeleton instead.
  if (!permsLoaded) {
    return (
      <div className="mx-auto w-full max-w-6xl">
        {titleEl}
        <div className="mb-6">
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">Settings</h1>
          <p className="text-muted-foreground mt-1">Your account and workspace configuration.</p>
        </div>
        <div className="space-y-3">
          {[...Array(5)].map((_, i) => (
            <Skeleton key={i} className="h-10 rounded-lg" />
          ))}
        </div>
      </div>
    );
  }

  const sections = visibleSections(hasCapability);
  const personal = sections.filter((s) => s.group === 'personal');
  const workspace = sections.filter((s) => s.group === 'workspace');

  // Route guard: a deep link to a section the member can't see redirects to
  // their default section (the server enforces the real gates regardless).
  // `segment` is destructured from pathParts above.
  if (segment && !sections.some((s) => s.path === segment && !s.externalTo)) {
    return <Navigate to={`/settings/${defaultSectionPath(hasCapability)}`} replace />;
  }

  return (
    <div className="mx-auto w-full max-w-6xl">
      {titleEl}
      <div className="mb-6">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">Settings</h1>
        <p className="text-muted-foreground mt-1">Your account and workspace configuration.</p>
      </div>

      <div className="flex flex-col md:flex-row gap-6 md:gap-8">
        {/* Grouped nav: vertical rail on desktop, scrollable rows on mobile. */}
        <nav aria-label="Settings sections" className="md:w-52 shrink-0 space-y-4 overflow-x-auto md:overflow-visible pb-1 md:pb-0">
          <NavGroup title="My settings" sections={personal} />
          <NavGroup title="Workspace" sections={workspace} />
        </nav>

        <div className="flex-1 min-w-0">
          {/* Each settings section is its own lazy chunk (App.tsx). Its own
              boundary, not AppLayout's, so a cold load of /settings/knowledge —
              the heaviest chunk in the app — keeps this nav rail on screen and
              spins only the panel beside it. */}
          <Suspense fallback={<SpinnerBlock />}>
            <Outlet />
          </Suspense>
        </div>
      </div>
    </div>
  );
}
