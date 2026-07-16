import { useState, useEffect, useRef, useCallback } from 'react';
import MDEditor from '@uiw/react-md-editor';
import { Building2, Check, ClipboardList, Loader2, Package, Search, Settings, Target, X, type LucideIcon } from 'lucide-react';
import { getKBSections, upsertKBSection, getKBAIPrompt, type KBEntry } from '../../lib/api';
import Modal from '../common/Modal';
import { Button, Input, Spinner } from '@/components/ui';

const SECTIONS: { key: string; label: string; icon: LucideIcon }[] = [
  { key: 'company', label: 'Company', icon: Building2 },
  { key: 'products', label: 'Products', icon: Package },
  { key: 'playbook', label: 'Playbook', icon: ClipboardList },
  { key: 'process', label: 'Process', icon: Settings },
  { key: 'competitors', label: 'Competitors', icon: Target },
];

export default function KnowledgeBase() {
  const [activeSection, setActiveSection] = useState<string>('company');
  const [entries, setEntries] = useState<Record<string, KBEntry>>({});
  const [content, setContent] = useState('');
  const [title, setTitle] = useState('');
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');
  const [showPrompt, setShowPrompt] = useState(false);
  const [aiPrompt, setAIPrompt] = useState('');
  const [loading, setLoading] = useState(true);
  const [loadError, setLoadError] = useState('');
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Load all sections
  const loadSections = useCallback(() => {
    setLoading(true);
    setLoadError('');
    getKBSections()
      .then((data) => {
        const map: Record<string, KBEntry> = {};
        for (const entry of data) {
          map[entry.section] = entry;
        }
        setEntries(map);
        setContent(map[activeSection]?.content ?? '');
        setTitle(map[activeSection]?.title ?? (SECTIONS.find(s => s.key === activeSection)?.label || activeSection));
      })
      .catch((e) => setLoadError(e instanceof Error ? e.message : 'Failed to load the knowledge base'))
      .finally(() => setLoading(false));
  }, [activeSection]);

  useEffect(() => { loadSections(); }, []); // eslint-disable-line react-hooks/exhaustive-deps

  // Switch section
  useEffect(() => {
    const entry = entries[activeSection];
    if (entry) {
      setContent(entry.content);
      setTitle(entry.title);
    } else {
      const sectionMeta = SECTIONS.find(s => s.key === activeSection);
      setContent('');
      setTitle(sectionMeta?.label || activeSection);
    }
    setSaveStatus('idle');
  }, [activeSection, entries]);

  const doSave = useCallback(async (section: string, t: string, c: string) => {
    // Saving empty content is only skipped for a section that never had any —
    // CLEARING an existing section must save, or deleted text silently comes
    // back on reload (the old early-return swallowed it).
    if (!c.trim() && !entries[section]) return;
    setSaveStatus('saving');
    try {
      const saved = await upsertKBSection(section, { title: t, content: c });
      setEntries(prev => ({ ...prev, [section]: saved }));
      setSaveStatus('saved');
      setTimeout(() => setSaveStatus('idle'), 2000);
    } catch {
      setSaveStatus('error');
    }
  }, [entries]);

  const handleContentChange = (val: string | undefined) => {
    const newContent = val || '';
    setContent(newContent);
    setSaveStatus('idle');

    // Debounce auto-save (fallback if user doesn't blur)
    if (debounceRef.current) clearTimeout(debounceRef.current);
    debounceRef.current = setTimeout(() => {
      doSave(activeSection, title || activeSection, newContent);
    }, 1500);
  };

  // Save immediately on blur — cancels the pending debounce
  const handleBlur = () => {
    if (debounceRef.current) {
      clearTimeout(debounceRef.current);
      debounceRef.current = null;
    }
    doSave(activeSection, title || activeSection, content);
  };

  const handlePreviewPrompt = async () => {
    try {
      const prompt = await getKBAIPrompt();
      setAIPrompt(prompt);
      setShowPrompt(true);
    } catch {
      setAIPrompt('Failed to load AI prompt');
      setShowPrompt(true);
    }
  };

  const tokenEstimate = Math.round(content.length / 4);

  if (loading) {
    return (
      <div className="flex items-center justify-center py-20">
        <Spinner size="lg" />
      </div>
    );
  }

  // A failed load must DISABLE editing, not just warn: the editor would render
  // blank over real server content, and one autosaved keystroke would then
  // overwrite the whole section (the A5.2 template-editor data-loss class).
  if (loadError) {
    return (
      <div className="space-y-4">
        <div>
          <h2 className="text-lg font-semibold">Business Knowledge Base</h2>
        </div>
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-4 text-sm text-destructive">
          {loadError} — editing is disabled so your existing content isn't overwritten.
        </div>
        <Button variant="outline" onClick={loadSections}>
          Try again
        </Button>
      </div>
    );
  }

  return (
    <div className="space-y-4">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h2 className="text-lg font-semibold">Business Knowledge Base</h2>
          <p className="text-sm text-muted-foreground">
            Teach your AI assistant about your company, products, and sales approach.
          </p>
        </div>
        <Button onClick={handlePreviewPrompt}>
          <Search aria-hidden /> Preview AI Prompt
        </Button>
      </div>

      {/* Main layout */}
      <div className="flex gap-4 min-h-[500px]">
        {/* Section tabs (left) */}
        <div className="w-48 flex-shrink-0 space-y-1">
          {SECTIONS.map(s => {
            const Icon = s.icon;
            return (
            <button
              key={s.key}
              type="button"
              onClick={() => setActiveSection(s.key)}
              className={`w-full text-left px-3 py-2.5 rounded-lg text-sm font-medium transition-colors flex items-center gap-2 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                activeSection === s.key
                  ? 'bg-primary/10 text-primary'
                  : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
              }`}
            >
              <Icon aria-hidden className="h-4 w-4 shrink-0" />
              {s.label}
              {entries[s.key] && (
                <span className="ml-auto w-2 h-2 rounded-full bg-emerald-500" title="Has content" />
              )}
            </button>
            );
          })}
        </div>

        {/* Editor (right) */}
        <div className="flex-1 min-w-0 space-y-3">
          {/* Editor toolbar */}
          <div className="flex items-center justify-between">
            <Input
              value={title}
              onChange={e => setTitle(e.target.value)}
              className="flex-1 border-none bg-transparent text-lg font-semibold shadow-none focus-visible:ring-0"
              placeholder="Section title"
            />
            <div className="flex items-center gap-3 text-sm">
              <span className="text-muted-foreground">~{tokenEstimate} tokens</span>
              {saveStatus === 'saving' && (
                <span className="flex items-center gap-1 text-amber-600 dark:text-amber-400">
                  <Loader2 className="h-3 w-3 animate-spin" aria-hidden />
                  Saving…
                </span>
              )}
              {saveStatus === 'saved' && (
                <span className="inline-flex items-center gap-1 text-emerald-600 dark:text-emerald-400"><Check className="h-3.5 w-3.5" aria-hidden /> Saved</span>
              )}
              {saveStatus === 'error' && (
                <span className="inline-flex items-center gap-1 text-destructive"><X className="h-3.5 w-3.5" aria-hidden /> Error</span>
              )}
            </div>
          </div>

          {/* Markdown editor — follow the app theme instead of forcing light */}
          <div data-color-mode={typeof document !== 'undefined' && document.documentElement.classList.contains('dark') ? 'dark' : 'light'}>
            <MDEditor
              value={content}
              onChange={handleContentChange}
              onBlur={handleBlur}
              height={420}
              preview="edit"
            />
          </div>
        </div>
      </div>

      {/* AI Prompt Preview — shared Radix modal (U7). The prompt is fetched
          before the modal opens, so there's no in-flight state to guard. */}
      <Modal
        open={showPrompt}
        onClose={() => setShowPrompt(false)}
        title="AI System Prompt Preview"
        size="3xl"
        padded={false}
      >
        <>
          <div className="p-6">
            <pre className="whitespace-pre-wrap text-sm font-mono text-foreground leading-relaxed">
              {aiPrompt}
            </pre>
          </div>
          <div className="px-6 py-3 border-t border-border text-sm text-muted-foreground">
            This is exactly what the AI sees when it helps your team.
          </div>
        </>
      </Modal>
    </div>
  );
}
