import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, cleanup } from '@testing-library/react';
import { MemoryRouter, Routes, Route } from 'react-router-dom';
import { APP_NAME, useDocumentTitle } from '../../../lib/useDocumentTitle';

// SettingsLayout titles all ~14 settings sub-routes from SETTINGS_SECTIONS (U7.2).
// The interesting case is /settings/roles/:id, which is NOT a section entry:
// RoleDetailSection titles that tab from the role's own name, so the layout must
// keep its hands off — otherwise the tab would read "Roles · Settings" over the
// top of the role the user actually opened.

vi.mock('../../../lib/auth', () => ({
  useAuth: () => ({ hasCapability: () => true, permsLoaded: true }),
}));

import SettingsLayout from '../SettingsLayout';

/** A plain settings section (Profile, Roles, …): like the real ones, it never
 *  touches document.title — the layout names it. */
function PlainSection() {
  return null;
}

/** A section that OWNS its title, as RoleDetailSection does. Calling the hook is
 *  the declaration of ownership: it renders inside the layout's <Outlet/>, i.e.
 *  after the layout's <DocumentTitle>, so its effect runs last and it wins. */
function TitledSection({ title }: { title?: string }) {
  useDocumentTitle(title);
  return null;
}

const renderAt = (path: string, child: React.ReactNode) =>
  render(
    <MemoryRouter initialEntries={[path]}>
      <Routes>
        <Route path="/settings" element={<SettingsLayout />}>
          <Route path="profile" element={child} />
          <Route path="roles" element={child} />
          <Route path="roles/:id" element={child} />
        </Route>
      </Routes>
    </MemoryRouter>,
  );

describe('SettingsLayout document title', () => {
  beforeEach(() => {
    document.title = 'initial';
  });
  afterEach(cleanup);

  it('names the tab after the active section', () => {
    renderAt('/settings/profile', <PlainSection />);
    expect(document.title).toBe(`Profile · Settings · ${APP_NAME}`);
  });

  it('names a workspace section too', () => {
    renderAt('/settings/roles', <PlainSection />);
    expect(document.title).toBe(`Roles · Settings · ${APP_NAME}`);
  });

  // The regression this guards: /settings/roles/:id is not a section, so the
  // layout must stay silent and let the role name stand.
  it('leaves /settings/roles/:id to the child, which owns the role name', () => {
    renderAt('/settings/roles/abc-123', <TitledSection title="Sales Rep · Roles · Settings" />);
    expect(document.title).toBe(`Sales Rep · Roles · Settings · ${APP_NAME}`);
  });

  it('never stamps "Roles · Settings" onto the nested role page', () => {
    // Child still loading (no title yet) ⇒ bare app name, never the section name:
    // proves the layout wrote nothing for the nested path.
    renderAt('/settings/roles/abc-123', <TitledSection />);
    expect(document.title).toBe(APP_NAME);
    expect(document.title).not.toContain('Roles · Settings');
  });
});
