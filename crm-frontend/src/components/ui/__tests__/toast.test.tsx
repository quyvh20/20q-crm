import { describe, it, expect, beforeEach, afterEach, vi } from 'vitest';
import { render, screen, act } from '@testing-library/react';
import userEvent from '@testing-library/user-event';
import { Toaster } from '../toast';
import { showToast, resetToasts, dismissToast } from '@/lib/useToast';

// Unit tests for the toast PRIMITIVE. Nothing else in the suite asserts through
// a toast — pages are pinned on their durable consequence instead (a row that
// disappears, an API call that fired), exactly as the E2E suite does, because a
// toast auto-dismisses and is a race by construction. This file is the one place
// the toast is the subject, so the behaviour is pinned once, here.

beforeEach(() => resetToasts());
afterEach(() => {
  resetToasts();
  vi.useRealTimers();
});

describe('Toaster — live regions', () => {
  it('mounts BOTH live regions before any toast exists', () => {
    render(<Toaster />);

    // The regression this guards: every hand-rolled toast this replaced rendered
    // `{toast && <div role="status">…}`, creating the live region in the same
    // commit as its text. A screen reader announces MUTATIONS of a region that
    // was already in the tree, so that pattern is routinely announced as nothing.
    expect(screen.getByRole('status')).toBeInTheDocument();
    expect(screen.getByRole('alert')).toBeInTheDocument();
    expect(screen.getByRole('status')).toBeEmptyDOMElement();
    expect(screen.getByRole('alert')).toBeEmptyDOMElement();
  });

  it('routes a success toast to the polite region and an error to the assertive one', () => {
    render(<Toaster />);

    act(() => {
      showToast('Saved');
      showToast('Save failed', { tone: 'error' });
    });

    // Politeness is fixed per region rather than swapped on one shared node,
    // which screen readers do not reliably re-read.
    const polite = screen.getByRole('status');
    const assertive = screen.getByRole('alert');
    expect(polite).toHaveTextContent('Saved');
    expect(polite).not.toHaveTextContent('Save failed');
    expect(assertive).toHaveTextContent('Save failed');
    expect(assertive).toHaveAttribute('aria-live', 'assertive');
    expect(polite).toHaveAttribute('aria-live', 'polite');
  });
});

describe('Toaster — dismissal', () => {
  it('auto-dismisses a success toast after its delay', () => {
    vi.useFakeTimers();
    render(<Toaster />);

    act(() => { showToast('Saved'); });
    expect(screen.getByText('Saved')).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(4000); });
    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
  });

  it('keeps an error up longer than a plain confirmation', () => {
    vi.useFakeTimers();
    render(<Toaster />);

    act(() => {
      showToast('Saved');
      showToast('Save failed', { tone: 'error' });
    });

    act(() => { vi.advanceTimersByTime(4000); });
    // The confirmation has gone; the thing the user must act on has not.
    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
    expect(screen.getByText('Save failed')).toBeInTheDocument();

    act(() => { vi.advanceTimersByTime(2000); });
    expect(screen.queryByText('Save failed')).not.toBeInTheDocument();
  });

  it('offers a close control, so auto-dismiss is never the only exit', async () => {
    const user = userEvent.setup();
    render(<Toaster />);

    act(() => { showToast('Saved'); });
    await user.click(screen.getByRole('button', { name: /Dismiss notification/i }));

    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
  });

  it('dismissToast on an already-gone id is a no-op, not a crash', () => {
    render(<Toaster />);
    let id = 0;
    act(() => { id = showToast('Saved'); });
    act(() => { dismissToast(id); });
    act(() => { dismissToast(id); });
    expect(screen.queryByText('Saved')).not.toBeInTheDocument();
  });
});

describe('Toaster — actions and stacking', () => {
  it('runs the action callback and closes the toast', async () => {
    const user = userEvent.setup();
    const onClick = vi.fn();
    render(<Toaster />);

    act(() => { showToast('Run started', { action: { label: 'View run', onClick } }); });
    await user.click(screen.getByRole('button', { name: 'View run' }));

    expect(onClick).toHaveBeenCalledTimes(1);
    expect(screen.queryByText('Run started')).not.toBeInTheDocument();
  });

  it('caps the stack, dropping the oldest', () => {
    render(<Toaster />);

    act(() => {
      showToast('one');
      showToast('two');
      showToast('three');
      showToast('four');
    });

    expect(screen.queryByText('one')).not.toBeInTheDocument();
    for (const m of ['two', 'three', 'four']) {
      expect(screen.getByText(m)).toBeInTheDocument();
    }
  });

  it('survives the viewport remounting — state is not owned by the component', () => {
    const { unmount } = render(<Toaster />);
    act(() => { showToast('Saved'); });
    unmount();

    // A toast raised as a lazy route transitions must still be readable once the
    // new tree paints; that is why the store lives outside React.
    render(<Toaster />);
    expect(screen.getByText('Saved')).toBeInTheDocument();
  });
});
