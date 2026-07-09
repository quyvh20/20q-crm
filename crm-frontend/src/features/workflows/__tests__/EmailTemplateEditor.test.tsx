import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { MemoryRouter, Routes, Route } from 'react-router-dom';

// Mutable handle so each test controls what useEmailTemplate returns.
const emailTemplateResult: { data: unknown; isLoading: boolean; isError: boolean; refetch: () => void } = {
  data: undefined,
  isLoading: false,
  isError: false,
  refetch: vi.fn(),
};

vi.mock('../queries', () => ({
  useEmailTemplate: () => emailTemplateResult,
  useSaveEmailTemplate: () => ({ mutate: vi.fn(), isPending: false }),
  useTestSendEmailTemplate: () => ({ mutate: vi.fn(), isPending: false }),
}));

// The TipTap body editor isn't under test here; stub it to avoid mounting ProseMirror.
vi.mock('../builder/config/EmailTemplateBodyEditor', () => ({
  EmailTemplateBodyEditor: () => null,
}));

import { EmailTemplateEditor } from '../EmailTemplateEditor';

function renderAt(id: string) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <MemoryRouter initialEntries={[`/workflows/email-templates/${id}`]}>
        <Routes>
          <Route path="/workflows/email-templates/:id" element={<EmailTemplateEditor />} />
        </Routes>
      </MemoryRouter>
    </QueryClientProvider>,
  );
}

describe('EmailTemplateEditor', () => {
  it('shows an error state (not a blank editable form) when an existing template fails to load', () => {
    emailTemplateResult.data = undefined;
    emailTemplateResult.isLoading = false;
    emailTemplateResult.isError = true;

    renderAt('real-id');

    // The error guard must block the form so a save can't overwrite the real row with blanks.
    expect(screen.getByText("Couldn't load this template.")).toBeInTheDocument();
    expect(screen.queryByText('Edit email template')).not.toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /Save/ })).not.toBeInTheDocument();
  });

  it('seeds the form from the loaded template', () => {
    emailTemplateResult.data = {
      id: 'real-id',
      org_id: 'o1',
      name: 'Welcome',
      subject: 'Hi there',
      body_html: '<p>hi</p>',
      object_slug: 'contact',
      created_by: 'u',
      updated_by: 'u',
      created_at: '',
      updated_at: '',
    };
    emailTemplateResult.isLoading = false;
    emailTemplateResult.isError = false;

    renderAt('real-id');

    expect(screen.getByText('Edit email template')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Welcome')).toBeInTheDocument();
    expect(screen.getByDisplayValue('Hi there')).toBeInTheDocument();
  });
});
