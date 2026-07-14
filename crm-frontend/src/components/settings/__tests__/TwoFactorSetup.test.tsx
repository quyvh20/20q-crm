import { describe, it, expect, vi, beforeEach } from 'vitest';
import { render, screen, fireEvent, cleanup, waitFor, within } from '@testing-library/react';

vi.mock('../../../lib/api', () => ({
  getTwoFactorStatus: vi.fn(),
  startTwoFactorSetup: vi.fn(),
  enableTwoFactor: vi.fn(),
  disableTwoFactor: vi.fn(),
  regenerateBackupCodes: vi.fn(),
}));

import {
  getTwoFactorStatus, startTwoFactorSetup, enableTwoFactor, disableTwoFactor, regenerateBackupCodes,
} from '../../../lib/api';
import TwoFactorSetup from '../TwoFactorSetup';

const SETUP = {
  secret: 'JBSWY3DPEHPK3PXP',
  otpauth_url: 'otpauth://totp/CRM:me@x.com?secret=JBSWY3DPEHPK3PXP',
  qr_data_uri: 'data:image/png;base64,QQ==',
};

beforeEach(() => {
  cleanup();
  vi.clearAllMocks();
  Object.assign(navigator, { clipboard: { writeText: vi.fn().mockResolvedValue(undefined) } });
});

describe('TwoFactorSetup — enrollment', () => {
  beforeEach(() => {
    vi.mocked(getTwoFactorStatus).mockResolvedValue({
      enabled: false, backup_codes_left: 0, required_by_workspace: false,
    });
  });

  it('walks QR → code → the one-time backup codes', async () => {
    vi.mocked(startTwoFactorSetup).mockResolvedValue(SETUP);
    vi.mocked(enableTwoFactor).mockResolvedValue(['AAAAA-11111', 'BBBBB-22222']);
    // After enabling, the panel reloads status — now enrolled.
    vi.mocked(getTwoFactorStatus).mockResolvedValueOnce({
      enabled: false, backup_codes_left: 0, required_by_workspace: false,
    }).mockResolvedValue({ enabled: true, backup_codes_left: 2, required_by_workspace: false });

    render(<TwoFactorSetup />);

    fireEvent.click(await screen.findByRole('button', { name: /set up two-factor/i }));

    // The QR is a SERVER-rendered PNG data URI — the app ships no QR library.
    const qr = await screen.findByAltText(/qr code/i);
    expect(qr).toHaveAttribute('src', SETUP.qr_data_uri);

    // Manual fallback for anyone who can't scan.
    fireEvent.click(screen.getByRole('button', { name: /setup key/i }));
    expect(screen.getByText(SETUP.secret)).toBeInTheDocument();

    fireEvent.change(screen.getByLabelText(/code from your app/i), { target: { value: '123456' } });
    fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));

    await waitFor(() => expect(enableTwoFactor).toHaveBeenCalledWith('123456'));

    // The backup codes are revealed exactly once, behind an acknowledgement.
    expect(await screen.findByText('AAAAA-11111')).toBeInTheDocument();
    expect(screen.getByText('BBBBB-22222')).toBeInTheDocument();
    expect(screen.getByRole('button', { name: 'Done' })).toBeDisabled();
  });

  it('keeps the user on the code step when the code is wrong', async () => {
    vi.mocked(startTwoFactorSetup).mockResolvedValue(SETUP);
    vi.mocked(enableTwoFactor).mockRejectedValue(new Error("that code isn't right"));

    render(<TwoFactorSetup />);
    fireEvent.click(await screen.findByRole('button', { name: /set up two-factor/i }));
    fireEvent.change(await screen.findByLabelText(/code from your app/i), { target: { value: '000000' } });
    fireEvent.click(screen.getByRole('button', { name: 'Turn on' }));

    expect(await screen.findByText(/that code isn't right/i)).toBeInTheDocument();
    expect(screen.getByLabelText(/code from your app/i)).toBeInTheDocument();
  });
});

describe('TwoFactorSetup — enrolled', () => {
  it('regenerating backup codes needs a code AND a confirmation', async () => {
    vi.mocked(getTwoFactorStatus).mockResolvedValue({
      enabled: true, enabled_at: '2026-07-01T00:00:00Z', backup_codes_left: 7, required_by_workspace: false,
    });
    vi.mocked(regenerateBackupCodes).mockResolvedValue(['CCCCC-33333']);

    render(<TwoFactorSetup />);

    fireEvent.click(await screen.findByRole('button', { name: /regenerate backup codes/i }));
    fireEvent.change(screen.getByLabelText(/confirm with a code/i), { target: { value: '654321' } });
    fireEvent.click(screen.getByRole('button', { name: 'Regenerate' }));

    // The destructive step goes through the shared confirm dialog.
    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent(/existing backup codes stop working/i);
    fireEvent.click(within(dialog).getByRole('button', { name: 'Regenerate' }));

    await waitFor(() => expect(regenerateBackupCodes).toHaveBeenCalledWith('654321'));
    expect(await screen.findByText('CCCCC-33333')).toBeInTheDocument();
  });

  it('turning it off requires a code', async () => {
    vi.mocked(getTwoFactorStatus).mockResolvedValue({
      enabled: true, backup_codes_left: 10, required_by_workspace: false,
    });
    vi.mocked(disableTwoFactor).mockResolvedValue(undefined);

    render(<TwoFactorSetup />);

    fireEvent.click(await screen.findByRole('button', { name: /turn off/i }));
    fireEvent.change(screen.getByLabelText(/confirm with a code/i), { target: { value: 'AAAAA-11111' } });
    fireEvent.click(screen.getByRole('button', { name: 'Turn off' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Turn it off' }));

    await waitFor(() => expect(disableTwoFactor).toHaveBeenCalledWith('AAAAA-11111'));
  });

  it('hides "Turn off" when the workspace requires 2FA', async () => {
    vi.mocked(getTwoFactorStatus).mockResolvedValue({
      enabled: true, backup_codes_left: 10, required_by_workspace: true,
    });

    render(<TwoFactorSetup />);

    expect(await screen.findByText(/workspace requires two-factor/i)).toBeInTheDocument();
    expect(screen.queryByRole('button', { name: /turn off/i })).not.toBeInTheDocument();
    // Rotating the codes is still allowed.
    expect(screen.getByRole('button', { name: /regenerate backup codes/i })).toBeInTheDocument();
  });

  it('warns when the backup codes are nearly gone', async () => {
    vi.mocked(getTwoFactorStatus).mockResolvedValue({
      enabled: true, backup_codes_left: 1, required_by_workspace: false,
    });

    render(<TwoFactorSetup />);
    expect(await screen.findByText(/almost out of backup codes/i)).toBeInTheDocument();
  });
});
