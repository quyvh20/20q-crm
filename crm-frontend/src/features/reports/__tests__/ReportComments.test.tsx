import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import type { ReportCommentView } from '../../../lib/api';

vi.mock('../../../lib/api', () => ({
  listReportComments: vi.fn(),
  addReportComment: vi.fn(),
  deleteReportComment: vi.fn(),
}));

import { listReportComments, addReportComment, deleteReportComment } from '../../../lib/api';
import ReportComments from '../ReportComments';

const comment = (partial: Partial<ReportCommentView>): ReportCommentView => ({
  id: crypto.randomUUID(), author_name: 'Someone', body: 'text',
  created_at: '2026-07-04T14:15:00Z', can_delete: false, ...partial,
});

function renderThread({ canComment }: { canComment: boolean }) {
  const qc = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  return render(
    <QueryClientProvider client={qc}>
      <ReportComments reportId="rep1" canComment={canComment} />
    </QueryClientProvider>,
  );
}

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
});

describe('ReportComments', () => {
  it('renders the thread with author names and bodies', async () => {
    vi.mocked(listReportComments).mockResolvedValue([
      comment({ author_name: 'Alice', body: 'first' }),
      comment({ author_name: 'Bob', body: 'second' }),
    ]);
    renderThread({ canComment: true });

    await waitFor(() => expect(screen.getByText('first')).toBeTruthy());
    expect(screen.getByText('Alice')).toBeTruthy();
    expect(screen.getByText('Bob')).toBeTruthy();
    expect(screen.getByText('second')).toBeTruthy();
  });

  it('posts a comment via addReportComment', async () => {
    vi.mocked(listReportComments).mockResolvedValue([]);
    vi.mocked(addReportComment).mockResolvedValue(undefined);
    renderThread({ canComment: true });

    await waitFor(() => expect(screen.getByText('No comments yet.')).toBeTruthy());
    fireEvent.change(screen.getByLabelText('Add a comment'), { target: { value: 'nice work' } });
    fireEvent.click(screen.getByText('Comment'));
    await waitFor(() => expect(addReportComment).toHaveBeenCalledWith('rep1', 'nice work'));
  });

  it('is read-only without comment access', async () => {
    vi.mocked(listReportComments).mockResolvedValue([comment({ body: 'hi' })]);
    renderThread({ canComment: false });

    await waitFor(() => expect(screen.getByText('hi')).toBeTruthy());
    expect(screen.getByText(/need comment access/i)).toBeTruthy();
    expect(screen.queryByLabelText('Add a comment')).toBeNull();
  });

  it('shows delete only on deletable rows and deletes via deleteReportComment', async () => {
    const mine = comment({ author_name: 'Me', body: 'mine', can_delete: true });
    const theirs = comment({ author_name: 'Them', body: 'theirs', can_delete: false });
    vi.mocked(listReportComments).mockResolvedValue([theirs, mine]);
    vi.mocked(deleteReportComment).mockResolvedValue(undefined);
    renderThread({ canComment: true });

    await waitFor(() => expect(screen.getByText('mine')).toBeTruthy());
    // The non-deletable row exposes no delete affordance.
    expect(screen.queryByLabelText('Delete comment by Them')).toBeNull();
    fireEvent.click(screen.getByLabelText('Delete comment by Me'));
    await waitFor(() => expect(deleteReportComment).toHaveBeenCalledWith('rep1', mine.id));
  });
});
