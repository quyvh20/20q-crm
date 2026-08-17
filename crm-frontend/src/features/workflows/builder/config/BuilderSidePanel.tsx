// The builder's right panel (A7.3): a two-tab shell over the existing per-node
// config panel and the AI Copilot. Tabs stay pinned while the content scrolls.
// Both panels stay mounted (inactive one hidden) so a half-typed Copilot prompt
// and the Configure scroll position survive a tab switch. The active tab is
// owned by NextBuilder so other surfaces (the palette's "Draft with AI" card,
// the ?ai= Command Center handoff) can open Copilot directly.
import { SlidersHorizontal, Sparkles } from 'lucide-react';
import { ConfigPanel } from './ConfigPanel';
import { CopilotPanel } from './CopilotPanel';
import type { DryRunState } from '../BuilderContext';

type Tab = 'configure' | 'copilot';

export function BuilderSidePanel({
  dryRun,
  aiPrompt,
  tab,
  onTabChange,
}: {
  dryRun?: DryRunState | null;
  aiPrompt?: string | null;
  tab: Tab;
  onTabChange: (tab: Tab) => void;
}) {
  const setTab = onTabChange;

  const tabBtn = (id: Tab, label: string, Icon: typeof SlidersHorizontal) => (
    <button
      type="button"
      role="tab"
      id={`builder-tab-${id}`}
      aria-selected={tab === id}
      aria-controls={`builder-tabpanel-${id}`}
      onClick={() => setTab(id)}
      className={`flex flex-1 items-center justify-center gap-1.5 px-3 py-2 text-xs font-medium transition-colors ${
        tab === id
          ? 'border-b-2 border-primary text-foreground'
          : 'border-b-2 border-transparent text-muted-foreground hover:text-foreground'
      }`}
    >
      <Icon className="h-3.5 w-3.5" /> {label}
    </button>
  );

  return (
    <div className="flex h-full flex-col">
      <div role="tablist" aria-label="Builder panel" className="flex shrink-0 border-b border-border">
        {tabBtn('configure', 'Configure', SlidersHorizontal)}
        {tabBtn('copilot', 'Copilot', Sparkles)}
      </div>
      <div
        role="tabpanel"
        id="builder-tabpanel-configure"
        aria-labelledby="builder-tab-configure"
        hidden={tab !== 'configure'}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        <ConfigPanel dryRun={dryRun} />
      </div>
      <div
        role="tabpanel"
        id="builder-tabpanel-copilot"
        aria-labelledby="builder-tab-copilot"
        hidden={tab !== 'copilot'}
        className="min-h-0 flex-1 overflow-y-auto"
      >
        <CopilotPanel initialPrompt={aiPrompt ?? ''} />
      </div>
    </div>
  );
}
