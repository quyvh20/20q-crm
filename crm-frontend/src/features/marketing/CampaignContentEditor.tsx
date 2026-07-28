// The marketing email builder page (M6.2 rebuilt as a drag-and-drop editor):
// full-height toolbar + palette / WYSIWYG canvas / inspector, document-level
// undo/redo, and a server-compiled preview modal. The wire contract is unchanged
// — the store's blocks ARE body_json.blocks (merge chips serialized to bare
// {{path|fallback}} tokens on every edit) and save/preview/test-send hit the
// same endpoints through the same React Query hooks.

import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  AlertCircle, ArrowLeft, CheckCircle2, Code, Eye, LayoutGrid, Monitor, Redo2, Send, Undo2,
} from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Badge, Button, SpinnerBlock } from '@/components/ui';
import { EmailBuilder } from './composer/EmailBuilder';
import { CodeView } from './composer/CodeView';
import { PreviewModal } from './composer/PreviewModal';
import { blocksToSimpleHtml } from './composer/htmlSerializer';
import { useBuilderStore, DEFAULT_SCOPE, type DocStyles } from './composer/builderStore';
import type { Block, BlockDocument } from './composer/blocks';
import { variableGroupsForScope } from './composer/mergeScope';
import type { Check } from './composer/InspectorPanel';
import { useContent, useCreateContent, useUpdateContent } from './contentQueries';
import { previewContent, testSendContent, type PreviewResult, type SaveError } from './contentApi';

export const CampaignContentEditor: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) return <div className="mx-auto w-full max-w-6xl"><SpinnerBlock label="Loading…" /></div>;
  if (!can('marketing.manage')) {
    return <div className="mx-auto w-full max-w-6xl"><AccessDeniedPanel capability="marketing.manage" what="email content" /></div>;
  }
  return <Editor />;
};

