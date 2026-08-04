import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';

/**
 * U3 page gate for the email-templates library. Server truth: every
 * /api/workflows/email-templates* route — including the list GET — requires
 * workflows.manage, so the whole page renders AccessDeniedPanel for a member
 * without the capability (and must not fire the list query, which would 403),
 * waits for the capability fetch before deciding, and renders the real library
 * for a manager.
 */

// Mutable permission state so each test drives the caller's capabilities.
const mockPerms = vi.hoisted(() => ({ loaded: true, canManage: false }));
vi.mock('../../../lib/auth', () => ({
  usePermissions: () => ({
    loaded: mockPerms.loaded,
    can: (code: string) => code === 'workflows.manage' && mockPerms.canManage,
  }),
}));

// Spy-able list hook so the denied path can prove the query never ran.
const useEmailTemplatesMock = vi.hoisted(() => vi.fn());
vi.mock('../queries', () => ({
  useEmailTemplates: useEmailTemplatesMock,
  useDeleteEmailTemplate: () => ({ mutate: vi.fn(), isPending: false }),
  useTestSendEmailTemplate: () => ({ mutate: vi.fn(), isPending: false }),
}));

import { EmailTemplatesPage } from '../EmailTemplatesPage';

function renderPage() {
  return render(
    <MemoryRouter initialEntries={['/workflows/email-templates']}>
      <EmailTemplatesPage />
    </MemoryRouter>,
  );
}

beforeEach(() => {
  vi.clearAllMocks();
  mockPerms.loaded = true;
  mockPerms.canManage = false;
  useEmailTemplatesMock.mockReturnValue({
    data: { templates: [], total: 0 },
    isLoading: false,
    error: null,
    refetch: vi.fn(),
  });
});

describe('EmailTemplatesPage — workflows.manage gate', () => {
  it('renders the denied panel (not the library) without workflows.manage, and never fires the list query', () => {
    renderPage();

    // The shared denied panel, keyed to the missing capability's human label.
    expect(screen.getByText("You don't have access to this")).toBeInTheDocument();
    expect(screen.getByText('Manage workflows')).toBeInTheDocument();

    // None of the library renders — and the list GET (which would 403) never fires.
    expect(screen.queryByRole('heading', { name: 'Email Templates' })).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /New template/i })).not.toBeInTheDocument();
    expect(useEmailTemplatesMock).not.toHaveBeenCalled();
  });

  it('renders the real library for a workflows.manage holder', () => {
    mockPerms.canManage = true;
    renderPage();

    expect(screen.getByRole('heading', { name: 'Email Templates' })).toBeInTheDocument();
    // Header + empty-state each offer a create.
    expect(screen.getAllByRole('button', { name: /New template/i })).not.toHaveLength(0);
    expect(screen.queryByText("You don't have access to this")).not.toBeInTheDocument();
  });

  it('shows the failure — not "No email templates yet" — when the list request fails', () => {
    mockPerms.canManage = true;
    const refetch = vi.fn();
    useEmailTemplatesMock.mockReturnValue({
      data: undefined,
      isLoading: false,
      error: new Error('the service is temporarily unavailable (reference: 7c04ab9e)'),
      refetch,
    });

    renderPage();

    const alert = screen.getByRole('alert');
    expect(alert).toHaveTextContent("Couldn't load your email templates.");
    expect(alert).toHaveTextContent('the service is temporarily unavailable (reference: 7c04ab9e)');

    // The old code fell through to `data?.templates ?? []` and told a workspace
    // with a full library that it had none and should go make one.
    expect(screen.queryByText('No email templates yet')).not.toBeInTheDocument();
    expect(
      screen.queryByText('Create a template once and reuse it across workflows.'),
    ).not.toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /Retry/ }));
    expect(refetch).toHaveBeenCalledTimes(1);
  });

  it('shows neither the denied panel nor the library while permissions are loading (no denied flash)', () => {
    // Before the capability fetch settles, can() is false for everyone —
    // deciding now would flash the denied panel at a deep-linked manager.
    mockPerms.loaded = false;
    mockPerms.canManage = true;
    renderPage();

    expect(screen.queryByText("You don't have access to this")).not.toBeInTheDocument();
    expect(screen.queryByRole('heading', { name: 'Email Templates' })).not.toBeInTheDocument();
    expect(useEmailTemplatesMock).not.toHaveBeenCalled();
  });
});
