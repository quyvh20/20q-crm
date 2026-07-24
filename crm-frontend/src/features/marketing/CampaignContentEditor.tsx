import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { AlertCircle, CheckCircle2, ArrowLeft, Send, Sun, Moon, Clock } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import { Button, Input, SpinnerBlock } from '@/components/ui';
import { BlockComposer } from './composer/BlockComposer';
import { MergeTagMenu } from './composer/RichTextEditor';
import { token } from './composer/mergeTagHtml';
import { variableGroupsForScope, SELECTABLE_SCOPES } from './composer/mergeScope';
import { makeBlock, type Block } from './composer/blocks';
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

  const [name, setName] = useState('');
  const [subject, setSubject] = useState('');
  const [preheader, setPreheader] = useState('');
  const [scope, setScope] = useState<string[]>(['contact', 'org', 'campaign']);
  const [blocks, setBlocks] = useState<Block[]>([makeBlock('text')]);
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);
  const [saveErrors, setSaveErrors] = useState<string[]>([]);
  const [dark, setDark] = useState(false);
  const [preview, setPreview] = useState<PreviewResult | null>(null);
  const [previewErr, setPreviewErr] = useState(false);

  // Seed local state ONCE per id — a react-query background refetch must not clobber
  // in-progress edits (the A5 SettingsLayout/seed-once trap).
  const seededId = useRef<string | null>(null);
  useEffect(() => {
    if (isNew) {
      if (seededId.current !== 'new') seededId.current = 'new';
      return;
    }
    if (data && seededId.current !== data.id) {
      setName(data.name);
      setSubject(data.subject);
      setPreheader(data.preheader);
      setScope(data.merge_scope?.length ? data.merge_scope : ['contact', 'org', 'campaign']);
      setBlocks(data.body_json?.blocks?.length ? data.body_json.blocks : [makeBlock('text')]);
      seededId.current = data.id;
    }
  }, [isNew, data]);

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const variableGroups = useMemo(() => variableGroupsForScope(scope), [scope]);

  // Debounced live preview (compiles server-side via /preview). The `cancelled`
  // guard discards a superseded/unmounted response so an older slow compile can't
  // overwrite a newer one (out-of-order race), and distinguishes "preview failed"
  // from "preview says it's clean".
  useEffect(() => {
    let cancelled = false;
    const t = setTimeout(() => {
      previewContent({ subject, preheader, body_json: { blocks }, merge_scope: scope })
        .then((r) => { if (!cancelled) { setPreview(r); setPreviewErr(false); } })
        .catch(() => { if (!cancelled) { setPreview(null); setPreviewErr(true); } });
    }, 400);
    return () => { cancelled = true; clearTimeout(t); };
  }, [subject, preheader, blocks, scope]);

  const toggleScope = (root: string) => {
    setScope((s) => (s.includes(root) ? s.filter((r) => r !== root) : [...s, root]));
  };

  const save = async () => {
    setSaveErrors([]);
    const input = { name: name.trim(), subject, preheader, body_json: { blocks }, merge_scope: scope };
    try {
      if (isNew) {
        const created = await createMut.mutateAsync(input);
        showToast('Content created');
        navigate(`/marketing/content/${created.id}`, { replace: true });
      } else {
        await updateMut.mutateAsync({ id: id as string, input });
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
  const checklist = buildChecklist(name, subject, preview, previewReady, saveErrors);

  return (
    <div className="mx-auto w-full max-w-6xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error' ? <AlertCircle className="h-4 w-4 text-destructive" /> : <CheckCircle2 className="h-4 w-4 text-primary" />}
          {toast.msg}
        </div>
      )}

      <button onClick={() => navigate('/marketing/content')} className="mb-4 -ml-1 flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground">
        <ArrowLeft className="h-4 w-4" /> Back to content
      </button>

      <div className="mb-4 flex flex-wrap items-center justify-between gap-3">
        <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Untitled campaign content" className="max-w-sm text-lg font-semibold" aria-label="Content name" />
        <div className="flex items-center gap-2">
          {!isNew && <Button variant="outline" onClick={testSend}><Send className="h-4 w-4" /> Send test</Button>}
          <Button onClick={save} disabled={busy}>{busy ? 'Saving…' : 'Save'}</Button>
        </div>
      </div>

      <div className="grid gap-6 lg:grid-cols-2">
        {/* Left: authoring */}
        <div className="space-y-4">
          <div>
            <label className="mb-1 flex items-center justify-between text-xs font-medium text-muted-foreground">
              Subject line
              <MergeTagMenu variableGroups={variableGroups} onInsert={(p, f) => setSubject((s) => s + token(p, f))} />
            </label>
            <Input value={subject} onChange={(e) => setSubject(e.target.value)} placeholder="Your subject…" />
          </div>
          <div>
            <label className="mb-1 flex items-center justify-between text-xs font-medium text-muted-foreground">
              Preheader <span className="font-normal">(inbox preview text)</span>
              <MergeTagMenu variableGroups={variableGroups} onInsert={(p, f) => setPreheader((s) => s + token(p, f))} />
            </label>
            <Input value={preheader} onChange={(e) => setPreheader(e.target.value)} placeholder="Short summary shown in the inbox…" />
          </div>

          <div>
            <p className="mb-1 text-xs font-medium text-muted-foreground">Merge scope</p>
            <div className="flex flex-wrap gap-3">
              {SELECTABLE_SCOPES.map((s) => (
                <label key={s.root} className="flex items-center gap-1.5 text-sm text-foreground">
                  <input type="checkbox" checked={scope.includes(s.root)} disabled={s.fixed} onChange={() => toggleScope(s.root)} />
                  {s.label}
                </label>
              ))}
            </div>
          </div>

          <div>
            <p className="mb-2 text-xs font-medium text-muted-foreground">Content blocks</p>
            <BlockComposer blocks={blocks} variableGroups={variableGroups} onChange={setBlocks} />
          </div>
        </div>

        {/* Right: preview + checklist */}
        <div className="space-y-4 lg:sticky lg:top-4 lg:self-start">
          <div className="rounded-xl border border-border bg-card">
            <div className="flex items-center justify-between border-b border-border px-3 py-2">
              <span className="text-xs font-semibold uppercase tracking-wide text-muted-foreground">Preview</span>
              <div className="flex items-center gap-2">
                {preview?.size_bytes != null && (
                  <span className={`text-[11px] ${preview.too_large ? 'text-destructive' : 'text-muted-foreground'}`}>
                    {Math.round(preview.size_bytes / 1024)} KB{preview.too_large ? ' (over 100KB!)' : ''}
                  </span>
                )}
                <button onClick={() => setDark((d) => !d)} title="Toggle dark preview" className="rounded p-1 text-muted-foreground hover:text-foreground">
                  {dark ? <Sun className="h-4 w-4" /> : <Moon className="h-4 w-4" />}
                </button>
              </div>
            </div>
            {preview?.compile_error ? (
              <div className="p-4 text-sm text-destructive">Couldn’t compile: {preview.compile_error}</div>
            ) : previewErr ? (
              <div className="p-4 text-sm text-muted-foreground">Preview unavailable — couldn’t reach the compiler. It’ll refresh on your next edit.</div>
            ) : (
              <iframe
                title="Email preview"
                // sandbox="" = opaque origin, no scripts/forms/same-origin. Static
                // email HTML (tables, inline CSS, images) still renders; any script that
                // somehow reached the compiled output cannot execute or touch the app.
                sandbox=""
                className="h-[32rem] w-full rounded-b-xl"
                style={{ background: dark ? '#0b0b0c' : '#ffffff' }}
                srcDoc={dark ? `<div style="background:#0b0b0c;padding:12px">${preview?.html ?? ''}</div>` : (preview?.html ?? '')}
              />
            )}
          </div>

          <div className="rounded-xl border border-border bg-card p-3">
            <p className="mb-2 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Pre-send checklist</p>
            <ul className="space-y-1.5 text-sm">
              {checklist.map((c, i) => (
                <li key={i} className={`flex items-start gap-2 ${c.ok === true ? 'text-foreground' : c.ok === 'pending' ? 'text-muted-foreground' : 'text-amber-600 dark:text-amber-400'}`}>
                  {c.ok === true ? <CheckCircle2 className="mt-0.5 h-4 w-4 shrink-0 text-emerald-500" />
                    : c.ok === 'pending' ? <Clock className="mt-0.5 h-4 w-4 shrink-0 text-muted-foreground" />
                    : <AlertCircle className="mt-0.5 h-4 w-4 shrink-0" />}
                  <span>{c.label}</span>
                </li>
              ))}
            </ul>
            {saveErrors.length > 0 && (
              <div className="mt-3 rounded-lg border border-destructive/30 bg-destructive/10 p-2 text-xs text-destructive">
                <p className="mb-1 font-medium">Merge-tag problems (blocking save):</p>
                <ul className="list-inside list-disc space-y-0.5">{saveErrors.map((e, i) => <li key={i}>{e}</li>)}</ul>
              </div>
            )}
          </div>
        </div>
      </div>
    </div>
  );
};

// ok: true = satisfied, false = failing, 'pending' = unknown until a preview lands.
interface Check { label: string; ok: boolean | 'pending' }
function buildChecklist(name: string, subject: string, preview: PreviewResult | null, previewReady: boolean, saveErrors: string[]): Check[] {
  // Preview-derived rows are 'pending' until a successful compile — never green on a
  // null/failed preview (which would be a false all-clear).
  const pv = (cond: boolean): boolean | 'pending' => (previewReady ? cond : 'pending');
  const out: Check[] = [
    { label: 'Content has a name', ok: name.trim() !== '' },
    { label: 'Subject line is set', ok: subject.trim() !== '' },
    { label: 'No merge-tag scope/fallback errors', ok: saveErrors.length > 0 ? false : pv((preview?.validation_errors?.length ?? 0) === 0) },
    { label: 'Compiled email is under 100KB', ok: pv(!preview?.too_large) },
    { label: 'Content compiles', ok: pv(!preview?.compile_error) },
  ];
  for (const w of preview?.warnings ?? []) out.push({ label: w, ok: false });
  return out;
}

export default CampaignContentEditor;
