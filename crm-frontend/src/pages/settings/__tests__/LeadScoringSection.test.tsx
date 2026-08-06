import { describe, it, expect } from 'vitest';

// The Lead Scoring section has THREE registration sites (SETTINGS_SECTIONS, the
// App.tsx route, the api layer) and missing the first is SILENT: SettingsLayout's
// deep-link guard redirects any first segment that is not a visible section, so
// /settings/lead-scoring would bounce to the default section and the route would
// never render — with nothing anywhere saying why.
//
// Modelled on the Integrations registration test, which exists for the same
// reason and caught the same class of mistake.
describe('Lead scoring settings registration', () => {
  it('is registered as a capability-gated workspace section', async () => {
    const { SETTINGS_SECTIONS, visibleSections } = await import('../sections');
    const entry = SETTINGS_SECTIONS.find((s) => s.path === 'lead-scoring');
    expect(entry, 'no SETTINGS_SECTIONS entry — the route would silently redirect').toBeDefined();
    expect(entry!.group).toBe('workspace');

    // objects.manage, matching what the backend's write routes actually enforce.
    // A looser gate here would show a page whose every button 403s.
    const withCap = visibleSections((c: string) => c === 'objects.manage');
    expect(withCap.some((s) => s.path === 'lead-scoring')).toBe(true);
    const without = visibleSections(() => false);
    expect(without.some((s) => s.path === 'lead-scoring')).toBe(false);
  });

  it('does not change where a bare /settings lands', async () => {
    const { defaultSectionPath } = await import('../sections');
    // defaultSectionPath returns the FIRST visible workspace section, so
    // inserting lead-scoring above 'general' would silently relocate every
    // admin's landing page.
    expect(defaultSectionPath((c: string) => c === 'org.settings' || c === 'objects.manage')).toBe('general');
  });

  it('does not displace Objects & Fields for an objects.manage-only admin', async () => {
    const { defaultSectionPath } = await import('../sections');
    // Both entries share the objects.manage gate, so ordering between them
    // decides the landing page for an admin who holds only that capability.
    // Objects & Fields is the established landing spot and must stay first.
    expect(defaultSectionPath((c: string) => c === 'objects.manage')).toBe('objects');
  });
});