const Editor: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const isNew = !id || id === 'new';
  const navigate = useNavigate();
  const { data, isLoading } = useContent(isNew ? undefined : id);
  const createMut = useCreateContent();
  const updateMut = useUpdateContent();

  const name = useBuilderStore((s) => s.name);
  const subject = useBuilderStore((s) => s.subject);
  const preheader = useBuilderStore((s) => s.preheader);
  const scope = useBuilderStore((s) => s.scope);
  const blocks = useBuilderStore((s) => s.blocks);
  const bodyBg = useBuilderStore((s) => s.bodyBg);
  const styles = useBuilderStore((s) => s.styles);
  const dirty = useBuilderStore((s) => s.dirty);
  const canUndo = useBuilderStore((s) => s.past.length > 0);
  const canRedo = useBuilderStore((s) => s.future.length > 0);

  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);
  const [saveErrors, setSaveErrors] = useState<string[]>([]);
  const [preview, setPreview] = useState<PreviewResult | null>(null);
  const [previewErr, setPreviewErr] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  const { confirm, dialog } = useConfirm();

  // Design vs Code (raw HTML) editing. Code mode requires the document to be a
  // single html block; undoing the conversion drops back to Design.
  const [view, setView] = useState<'design' | 'code'>('design');
  const isHtmlDoc = blocks.length === 1 && blocks[0].type === 'html';
  useEffect(() => {
    if (view === 'code' && !isHtmlDoc) setView('design');
  }, [view, isHtmlDoc]);

  const switchToCode = async () => {
    if (isHtmlDoc) {
      setView('code');
      return;
    }
    const ok = await confirm({
      title: 'Edit as HTML?',
      body: 'Your blocks become one editable HTML snippet. You can keep editing the code, but it won’t convert back into blocks (undo does).',
      confirmLabel: 'Convert to HTML',
    });
    if (!ok) return;
    const s = useBuilderStore.getState();
    s.convertToHtml(blocksToSimpleHtml(s.blocks));
    setView('code');
  };

  // Below 768px the builder is read-only (the NextBuilder mobile pass): the
  // canvas becomes a scrollable preview and editing chrome is hidden.
  const [isMobile, setIsMobile] = useState(false);
  useEffect(() => {
    const check = () => setIsMobile(window.innerWidth < 768);
    check();
    window.addEventListener('resize', check);
    return () => window.removeEventListener('resize', check);
  }, []);

  // Seed the store ONCE per id — a react-query background refetch must not
  // clobber in-progress edits (the A5 seed-once trap).
  const seededId = useRef<string | null>(null);
  useEffect(() => {
    if (isNew) {
      if (seededId.current !== 'new') {
        useBuilderStore.getState().hydrate({ name: '', subject: '', preheader: '', scope: [...DEFAULT_SCOPE], blocks: [] });
        seededId.current = 'new';
      }
      return;
    }
    if (data && seededId.current !== data.id) {
      const bj = data.body_json;
      useBuilderStore.getState().hydrate({
        name: data.name,
        subject: data.subject,
        preheader: data.preheader,
        scope: data.merge_scope ?? [],
        blocks: bj?.blocks ?? [],
        bodyBg: bj?.body_bg ?? '',
        styles: {
          width: bj?.width,
          fontFamily: bj?.font_family,
          textColor: bj?.text_color,
          linkColor: bj?.link_color,
          footerBg: bj?.footer?.bg,
          footerColor: bj?.footer?.color,
          footerText: bj?.footer?.text,
        },
      });
      seededId.current = data.id;
    }
  }, [isNew, data]);

  // Leave the store clean for the next mount (a stale doc flashing on
  // /marketing/content/new would look like data corruption).
  useEffect(() => () => useBuilderStore.getState().reset(), []);

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const variableGroups = useMemo(() => variableGroupsForScope(scope), [scope]);

  // Stable identity matters: PreviewModal refetches the as-recipient render when
  // this changes, so it must only change when the document actually does.
  const previewInput = useMemo(
    () => ({ subject, preheader, body_json: buildBodyJson(blocks, bodyBg, styles), merge_scope: scope }),
    [subject, preheader, blocks, scope, bodyBg, styles],
  );

  // Debounced live compile (server-side /preview): powers the Review checklist,
  // the size badge and the preview modal. The `cancelled` guard discards a
  // superseded/unmounted response (out-of-order race) and keeps "preview failed"
  // distinct from "preview says it's clean".
  useEffect(() => {
    let cancelled = false;
    const t = setTimeout(() => {
      previewContent({ subject, preheader, body_json: buildBodyJson(blocks, bodyBg, styles), merge_scope: scope })
        .then((r) => { if (!cancelled) { setPreview(r); setPreviewErr(false); } })
        .catch(() => { if (!cancelled) { setPreview(null); setPreviewErr(true); } });
    }, 400);
    return () => { cancelled = true; clearTimeout(t); };
  }, [subject, preheader, blocks, scope, bodyBg, styles]);

  // Warn before discarding unsaved work.
  useEffect(() => {
    if (!dirty) return;
    const h = (e: BeforeUnloadEvent) => { e.preventDefault(); e.returnValue = ''; };
    window.addEventListener('beforeunload', h);
    return () => window.removeEventListener('beforeunload', h);
  }, [dirty]);

  // Document-level undo/redo + Escape-deselect. Skipped while typing in inputs
  // or TipTap (they own their local history / focus).
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      const el = e.target as HTMLElement | null;
      if (el?.closest('input, textarea, select, [contenteditable="true"]')) return;
      const s = useBuilderStore.getState();
      if ((e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === 'z') {
        e.preventDefault();
        s.undo();
      } else if ((e.ctrlKey || e.metaKey) && (e.key.toLowerCase() === 'y' || (e.shiftKey && e.key.toLowerCase() === 'z'))) {
        e.preventDefault();
        s.redo();
      } else if (e.key === 'Escape') {
        s.select(null);
      }
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, []);

  const save = async () => {
    setSaveErrors([]);
    const s = useBuilderStore.getState();
    const input = {
      name: s.name.trim(),
      subject: s.subject,
      preheader: s.preheader,
      body_json: buildBodyJson(s.blocks, s.bodyBg, s.styles),
      merge_scope: s.scope,
    };
    try {
      if (isNew) {
        const created = await createMut.mutateAsync(input);
        // Claim the new id BEFORE navigating so the seed effect doesn't re-hydrate
        // over live state when the detail query lands.
        seededId.current = created.id;
        s.markSaved();
        showToast('Content created');
        navigate(`/marketing/content/${created.id}`, { replace: true });
      } else {
        await updateMut.mutateAsync({ id: id as string, input });
        s.markSaved();
        showToast('Saved');
      }
    } catch (e) {
      const se = e as SaveError;
      if (se.validationErrors?.length) setSaveErrors(se.validationErrors);
      showToast(se.message || 'Save failed', 'error');
    }
  };

  const testSend = async () => {
    if (isNew) return;
    try {
      const to = await testSendContent(id as string);
      showToast(`Test sent to ${to}`);
    } catch (e) {
      showToast((e as Error).message || 'Test send failed', 'error');
    }
  };

  if (!isNew && isLoading) return <div className="mx-auto w-full max-w-6xl"><SpinnerBlock label="Loading…" /></div>;

  const busy = createMut.isPending || updateMut.isPending;
  const previewReady = preview !== null && !previewErr;
  const checklist = buildChecklist(name, subject, blocks, preview, previewReady, saveErrors);
  const store = useBuilderStore.getState();

  const goBack = async () => {
    // beforeunload only guards hard unloads — cover the in-app back path too.
    if (dirty) {
      const ok = await confirm({
        title: 'Discard unsaved changes?',
        body: 'This email has unsaved changes. Leaving now discards them.',
        confirmLabel: 'Discard changes',
        tone: 'danger',
      });
      if (!ok) return;
    }
    navigate('/marketing/content');
  };

  return (
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col bg-background">
      {dialog}
      {toast && (
        <div
          role={toast.type === 'error' ? 'alert' : 'status'}
          className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg"
        >
          {toast.type === 'error' ? <AlertCircle className="h-4 w-4 text-destructive" /> : <CheckCircle2 className="h-4 w-4 text-primary" />}
          {toast.msg}
        </div>
      )}

      {/* Toolbar */}
      <div className="flex items-center gap-2 border-b border-border bg-card px-3 py-2">
        <button
          type="button"
          onClick={goBack}
          className="flex items-center gap-1 rounded-lg px-2 py-1.5 text-sm text-muted-foreground hover:bg-accent hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" /> Content
        </button>
        <div className="h-5 w-px bg-border" />
        <input
          value={name}
          onChange={(e) => store.setName(e.target.value)}
          placeholder="Untitled email"
          aria-label="Content name"
          maxLength={160}
          disabled={isMobile}
          className="min-w-0 flex-1 rounded-lg border border-transparent bg-transparent px-2 py-1.5 text-sm font-semibold text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
        />
        {dirty && <Badge variant="warning">Unsaved</Badge>}

        {!isMobile && (
          <>
            <div className="flex items-center gap-0.5">
              <ToolbarIcon title="Undo (Ctrl+Z)" disabled={!canUndo} onClick={() => store.undo()}><Undo2 className="h-4 w-4" /></ToolbarIcon>
              <ToolbarIcon title="Redo (Ctrl+Shift+Z)" disabled={!canRedo} onClick={() => store.redo()}><Redo2 className="h-4 w-4" /></ToolbarIcon>
            </div>
            <div className="h-5 w-px bg-border" />
            {/* Design / Code view toggle */}
            <div className="flex items-center rounded-lg border border-border p-0.5" role="group" aria-label="Editor view">
              <button
                type="button"
                aria-pressed={view === 'design'}
                onClick={() => setView('design')}
                className={`flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                  view === 'design' ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                <LayoutGrid className="h-3.5 w-3.5" /> Design
              </button>
              <button
                type="button"
                aria-pressed={view === 'code'}
                onClick={switchToCode}
                className={`flex items-center gap-1 rounded-md px-2 py-1 text-xs font-medium transition-colors ${
                  view === 'code' ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:text-foreground'
                }`}
              >
                <Code className="h-3.5 w-3.5" /> Code
              </button>
            </div>
            <div className="h-5 w-px bg-border" />
            {preview?.size_bytes != null && (
              <span className={`hidden text-[11px] lg:inline ${preview.too_large ? 'font-medium text-destructive' : 'text-muted-foreground'}`}>
                {Math.round(preview.size_bytes / 1024)} KB
              </span>
            )}
            <Button variant="outline" size="sm" onClick={() => setPreviewOpen(true)}>
              <Eye className="h-4 w-4" /> Preview
            </Button>
            {!isNew && (
              <Button variant="outline" size="sm" onClick={testSend} title="Sends the last saved version to your own email">
                <Send className="h-4 w-4" /> Send test
              </Button>
            )}
            <Button size="sm" onClick={save} disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
          </>
        )}
      </div>

      {isMobile && (
        <div className="flex items-center gap-2 border-b border-border bg-amber-500/10 px-4 py-2 text-xs text-amber-700 dark:text-amber-400">
          <Monitor className="h-3.5 w-3.5 shrink-0" />
          The email builder needs a wider screen — this is a read-only preview.
        </div>
      )}

      {view === 'code' && !isMobile ? (
        <CodeView />
      ) : (
        <EmailBuilder
          variableGroups={variableGroups}
          inspector={{ preview, previewErr, saveErrors, checklist }}
          readOnly={isMobile}
        />
      )}

      <PreviewModal open={previewOpen} onClose={() => setPreviewOpen(false)} preview={preview} previewErr={previewErr} previewInput={previewInput} />
    </div>
  );
};

const ToolbarIcon: React.FC<{ title: string; disabled?: boolean; onClick: () => void; children: React.ReactNode }> = ({ title, disabled, onClick, children }) => (
  <button
    type="button"
    title={title}
    aria-label={title}
    disabled={disabled}
    onClick={onClick}
    className="flex h-8 w-8 items-center justify-center rounded-lg text-muted-foreground hover:bg-accent hover:text-foreground disabled:opacity-30 disabled:hover:bg-transparent"
  >
    {children}
  </button>
);

/** buildBodyJson maps store state to the wire BlockDocument (empty style values
 *  are omitted so untouched docs stay byte-identical). */
function buildBodyJson(blocks: Block[], bodyBg: string, styles: DocStyles): BlockDocument {
  const footer = styles.footerBg || styles.footerColor || styles.footerText?.trim()
    ? { bg: styles.footerBg || undefined, color: styles.footerColor || undefined, text: styles.footerText || undefined }
    : undefined;
  return {
    blocks,
    body_bg: bodyBg || undefined,
    width: styles.width || undefined,
    font_family: styles.fontFamily && styles.fontFamily !== 'arial' ? styles.fontFamily : undefined,
    text_color: styles.textColor || undefined,
    link_color: styles.linkColor || undefined,
    footer,
  };
}

// ok: true = satisfied, false = failing, 'pending' = unknown until a preview lands.
function buildChecklist(
  name: string,
  subject: string,
  blocks: Block[],
  preview: PreviewResult | null,
  previewReady: boolean,
  saveErrors: string[],
): Check[] {
  // Preview-derived rows are 'pending' until a successful compile — never green on
  // a null/failed preview (which would be a false all-clear).
  const pv = (cond: boolean): boolean | 'pending' => (previewReady ? cond : 'pending');
  const out: Check[] = [
    { label: 'Content has a name', ok: name.trim() !== '' },
    { label: 'Subject line is set', ok: subject.trim() !== '' },
    { label: 'No merge-tag scope/fallback errors', ok: saveErrors.length > 0 ? false : pv((preview?.validation_errors?.length ?? 0) === 0) },
    { label: 'Compiled email is under 100KB', ok: pv(!preview?.too_large) },
    { label: 'Content compiles', ok: pv(!preview?.compile_error) },
  ];
  // A seeded button ships href 'https://' — the backend lint counts any non-empty
  // href as "has a link", so catch the dead placeholder client-side.
  const buttons = blocks.flatMap((b) => (b.type === 'button' ? [b] : (b.columns ?? []).flat().filter((s) => s.type === 'button')));
  if (buttons.length > 0) {
    const ok = buttons.every((b) => { const h = (b.href ?? '').trim(); return h !== '' && h !== 'https://'; });
    out.push({ label: 'Every button has a real link', ok });
  }
  for (const w of preview?.warnings ?? []) out.push({ label: w, ok: false });
  return out;
}

export default CampaignContentEditor;
