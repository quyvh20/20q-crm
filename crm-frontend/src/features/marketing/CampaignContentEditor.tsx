// The marketing email builder page (M6.2 rebuilt as a drag-and-drop editor):
// full-height toolbar + palette / WYSIWYG canvas / inspector, document-level
// undo/redo, and a server-compiled preview modal. The wire contract is unchanged
// — the store's blocks ARE body_json.blocks (merge chips serialized to bare
// {{path|fallback}} tokens on every edit) and save/preview/test-send hit the
// same endpoints through the same React Query hooks.

import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import {
  ArrowLeft, Code, Eye, LayoutGrid, Monitor, Redo2, Send, Sparkles, Undo2,
} from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { useConfirm } from '../../components/common/ConfirmDialog';
import { Badge, Button, SpinnerBlock } from '@/components/ui';
import { useToast } from '@/lib/useToast';
import { EmailBuilder } from './composer/EmailBuilder';
import { CodeView } from './composer/CodeView';
import { PreviewModal } from './composer/PreviewModal';
import { AICopilotModal, type AIMode } from './composer/AICopilotModal';
import { blocksToSimpleHtml } from './composer/htmlSerializer';
import { useBuilderStore, DEFAULT_SCOPE, type BuilderSeed, type DocStyles } from './composer/builderStore';
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

  const toast = useToast();
  const [saveErrors, setSaveErrors] = useState<string[]>([]);
  const [preview, setPreview] = useState<PreviewResult | null>(null);
  const [previewErr, setPreviewErr] = useState(false);
  const [previewOpen, setPreviewOpen] = useState(false);
  // The copilot surface. Mode is chosen by the entry point: the toolbar writes a
  // whole email on an empty canvas and adds a section otherwise; the inspector's
  // per-block button rewrites.
  const [aiMode, setAiMode] = useState<AIMode | null>(null);
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

  // Draft persistence: the SPA router can't block in-app navigation
  // (BrowserRouter, no data router), so instead of trying to trap every exit,
  // the working copy is checkpointed to localStorage while dirty and offered
  // back on the next visit — work survives navigation, crashes and reloads.
  const draftKey = `mkt_builder_draft_${isNew ? 'new' : id}`;
  const [draftOffer, setDraftOffer] = useState<BuilderSeed | null>(null);

  // Seed the store ONCE per id — a react-query background refetch must not
  // clobber in-progress edits (the A5 seed-once trap).
  const seededId = useRef<string | null>(null);
  useEffect(() => {
    const offerDraft = (serverSeed: BuilderSeed) => {
      try {
        const raw = localStorage.getItem(draftKey);
        if (!raw) return;
        const d = JSON.parse(raw) as BuilderSeed;
        // A draft identical to the server copy is stale bookkeeping, not work.
        const strip = (s: BuilderSeed) => JSON.stringify([s.name, s.subject, s.preheader, s.scope, s.blocks, s.bodyBg ?? '', s.styles ?? {}]);
        if (strip(d) === strip(serverSeed)) {
          localStorage.removeItem(draftKey);
          return;
        }
        setDraftOffer(d);
      } catch {
        // Unreadable draft — drop it.
        try { localStorage.removeItem(draftKey); } catch { /* ignore */ }
      }
    };
    if (isNew) {
      if (seededId.current !== 'new') {
        const seed: BuilderSeed = { name: '', subject: '', preheader: '', scope: [...DEFAULT_SCOPE], blocks: [] };
        useBuilderStore.getState().hydrate(seed);
        seededId.current = 'new';
        offerDraft(seed);
      }
      return;
    }
    if (data && seededId.current !== data.id) {
      const bj = data.body_json;
      const seed: BuilderSeed = {
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
          lineHeight: bj?.line_height,
          footerBg: bj?.footer?.bg,
          footerColor: bj?.footer?.color,
          footerText: bj?.footer?.text,
        },
      };
      useBuilderStore.getState().hydrate(seed);
      seededId.current = data.id;
      offerDraft(seed);
    }
  }, [isNew, data, draftKey]);

  // Checkpoint the working copy while dirty (debounced; best-effort).
  useEffect(() => {
    if (!dirty) return;
    const t = setTimeout(() => {
      // The route id can change without unmounting this component (in-app
      // navigation between two emails). Only checkpoint once the store is
      // known to hold THIS id's document, or one email's content would be
      // written under another's key and offered back as its "draft".
      if (seededId.current !== (isNew ? 'new' : id)) return;
      const s = useBuilderStore.getState();
      try {
        localStorage.setItem(draftKey, JSON.stringify({
          name: s.name, subject: s.subject, preheader: s.preheader, scope: s.scope,
          blocks: s.blocks, bodyBg: s.bodyBg, styles: s.styles,
        } satisfies BuilderSeed));
      } catch { /* quota/private mode — checkpointing is best-effort */ }
    }, 800);
    return () => clearTimeout(t);
  }, [dirty, name, subject, preheader, scope, blocks, bodyBg, styles, draftKey, isNew, id]);

  const restoreDraft = () => {
    if (!draftOffer) return;
    // Applied as a normal undoable edit — restoring never silently discards
    // work done in this session, and Ctrl+Z undoes the restore itself.
    useBuilderStore.getState().applyDraft(draftOffer);
    setDraftOffer(null);
  };
  const discardDraft = () => {
    try { localStorage.removeItem(draftKey); } catch { /* ignore */ }
    setDraftOffer(null);
  };

  // Leave the store clean for the next mount (a stale doc flashing on
  // /marketing/content/new would look like data corruption).
  useEffect(() => () => useBuilderStore.getState().reset(), []);

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

  // Document-level keyboard verbs: undo/redo, Escape-deselect, Delete-remove,
  // Ctrl+D duplicate. Skipped while typing in inputs or TipTap (they own their
  // local editing / focus).
  useEffect(() => {
    const h = (e: KeyboardEvent) => {
      // e.target is not always an Element (window/document dispatch, synthetic
      // events in tests) — closest() would throw and kill the handler.
      const el = e.target instanceof Element ? e.target : null;
      // A dialog owns the keyboard while it is open: Escape should close IT, and
      // Ctrl+Z should not rewrite the document behind it.
      if (el?.closest('[role="dialog"], [role="alertdialog"]')) return;
      const s = useBuilderStore.getState();
      const isUndo = (e.ctrlKey || e.metaKey) && !e.shiftKey && e.key.toLowerCase() === 'z';
      const isRedo = (e.ctrlKey || e.metaKey) && (e.key.toLowerCase() === 'y' || (e.shiftKey && e.key.toLowerCase() === 'z'));
      // Inside TipTap, only undo/redo route to the document history (its own
      // history is disabled — one undo stack); every other key stays native so
      // Delete deletes text, not the block. Form fields keep everything native.
      if (el?.closest('[contenteditable="true"]')) {
        if (isUndo) {
          e.preventDefault();
          s.undo();
        } else if (isRedo) {
          e.preventDefault();
          s.redo();
        }
        return;
      }
      if (el?.closest('input, textarea, select')) return;
      // Destructive verbs only fire when the canvas owns the interaction.
      // Selecting a block leaves focus on <body> (clicks on divs don't focus),
      // so body counts — but a focused control elsewhere in the chrome must
      // never let Backspace delete the block behind it.
      const canvasOwnsKey = !el || el === document.body || !!el.closest('[data-testid="builder-canvas"]');
      if (isUndo) {
        e.preventDefault();
        s.undo();
      } else if (isRedo) {
        e.preventDefault();
        s.redo();
      } else if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 'd') {
        if (s.selectedId && canvasOwnsKey) {
          e.preventDefault();
          s.duplicateBlock(s.selectedId);
        }
      } else if (e.key === 'Delete' || e.key === 'Backspace') {
        if (s.selectedId && canvasOwnsKey) {
          e.preventDefault();
          s.removeBlock(s.selectedId);
        }
      } else if (e.key === 'Escape') {
        s.select(null);
      }
    };
    window.addEventListener('keydown', h);
    return () => window.removeEventListener('keydown', h);
  }, []);

  const save = async (): Promise<boolean> => {
    setSaveErrors([]);
    const s = useBuilderStore.getState();
    const input = {
      name: s.name.trim(),
      subject: s.subject,
      preheader: s.preheader,
      body_json: buildBodyJson(s.blocks, s.bodyBg, s.styles),
      merge_scope: s.scope,
    };
    // The document can move on while the request is in flight; markSaved only
    // reports clean when the working copy still matches what was sent, and the
    // checkpoint is kept otherwise so those keystrokes stay recoverable.
    const sentSignature = s.docSignature();
    try {
      if (isNew) {
        const created = await createMut.mutateAsync(input);
        // Claim the new id BEFORE navigating so the seed effect doesn't re-hydrate
        // over live state when the detail query lands.
        seededId.current = created.id;
        if (s.markSaved(sentSignature)) {
          try { localStorage.removeItem(draftKey); } catch { /* ignore */ }
        }
        toast.show('Content created');
        navigate(`/marketing/content/${created.id}`, { replace: true });
      } else {
        await updateMut.mutateAsync({ id: id as string, input });
        if (s.markSaved(sentSignature)) {
          try { localStorage.removeItem(draftKey); } catch { /* ignore */ }
        }
        toast.show('Saved');
      }
      return true;
    } catch (e) {
      const se = e as SaveError;
      if (se.validationErrors?.length) setSaveErrors(se.validationErrors);
      toast.error(se.message || 'Save failed');
      return false;
    }
  };

  const testSend = async () => {
    if (isNew) return;
    // The endpoint sends the last SAVED version — a dirty editor would silently
    // test stale content, so offer to save first.
    if (useBuilderStore.getState().dirty) {
      const ok = await confirm({
        title: 'Save before sending the test?',
        body: 'The test email sends the last saved version — your current changes aren’t in it yet.',
        confirmLabel: 'Save & send test',
      });
      if (!ok) return;
      const saved = await save();
      if (!saved) return;
    }
    try {
      const to = await testSendContent(id as string);
      toast.show(`Test sent to ${to}`);
    } catch (e) {
      toast.error((e as Error).message || 'Test send failed');
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
      // An explicit discard also drops the checkpoint — no zombie restore offer.
      try { localStorage.removeItem(draftKey); } catch { /* ignore */ }
    }
    navigate('/marketing/content');
  };

  return (
    <div className="-m-6 flex h-[calc(100%+3rem)] min-h-0 flex-col bg-background">
      {dialog}
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
            <Button
              variant="outline"
              size="sm"
              onClick={() => setAiMode(blocks.length === 0 ? 'email' : 'section')}
              title={blocks.length === 0 ? 'Write this email with AI' : 'Add a section with AI'}
            >
              <Sparkles className="h-4 w-4" /> AI
            </Button>
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

      {draftOffer && !isMobile && (
        <div className="flex items-center gap-3 border-b border-border bg-primary/5 px-4 py-2 text-xs text-foreground">
          <span>You have unsaved changes from a previous session of this email.</span>
          <Button size="sm" onClick={restoreDraft}>Restore</Button>
          <Button variant="outline" size="sm" onClick={discardDraft}>Discard</Button>
        </div>
      )}

      {view === 'code' && !isMobile ? (
        <CodeView />
      ) : (
        <EmailBuilder
          variableGroups={variableGroups}
          inspector={{ preview, previewErr, saveErrors, checklist, onRewriteWithAI: () => setAiMode('rewrite') }}
          readOnly={isMobile}
        />
      )}

      <AICopilotModal open={aiMode !== null} mode={aiMode ?? 'section'} onClose={() => setAiMode(null)} />

      <PreviewModal open={previewOpen} onClose={() => setPreviewOpen(false)} preview={preview} previewErr={previewErr} previewInput={previewInput} docWidth={styles.width} />
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
    line_height: styles.lineHeight || undefined,
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
  const flat = blocks.flatMap((b) => [b, ...(b.columns ?? []).flat()]);
  const buttons = flat.filter((b) => b.type === 'button');
  if (buttons.length > 0) {
    const ok = buttons.every((b) => { const h = (b.href ?? '').trim(); return h !== '' && h !== 'https://'; });
    out.push({ label: 'Every button has a real link', ok });
  }
  // Empty-src image/video blocks ship as broken images (the compiler emits the
  // tag unconditionally).
  const media = flat.filter((b) => b.type === 'image' || b.type === 'video');
  if (media.length > 0) {
    out.push({ label: 'Every image has a picture selected', ok: media.every((b) => (b.src ?? '').trim() !== '') });
  }
  // Menu links seeded from presets keep the 'https://' placeholder — dead nav.
  const menuItems = flat.filter((b) => b.type === 'menu').flatMap((b) => b.items ?? []).filter((it) => it.label.trim() !== '');
  if (menuItems.length > 0) {
    const ok = menuItems.every((it) => { const h = it.href.trim(); return h !== '' && h !== 'https://'; });
    out.push({ label: 'Every menu link has a real URL', ok });
  }
  for (const w of preview?.warnings ?? []) out.push({ label: w, ok: false });
  return out;
}

export default CampaignContentEditor;
