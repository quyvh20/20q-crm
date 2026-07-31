import { describe, it, expect, vi, afterEach, beforeEach } from 'vitest';
import { render, screen, cleanup, fireEvent } from '@testing-library/react';
import { MemoryRouter } from 'react-router-dom';
import { MarketingConsentPage } from '../MarketingConsentPage';

vi.mock('../../../lib/auth', () => ({ usePermissions: vi.fn() }));
import { usePermissions } from '../../../lib/auth';

const previewMutate = vi.fn();
const grantMutate = vi.fn();

vi.mock('../consentQueries', () => ({
  useGrantableBases: vi.fn(() => ({
    data: [
      { value: 'existing_business_relationship', requires_casl_expiry: false },
      { value: 'implied_transaction', requires_casl_expiry: true },
    ],
    isLoading: false,
  })),
  usePreviewGrant: vi.fn(() => ({ mutate: previewMutate, isPending: false, data: undefined })),
  useGrantLawfulBasis: vi.fn(() => ({ mutate: grantMutate, isPending: false })),
}));

vi.mock('../segmentsQueries', () => ({
  useSegments: vi.fn(() => ({ data: [{ id: 'seg-1', name: 'Customers' }], isLoading: false })),
}));

function setPerms(can: boolean, loaded: boolean) {
  (usePermissions as unknown as ReturnType<typeof vi.fn>).mockReturnValue({
    can: (code: string) => (code === 'marketing.manage' ? can : false),
    loaded,
  });
}

const renderPage = () => render(<MemoryRouter><MarketingConsentPage /></MemoryRouter>);

beforeEach(() => vi.clearAllMocks());
afterEach(cleanup);

describe('MarketingConsentPage gating', () => {
  it('shows a spinner (not the denied panel) while permissions are still loading', () => {
    setPerms(false, false);
    renderPage();
    expect(screen.getByText(/loading/i)).toBeInTheDocument();
    expect(screen.queryByLabelText('Lawful basis')).not.toBeInTheDocument();
  });

  it('shows the access-denied panel without the capability', () => {
    setPerms(false, true);
    renderPage();
    expect(screen.queryByLabelText('Lawful basis')).not.toBeInTheDocument();
  });
});

describe('MarketingConsentPage form', () => {
  beforeEach(() => setPerms(true, true));

  it('only offers bases an administrator may actually declare', () => {
    renderPage();
    const select = screen.getByLabelText('Lawful basis') as HTMLSelectElement;
    const values = Array.from(select.options).map((o) => o.value);

    expect(values).toContain('existing_business_relationship');
    // express / double_opt_in must never appear: the backend refuses them because
    // recording one would not make anyone mailable.
    expect(values).not.toContain('express');
    expect(values).not.toContain('double_opt_in');
  });

  it('requires every field before it will even preview', () => {
    renderPage();
    const button = screen.getByRole('button', { name: /review and record/i });
    expect(button).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'existing_business_relationship' },
    });
    expect(button).toBeDisabled(); // still no audience, no source

    fireEvent.change(screen.getByLabelText('Apply to audience'), { target: { value: 'seg-1' } });
    expect(button).toBeDisabled(); // still no declared source

    fireEvent.change(screen.getByLabelText('Declared source'), {
      target: { value: '2024 customer import' },
    });
    expect(button).toBeEnabled();
  });

  // The CASL bases expire, and the backend rejects a grant that omits the expiry.
  // Asking for it only when it applies keeps the form from 400ing on submit.
  it('asks for an expiry only for the CASL implied bases', () => {
    renderPage();
    expect(screen.queryByLabelText('Consent expires')).not.toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'implied_transaction' },
    });
    expect(screen.getByLabelText('Consent expires')).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'existing_business_relationship' },
    });
    expect(screen.queryByLabelText('Consent expires')).not.toBeInTheDocument();
  });

  it('keeps the CASL expiry blocking until it is supplied', () => {
    renderPage();
    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'implied_transaction' },
    });
    fireEvent.change(screen.getByLabelText('Apply to audience'), { target: { value: 'seg-1' } });
    fireEvent.change(screen.getByLabelText('Declared source'), { target: { value: 'orders' } });

    const button = screen.getByRole('button', { name: /review and record/i });
    expect(button).toBeDisabled();

    fireEvent.change(screen.getByLabelText('Consent expires'), { target: { value: '2027-01-01' } });
    expect(button).toBeEnabled();
  });

  // The whole point of the two-stage flow: nothing is written until the operator has
  // seen how many addresses a legally significant declaration would touch.
  it('previews rather than granting on the first click', () => {
    renderPage();
    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'existing_business_relationship' },
    });
    fireEvent.change(screen.getByLabelText('Apply to audience'), { target: { value: 'seg-1' } });
    fireEvent.change(screen.getByLabelText('Declared source'), {
      target: { value: '2024 customer import' },
    });
    fireEvent.click(screen.getByRole('button', { name: /review and record/i }));

    expect(previewMutate).toHaveBeenCalledTimes(1);
    expect(grantMutate).not.toHaveBeenCalled();

    const body = previewMutate.mock.calls[0][0];
    expect(body).toMatchObject({
      basis: 'existing_business_relationship',
      source: '2024 customer import',
      segment_ids: ['seg-1'],
    });
    // An expiry on a standing basis is a 400 — it must be omitted, not sent empty.
    expect(body.casl_expires_at).toBeUndefined();
  });

  // A date input yields "YYYY-MM-DD", and new Date() on that parses as UTC
  // midnight — which west of UTC is the PREVIOUS local day, expiring consent up to
  // a day early. The value must represent the end of the chosen day locally.
  it('sends a CASL expiry that does not lapse before the chosen day ends', () => {
    renderPage();
    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'implied_transaction' },
    });
    fireEvent.change(screen.getByLabelText('Apply to audience'), { target: { value: 'seg-1' } });
    fireEvent.change(screen.getByLabelText('Declared source'), { target: { value: 'orders' } });
    fireEvent.change(screen.getByLabelText('Consent expires'), { target: { value: '2027-03-01' } });
    fireEvent.click(screen.getByRole('button', { name: /review and record/i }));

    const sent = previewMutate.mock.calls[0][0].casl_expires_at as string;
    expect(sent).toBeDefined();

    // Whatever the runner's timezone, the instant sent must be at or after the end
    // of 2027-03-01 locally — never before it.
    const endOfChosenDayLocal = new Date('2027-03-01T23:59:59').getTime();
    expect(new Date(sent).getTime()).toBeGreaterThanOrEqual(endOfChosenDayLocal);
  });

  it('will not let a past expiry be picked', () => {
    renderPage();
    fireEvent.change(screen.getByLabelText('Lawful basis'), {
      target: { value: 'implied_transaction' },
    });
    const input = screen.getByLabelText('Consent expires') as HTMLInputElement;
    expect(input.min).toBe(new Date().toISOString().slice(0, 10));
  });
});
