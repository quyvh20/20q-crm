import { describe, it, expect, vi } from 'vitest';
import { render, screen, fireEvent } from '@testing-library/react';

// Isolate the two-tab shell's tab-selection logic from the (heavy) child panels.
vi.mock('../builder/config/ConfigPanel', () => ({
  ConfigPanel: () => <div>CONFIG_PANEL</div>,
}));
vi.mock('../builder/config/CopilotPanel', () => ({
  CopilotPanel: ({ initialPrompt }: { initialPrompt?: string }) => <div>COPILOT_PANEL:{initialPrompt}</div>,
}));

import { BuilderSidePanel } from '../builder/config/BuilderSidePanel';

// The active tab is CONTROLLED by NextBuilder (so the palette's "Draft with AI"
// card and the ?ai= handoff can open Copilot); the shell renders the given tab
// and reports clicks. The open-on-aiPrompt default lives in NextBuilder now.
describe('BuilderSidePanel', () => {
  it('renders the controlled tab as selected', () => {
    render(<BuilderSidePanel tab="configure" onTabChange={() => {}} />);
    expect(screen.getByRole('tab', { name: /configure/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: /copilot/i })).toHaveAttribute('aria-selected', 'false');
  });

  it('reports a tab click through onTabChange', () => {
    const onTabChange = vi.fn();
    render(<BuilderSidePanel tab="configure" onTabChange={onTabChange} />);
    fireEvent.click(screen.getByRole('tab', { name: /copilot/i }));
    expect(onTabChange).toHaveBeenCalledWith('copilot');
  });

  // A7.4: a Command Center handoff forwards the prompt into the Copilot panel.
  it('forwards aiPrompt to the Copilot panel', () => {
    render(<BuilderSidePanel aiPrompt="build me a welcome flow" tab="copilot" onTabChange={() => {}} />);
    expect(screen.getByRole('tab', { name: /copilot/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText(/COPILOT_PANEL:build me a welcome flow/)).toBeInTheDocument();
  });
});
