import { useState, useEffect, useRef, useCallback } from 'react';
import MDEditor from '@uiw/react-md-editor';
import { getKBSections, upsertKBSection, getKBAIPrompt, type KBEntry } from '../../lib/api';

const SECTIONS = [
  { key: 'company', label: 'Company', icon: '🏢' },
  { key: 'products', label: 'Products', icon: '📦' },
  { key: 'playbook', label: 'Playbook', icon: '📋' },
  { key: 'process', label: 'Process', icon: '⚙️' },
  { key: 'competitors', label: 'Competitors', icon: '🎯' },
] as const;

export default function KnowledgeBase() {
  const [activeSection, setActiveSection] = useState<string>('company');
  const [entries, setEntries] = useState<Record<string, KBEntry>>({});
  const [content, setContent] = useState('');
  const [title, setTitle] = useState('');
  const [saveStatus, setSaveStatus] = useState<'idle' | 'saving' | 'saved' | 'error'>('idle');
  const [showPrompt, setShowPrompt] = useState(false);
  const [aiPrompt, setAIPrompt] = useState('');
  const [loading, setLoading] = useState(true);
  const debounceRef = useRef<ReturnType<typeof setTimeout> | null>(null);

  // Load all sections
  useEffect(() => {
    getKBSections()
      .then((data) => {
        const map: Record<string, KBEntry> = {};
        for (const entry of data) {
          map[entry.section] = entry;
        }
        setEntries(map);
        // Load first available section
        if (map[activeSection]) {
          setContent(map[activeSection].content);
          setTitle(map[activeSection].title);
        }
      })
      .catch(() => {})
      .finally(() => setLoading(false));
  }, []); // eslint-disable-line react-hooks/exhaustive-deps

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
    if (!c.trim()) return;
    setSaveStatus('saving');
    try {
      const saved = await upsertKBSection(section, { title: t, content: c });
      setEntries(prev => ({ ...prev, [section]: saved }));
      setSaveStatus('saved');
      setTimeout(() => setSaveStatus('idle'), 2000);
    } catch {
      setSaveStatus('error');
    }
  }, []);

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
        <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full" />
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
        <button
          onClick={handlePreviewPrompt}
          className="px-4 py-2 text-sm font-medium rounded-lg bg-gradient-to-r from-indigo-500 to-purple-600 text-white hover:opacity-90 transition-opacity"
        >
          🔍 Preview AI Prompt
        </button>
      </div>

      {/* Main layout */}
      <div className="flex gap-4 min-h-[500px]">
        {/* Section tabs (left) */}
        <div className="w-48 flex-shrink-0 space-y-1">
          {SECTIONS.map(s => (
            <button
              key={s.key}
              onClick={() => setActiveSection(s.key)}
              className={`w-full text-left px-3 py-2.5 rounded-lg text-sm font-medium transition-all flex items-center gap-2 ${
                activeSection === s.key
                  ? 'bg-blue-50 text-blue-700 border border-blue-200'
                  : 'text-muted-foreground hover:bg-accent hover:text-foreground'
              }`}
            >
              <span>{s.icon}</span>
              {s.label}
              {entries[s.key] && (
                <span className="ml-auto w-2 h-2 rounded-full bg-green-400" title="Has content" />
              )}
            </button>
          ))}
        </div>

        {/* Editor (right) */}
        <div className="flex-1 min-w-0 space-y-3">
          {/* Editor toolbar */}
          <div className="flex items-center justify-between">
            <input
              value={title}
              onChange={e => setTitle(e.target.value)}
              className="text-lg font-semibold bg-transparent border-none outline-none flex-1"
              placeholder="Section title"
            />
            <div className="flex items-center gap-3 text-sm">
              <span className="text-muted-foreground">~{tokenEstimate} tokens</span>
              {saveStatus === 'saving' && (
                <span className="text-yellow-600 flex items-center gap-1">
                  <span className="animate-spin h-3 w-3 border-2 border-yellow-600 border-t-transparent rounded-full" />
                  Saving…
                </span>
              )}
              {saveStatus === 'saved' && (
                <span className="text-green-600">✓ Saved</span>
              )}
              {saveStatus === 'error' && (
                <span className="text-red-500">✗ Error</span>
              )}
            </div>
          </div>

          {/* Markdown editor */}
          <div data-color-mode="light">
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

      {/* AI Prompt Preview Modal */}
      {showPrompt && (
        <div className="fixed inset-0 z-50 flex items-center justify-center bg-background/80 backdrop-blur-sm">
          <div className="w-full max-w-3xl max-h-[80vh] overflow-hidden rounded-2xl bg-card border shadow-2xl flex flex-col">
            <div className="flex items-center justify-between px-6 py-4 border-b bg-gradient-to-r from-indigo-500 to-purple-600 text-white">
              <h3 className="font-semibold">🤖 AI System Prompt Preview</h3>
              <button onClick={() => setShowPrompt(false)} className="text-white/80 hover:text-white text-lg">✕</button>
            </div>
            <div className="flex-1 overflow-auto p-6">
              <pre className="whitespace-pre-wrap text-sm font-mono text-foreground leading-relaxed">
                {aiPrompt}
              </pre>
            </div>
            <div className="px-6 py-3 border-t text-sm text-muted-foreground">
              This is exactly what the AI sees when it helps your team.
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
