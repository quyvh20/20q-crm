import { useState } from 'react';
import { describe, it, expect, vi } from 'vitest';
import { render, screen, waitFor, within, fireEvent } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import Modal from '../Modal';
import { useConfirm } from '../ConfirmDialog';

// U7 (a11y): the app's hand-rolled overlays had no Escape, no focus trap, no
// focus restore and no aria. These tests pin the behaviour the shared Radix
// primitive is here to guarantee — a keyboard user can always get out, and lands
// back where they were. Note the dialog renders in a PORTAL, so everything is
// queried via `screen` (document.body), not the render container.

function Harness({ dismissable = true, onClosed }: { dismissable?: boolean; onClosed?: () => void }) {
  const [open, setOpen] = useState(false);
  return (
    <>
      <button onClick={() => setOpen(true)}>Open the modal</button>
      <Modal
        open={open}
        onClose={() => { setOpen(false); onClosed?.(); }}
        title="New Deal"
        description="Fill in the deal details."
        dismissable={dismissable}
      >
        <input aria-label="Deal title" />
        <button>Create Deal</button>
      </Modal>
    </>
  );
}

describe('Modal', () => {
  it('is a labelled, described modal dialog rendered in a portal', async () => {
    render(<Harness />);
    await userEvent.click(screen.getByRole('button', { name: 'Open the modal' }));

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveAttribute('aria-modal', 'true');
    expect(dialog).toHaveAccessibleName('New Deal');
    expect(dialog).toHaveAccessibleDescription('Fill in the deal details.');
    // The close X is an icon button — it must still announce itself.
    expect(within(dialog).getByRole('button', { name: 'Close' })).toBeInTheDocument();
  });

  it('closes on Escape and restores focus to the element that opened it', async () => {
    const onClosed = vi.fn();
    render(<Harness onClosed={onClosed} />);

    const trigger = screen.getByRole('button', { name: 'Open the modal' });
    await userEvent.click(trigger);
    await screen.findByRole('dialog');

    // Focus moved into the dialog, not left behind on the page underneath.
    await waitFor(() => expect(screen.getByRole('dialog').contains(document.activeElement)).toBe(true));

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
    expect(onClosed).toHaveBeenCalledTimes(1);
    // Focus restore: the old hand-rolled overlays dumped keyboard users at the
    // top of the document after every dismissal.
    await waitFor(() => expect(trigger).toHaveFocus());
  });

  it('closes on the X', async () => {
    render(<Harness />);
    await userEvent.click(screen.getByRole('button', { name: 'Open the modal' }));

    const dialog = await screen.findByRole('dialog');
    fireEvent.click(within(dialog).getByRole('button', { name: 'Close' }));

    await waitFor(() => expect(screen.queryByRole('dialog')).toBeNull());
  });

  it('ignores Escape while dismissal is blocked (e.g. a mutation in flight)', async () => {
    render(<Harness dismissable={false} />);
    await userEvent.click(screen.getByRole('button', { name: 'Open the modal' }));
    await screen.findByRole('dialog');

    fireEvent.keyDown(document, { key: 'Escape' });

    // Still open — a stray Escape must not orphan an in-flight request.
    expect(screen.getByRole('dialog')).toBeInTheDocument();
  });
});

// ConfirmDialog is the Modal's highest-traffic consumer (~14 call sites) and its
// promise API is what they all depend on; assert both halves still hold.
function ConfirmHarness({ onResult }: { onResult: (ok: boolean) => void }) {
  const { confirm, dialog } = useConfirm();
  return (
    <>
      <button
        onClick={async () => onResult(await confirm({ title: 'Delete Contact', body: 'This cannot be undone.' }))}
      >
        Delete
      </button>
      {dialog}
    </>
  );
}

describe('useConfirm', () => {
  it('resolves true on confirm and false on Escape, restoring focus', async () => {
    const onResult = vi.fn();
    render(<ConfirmHarness onResult={onResult} />);

    const trigger = screen.getByRole('button', { name: 'Delete' });
    await userEvent.click(trigger);

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveTextContent('Delete Contact');
    expect(dialog).toHaveTextContent('This cannot be undone.');

    // Escape cancels, like the window.confirm this replaced.
    fireEvent.keyDown(document, { key: 'Escape' });
    await waitFor(() => expect(onResult).toHaveBeenCalledWith(false));
    await waitFor(() => expect(trigger).toHaveFocus());

    // …and confirming resolves true.
    await userEvent.click(trigger);
    fireEvent.click(within(await screen.findByRole('dialog')).getByRole('button', { name: 'Confirm' }));
    await waitFor(() => expect(onResult).toHaveBeenLastCalledWith(true));
    expect(screen.queryByRole('dialog')).toBeNull();
  });
});
