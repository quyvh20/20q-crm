import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { SendingDomainsPage } from '../SendingDomainsPage';
import type { EmailDomain } from '../domainsApi';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

const mutStub = () => ({ mutate: vi.fn(), isPending: false });
vi.mock('../domainsQueries', () => ({
  useDomains: vi.fn(),
  useAddDomain: () => mutStub(),
  useVerifyDomain: () => mutStub(),
  useRefreshDomain: () => mutStub(),
  useRemoveDomain: () => mutStub(),
}));
import { useDomains } from '../domainsQueries';

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (c: string) => (c === 'marketing.manage' ? can : false),
    loaded,
  });
}
function setDomains(list: Partial<EmailDomain>[], canSend: boolean, reason = '') {
  (useDomains as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    data: { data: list, meta: { can_bulk_send: canSend, reason } },
    isLoading: false,
  });
}

const renderPage = () => render(<MemoryRouter><SendingDomainsPage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('SendingDomainsPage', () => {
  it('shows a spinner while permissions load (no flash-denied)', () => {
    setPerms(false, false);
    setDomains([], false, 'no_domain');
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByText('Sending domains')).not.toBeInTheDocument();
  });

  it('denies without the marketing.manage capability', () => {
    setPerms(false, true);
    setDomains([], false, 'no_domain');
    renderPage();
    expect(screen.getByText(/marketing suppression & consent ledger/i)).toBeInTheDocument();
  });

  it('shows the blocked banner when no domain is verified', () => {
    setPerms(true, true);
    setDomains([], false, 'no_domain');
    renderPage();
    expect(screen.getByText('Sending domains')).toBeInTheDocument();
    expect(screen.getByText('Marketing sending is blocked')).toBeInTheDocument();
    expect(screen.getByText('No sending domains yet')).toBeInTheDocument();
  });

  it('shows the enabled banner and verification chips for a verified domain', () => {
    setPerms(true, true);
    setDomains([{
      id: 'd1', domain: 'example.com', status: 'verified',
      spf_verified: true, dkim_verified: true, dmarc_policy: 'none',
      dns_records: [], return_path: 'send.example.com',
    }], true, '');
    renderPage();
    expect(screen.getByText('Marketing sending is enabled')).toBeInTheDocument();
    expect(screen.getByText('example.com')).toBeInTheDocument();
    // DMARC chip reflects the policy
    expect(screen.getByText(/DMARC \(p=none\)/)).toBeInTheDocument();
  });
});
