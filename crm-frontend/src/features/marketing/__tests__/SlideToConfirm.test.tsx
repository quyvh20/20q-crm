import { describe, it, expect, vi, afterEach } from 'vitest';
import { render, screen, fireEvent, cleanup } from '@testing-library/react';
import SlideToConfirm from '../SlideToConfirm';

afterEach(cleanup);

describe('SlideToConfirm send guard', () => {
  it('fires onConfirm only when slid all the way to the end', () => {
    const onConfirm = vi.fn();
    render(<SlideToConfirm label="Slide to send" onConfirm={onConfirm} />);
    const slider = screen.getByLabelText('Slide to send') as HTMLInputElement;

    // A partial slide then release must NOT confirm (and snaps back).
    fireEvent.change(slider, { target: { value: '55' } });
    fireEvent.mouseUp(slider);
    expect(onConfirm).not.toHaveBeenCalled();

    // A full slide to the end fires it.
    fireEvent.change(slider, { target: { value: '100' } });
    fireEvent.mouseUp(slider);
    expect(onConfirm).toHaveBeenCalledTimes(1);
  });

  it('does not fire when disabled', () => {
    const onConfirm = vi.fn();
    render(<SlideToConfirm label="Slide to send" disabled onConfirm={onConfirm} />);
    const slider = screen.getByLabelText('Slide to send') as HTMLInputElement;
    fireEvent.change(slider, { target: { value: '100' } });
    fireEvent.mouseUp(slider);
    expect(onConfirm).not.toHaveBeenCalled();
  });
});
