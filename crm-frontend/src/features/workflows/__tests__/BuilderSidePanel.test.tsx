import { describe, it, expect, vi } from 'vitest';
import { render, screen } from '@testing-library/react';

// Isolate the two-tab shell's tab-selection logic from the (heavy) child panels.
vi.mock('../builder/config/ConfigPanel', () => ({
  ConfigPanel: () => <div>CONFIG_PANEL</div>,
}));
vi.mock('../builder/config/CopilotPanel', () => ({
  CopilotPanel: ({ initialPrompt }: { initialPrompt?: string }) => <div>COPILOT_PANEL:{initialPrompt}</div>,
}));

import { BuilderSidePanel } from '../builder/config/BuilderSidePanel';

describe('BuilderSidePanel', () => {
  it('defaults to the Configure tab when no aiPrompt is handed in', () => {
    render(<BuilderSidePanel />);
    expect(screen.getByRole('tab', { name: /configure/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByRole('tab', { name: /copilot/i })).toHaveAttribute('aria-selected', 'false');
  });

  // A7.4: a Command Center handoff opens the Copilot tab and forwards the prompt.
  it('opens the Copilot tab and forwards the prompt when handed an aiPrompt', () => {
    render(<BuilderSidePanel aiPrompt="build me a welcome flow" />);
    expect(screen.getByRole('tab', { name: /copilot/i })).toHaveAttribute('aria-selected', 'true');
    expect(screen.getByText(/COPILOT_PANEL:build me a welcome flow/)).toBeInTheDocument();
  });
});
