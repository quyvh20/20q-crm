import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor } from '@testing-library/react';
import SecretReveal from '../SecretReveal';

// SecretReveal is the one-time-secret surface shared by the 2FA backup codes and
// a freshly minted API token. The load-bearing behavior: the value is shown, it
// can be copied, and it CANNOT be dismissed until the user says they've saved it.

const writeText = vi.fn();

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  writeText.mockResolvedValue(undefined);
  Object.assign(navigator, { clipboard: { writeText } });
});

describe('SecretReveal', () => {
  it('renders a single secret and copies it to the clipboard', async () => {
    render(<SecretReveal title="Your new API token" value="crm_pat_abc123" onDone={() => {}} />);

    expect(screen.getByTestId('secret-value')).toHaveTextContent('crm_pat_abc123');

    fireEvent.click(screen.getByRole('button', { name: /copy/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('crm_pat_abc123'));
    expect(await screen.findByText('Copied')).toBeInTheDocument();
  });

  it('renders a list of codes and copies them all as one block', async () => {
    render(<SecretReveal title="Your backup codes" values={['AAAAA-11111', 'BBBBB-22222']} onDone={() => {}} />);

    expect(screen.getByText('AAAAA-11111')).toBeInTheDocument();
    expect(screen.getByText('BBBBB-22222')).toBeInTheDocument();

    fireEvent.click(screen.getByRole('button', { name: /copy all/i }));
    await waitFor(() => expect(writeText).toHaveBeenCalledWith('AAAAA-11111\nBBBBB-22222'));
  });

  it('keeps Done disabled until the user acknowledges — the secret is unrecoverable', () => {
    const onDone = vi.fn();
    render(<SecretReveal title="Your backup codes" values={['AAAAA-11111']} onDone={onDone} />);

    const done = screen.getByRole('button', { name: 'Done' });
    expect(done).toBeDisabled();
    fireEvent.click(done);
    expect(onDone).not.toHaveBeenCalled();

    fireEvent.click(screen.getByRole('checkbox'));
    expect(done).toBeEnabled();
    fireEvent.click(done);
    expect(onDone).toHaveBeenCalledTimes(1);
  });

  it('surfaces a copy failure instead of silently claiming success', async () => {
    writeText.mockRejectedValue(new Error('denied'));
    // Legacy fallback also fails (jsdom has no execCommand).
    render(<SecretReveal title="Your new API token" value="crm_pat_abc123" onDone={() => {}} />);

    fireEvent.click(screen.getByRole('button', { name: /copy/i }));
    expect(await screen.findByText(/copy failed/i)).toBeInTheDocument();
    expect(screen.queryByText('Copied')).not.toBeInTheDocument();
  });
});
