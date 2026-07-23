import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, cleanup } from '@testing-library/react';
import { MarketingStatusBadge } from '../MarketingStatusBadge';
import type { MarketingStatus } from '../api';

// Mock the data hook so the badge's rendering logic is tested in isolation (no
// react-query provider / network needed).
vi.mock('../queries', () => ({ useMarketingStatus: vi.fn() }));
import { useMarketingStatus } from '../queries';

// Mock permissions — the badge gates on marketing.manage. Default: allowed.
vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

function setPerm(allowed: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ can: () => allowed });
}

function setStatus(data: Partial<MarketingStatus> | undefined, isSuccess = true) {
  setPerm(true);
  (useMarketingStatus as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ data, isSuccess });
}

const base: MarketingStatus = {
  email: 'a@b.com', suppressed: false, suppression_reasons: [],
  sendable_marketing: true, not_sendable_reason: '', marketing_status: '', consent_basis: '',
};

afterEach(cleanup);

describe('MarketingStatusBadge', () => {
  it('renders nothing without an email', () => {
    setStatus(undefined, false);
    const { container } = render(<MarketingStatusBadge email={undefined} />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing while the query has not succeeded (loading / 403)', () => {
    setStatus(undefined, false);
    const { container } = render(<MarketingStatusBadge email="a@b.com" />);
    expect(container).toBeEmptyDOMElement();
  });

  it('shows "Do not email" when suppressed, even if status is subscribed', () => {
    setStatus({ ...base, suppressed: true, suppression_reasons: ['unsubscribe'], marketing_status: 'subscribed' });
    render(<MarketingStatusBadge email="a@b.com" />);
    expect(screen.getByText('Do not email')).toBeInTheDocument();
  });

  it('shows Subscribed', () => {
    setStatus({ ...base, marketing_status: 'subscribed' });
    render(<MarketingStatusBadge email="a@b.com" />);
    expect(screen.getByText('Subscribed')).toBeInTheDocument();
  });

  it('shows Pending opt-in', () => {
    setStatus({ ...base, marketing_status: 'pending' });
    render(<MarketingStatusBadge email="a@b.com" />);
    expect(screen.getByText('Pending opt-in')).toBeInTheDocument();
  });

  it('renders nothing when there is no signal (no state row, not suppressed)', () => {
    setStatus({ ...base, marketing_status: '' });
    const { container } = render(<MarketingStatusBadge email="a@b.com" />);
    expect(container).toBeEmptyDOMElement();
  });

  it('renders nothing (and does not depend on data) without the marketing.manage capability', () => {
    setPerm(false);
    (useMarketingStatus as unknown as ReturnType<typeof vi.fn>).mockReturnValue({ data: undefined, isSuccess: false });
    const { container } = render(<MarketingStatusBadge email="a@b.com" />);
    expect(container).toBeEmptyDOMElement();
  });
});
