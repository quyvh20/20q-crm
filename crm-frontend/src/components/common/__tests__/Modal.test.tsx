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

  // hideHeader is what let the mobile nav (AppLayout) adopt the primitive: it
  // owns its own brand block and close button, so the built-in header row is in
  // the way — but the dialog must STILL be named for assistive tech.
  it('keeps an accessible name when the header is hidden', async () => {
    render(
      <Modal open onClose={() => {}} title="Navigation" variant="drawer" side="left" hideHeader padded={false}>
        <a href="/deals">Deals</a>
      </Modal>,
    );

    const dialog = await screen.findByRole('dialog');
    expect(dialog).toHaveAccessibleName('Navigation');
    // No visible title row and no built-in X — the caller supplies its own.
    expect(within(dialog).queryByRole('button', { name: 'Close' })).toBeNull();
    expect(within(dialog).getByRole('link', { name: 'Deals' })).toBeInTheDocument();
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

// MemberDrawer opens a confirm ON TOP of an open drawer — the app's only
// modal-over-modal. Radix's layer stack is what keeps the two from fighting:
// interacting with the confirm must not read as an "outside click" that
// dismisses the drawer underneath it, and Escape must close only the top layer.
function NestedHarness({ onResult }: { onResult: (ok: boolean) => void }) {
  const [open, setOpen] = useState(true);
  const { confirm, dialog } = useConfirm();
  return (
    <>
      <Modal open={open} onClose={() => setOpen(false)} title="Member details" variant="drawer">
        <button onClick={async () => onResult(await confirm({ title: 'Sign out everywhere?', body: 'They will be signed out.' }))}>
          Sign out everywhere
        </button>
      </Modal>
      {dialog}
    </>
  );
}

describe('Modal nested in Modal', () => {
  it('confirming on the top layer leaves the dialog underneath open', async () => {
    const onResult = vi.fn();
    render(<NestedHarness onResult={onResult} />);

    expect(await screen.findByRole('dialog', { name: 'Member details' })).toBeInTheDocument();
    await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }));

    const confirmDialog = await screen.findByRole('dialog', { name: 'Sign out everywhere?' });
    await userEvent.click(within(confirmDialog).getByRole('button', { name: 'Confirm' }));

    await waitFor(() => expect(onResult).toHaveBeenCalledWith(true));
    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Sign out everywhere?' })).toBeNull());
    // The drawer survives: clicking the confirm is not an outside-click on it.
    expect(screen.getByRole('dialog', { name: 'Member details' })).toBeInTheDocument();
  });

  it('Escape closes only the top layer', async () => {
    render(<NestedHarness onResult={() => {}} />);
    await userEvent.click(screen.getByRole('button', { name: 'Sign out everywhere' }));
    await screen.findByRole('dialog', { name: 'Sign out everywhere?' });

    fireEvent.keyDown(document, { key: 'Escape' });

    await waitFor(() => expect(screen.queryByRole('dialog', { name: 'Sign out everywhere?' })).toBeNull());
    expect(screen.getByRole('dialog', { name: 'Member details' })).toBeInTheDocument();
  });
});

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
