import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { AlertCircle, ArrowLeft, CheckCircle2, Save, Send, AlertTriangle } from 'lucide-react';
import { useEmailTemplate, useSaveEmailTemplate, useTestSendEmailTemplate } from './queries';
import { useDocumentTitle } from '../../lib/useDocumentTitle';
import { getWorkflowSchema } from './api';
import { EmailTemplateBodyEditor, type VariableGroup } from './builder/config/EmailTemplateBodyEditor';
import { Button, SpinnerBlock } from '@/components/ui';

// Meta scopes always resolvable regardless of the template's object scope.
const META_SCOPES = new Set(['trigger', 'org', 'user', 'actions']);

/** Email template create/edit page (A5). The body is authored as HTML here; A5.3
 *  swaps the textarea for a TipTap editor with an inline merge-tag node. */
export const EmailTemplateEditor: React.FC = () => {
  const navigate = useNavigate();
  const { id } = useParams<{ id: string }>();
  const isNew = !id || id === 'new';

  const { data: existing, isLoading, isError, refetch } = useEmailTemplate(isNew ? undefined : id);
  const saveMutation = useSaveEmailTemplate();
  const testSend = useTestSendEmailTemplate();

  // Tab title from the SAVED template (U7.2) — `existing.name` from react-query,
  // never the `name` useState below, which is bound to the name input and would
  // retitle the tab on every keystroke.
  useDocumentTitle(isNew ? 'New Email Template' : existing?.name);

  // Object catalog for the merge-scope select (entities + custom objects).
  const { data: schema } = useQuery({ queryKey: ['workflowSchema'], queryFn: getWorkflowSchema, staleTime: 60_000 });
  const objectOptions = useMemo(() => {
    if (!schema) return [];
    return [...schema.entities.filter((e) => e.key !== 'trigger'), ...(schema.custom_objects || [])].map((e) => ({
      key: e.key,
      label: e.label,
    }));
  }, [schema]);

  const [name, setName] = useState('');
  const [subject, setSubject] = useState('');
  const [objectSlug, setObjectSlug] = useState('');
  const [bodyHtml, setBodyHtml] = useState('');
  const [bodyJson, setBodyJson] = useState<unknown>(undefined);
  const [dirty, setDirty] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);

  // Merge-tag catalog for the editor, scoped to the template's object (+ meta scopes).
  const variableGroups: VariableGroup[] = useMemo(() => {
    if (!schema) return [];
    const all = [...schema.entities, ...(schema.custom_objects || [])];
    const scoped = objectSlug ? all.filter((e) => e.key === objectSlug || META_SCOPES.has(e.key)) : all;
    return scoped
      .map((e) => ({ key: e.key, label: e.label, fields: e.fields.map((f) => ({ path: f.path, label: f.label })) }))
      .filter((g) => g.fields.length > 0);
  }, [schema, objectSlug]);

  // Seed the form ONCE per template id. body_html/body_json seed the save state so
  // a save without touching the body preserves the loaded content (the editor only
  // overwrites these via onChange when actually edited). Guarding on the id (not just
  // `existing`) means a background refetch — e.g. refetchOnWindowFocus after the stale
  // window — can't re-seed and clobber the user's in-progress edits.
  const seededIdRef = useRef<string | null>(null);
  useEffect(() => {
    if (existing && seededIdRef.current !== existing.id) {
      seededIdRef.current = existing.id;
      setName(existing.name);
      setSubject(existing.subject);
      setObjectSlug(existing.object_slug || '');
      setBodyHtml(existing.body_html);
      setBodyJson(existing.body_json);
      setDirty(false);
    }
  }, [existing]);

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const markDirty = <T,>(setter: (v: T) => void) => (v: T) => {
    setter(v);
    setDirty(true);
    setError(null);
  };

  const handleSave = () => {
    if (!name.trim()) {
      setError('Name is required');
      return;
    }
    saveMutation.mutate(
      { id: isNew ? null : id!, input: { name: name.trim(), subject, body_html: bodyHtml, body_json: bodyJson, object_slug: objectSlug } },
      {
        onSuccess: (tmpl) => {
          setDirty(false);
          if (isNew) navigate(`/workflows/email-templates/${tmpl.id}`, { replace: true });
          else showToast('Template saved');
        },
        onError: (e) => setError((e as Error).message || 'Failed to save template'),
      },
    );
  };

  const handleTestSend = () => {
    if (isNew || !id) return;
    testSend.mutate(id, {
      onSuccess: (r) => showToast(`Test email sent to ${r.to}`),
      onError: (e) => showToast((e as Error).message || 'Failed to send test', 'error'),
    });
  };

  const inputCls =
    'w-full rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring';

  if (!isNew && isLoading) {
    return <SpinnerBlock label="Loading…" className="py-24" />;
  }

  // A failed load of an existing template must NOT fall through to a blank, editable
  // form — saving that would overwrite the real subject/body/scope with empties.
  // Block with an explicit error + retry when the fetch errored and we have no data.
  if (!isNew && isError && !existing) {
    return (
      <div className="mx-auto w-full max-w-3xl">
        <Button
          variant="ghost"
          size="sm"
          onClick={() => navigate('/workflows/email-templates')}
          className="mb-4 -ml-2 text-muted-foreground"
        >
          <ArrowLeft aria-hidden /> Email Templates
        </Button>
        <div className="flex flex-col items-center gap-3 rounded-2xl border border-destructive/40 bg-destructive/10 py-16 text-center">
          <AlertTriangle aria-hidden className="h-8 w-8 text-destructive" />
          <p className="text-foreground">Couldn't load this template.</p>
          <Button onClick={() => refetch()}>Retry</Button>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto w-full max-w-3xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error' ? (
            <AlertCircle aria-hidden className="h-4 w-4 shrink-0 text-destructive" />
          ) : (
            <CheckCircle2 aria-hidden className="h-4 w-4 shrink-0 text-primary" />
          )}
          {toast.msg}
        </div>
      )}

      <Button
        variant="ghost"
        size="sm"
        onClick={() => navigate('/workflows/email-templates')}
        className="mb-4 -ml-2 text-muted-foreground"
      >
        <ArrowLeft aria-hidden /> Email Templates
      </Button>

      <div className="mb-6 flex items-center justify-between gap-4">
        <h1 className="text-2xl font-semibold tracking-tight text-foreground">{isNew ? 'New email template' : 'Edit email template'}</h1>
        <div className="flex items-center gap-2">
          {!isNew && (
            <Button
              variant="outline"
              onClick={handleTestSend}
              disabled={testSend.isPending || dirty}
              title={dirty ? 'Save your changes first' : 'Send a test to yourself'}
            >
              <Send aria-hidden /> {testSend.isPending ? 'Sending…' : 'Test send'}
            </Button>
          )}
          <Button
            onClick={handleSave}
            disabled={saveMutation.isPending}
          >
            <Save aria-hidden /> {saveMutation.isPending ? 'Saving…' : 'Save'}
          </Button>
        </div>
      </div>

      {error && (
        <div className="mb-4 rounded-lg border border-destructive/40 bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>
      )}

      <div className="space-y-4 rounded-2xl border border-border bg-card p-5">
        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <div>
            <label className="mb-1 block text-sm text-muted-foreground">Name</label>
            <input value={name} onChange={(e) => markDirty(setName)(e.target.value)} placeholder="Welcome email" className={inputCls} />
          </div>
          <div>
            <label className="mb-1 block text-sm text-muted-foreground">Merge scope (optional)</label>
            <select value={objectSlug} onChange={(e) => markDirty(setObjectSlug)(e.target.value)} className={inputCls}>
              <option value="">Unscoped</option>
              {objectOptions.map((o) => (
                <option key={o.key} value={o.key}>{o.label}</option>
              ))}
            </select>
          </div>
        </div>

        <div>
          <label className="mb-1 block text-sm text-muted-foreground">Subject</label>
          <input
            value={subject}
            onChange={(e) => markDirty(setSubject)(e.target.value)}
            placeholder="Welcome aboard, {{contact.first_name}}"
            className={inputCls}
          />
        </div>

        <div>
          <label className="mb-1 block text-sm text-muted-foreground">Body</label>
          <EmailTemplateBodyEditor
            key={existing?.id ?? 'new'}
            initialHtml={existing?.body_html ?? ''}
            initialJson={existing?.body_json}
            variableGroups={variableGroups}
            onChange={(html, json) => {
              setBodyHtml(html);
              setBodyJson(json);
              setDirty(true);
              setError(null);
            }}
          />
          <p className="mt-1 text-xs text-muted-foreground">
            Use <span className="font-medium text-foreground">Insert variable</span> to add merge tags like{' '}
            <code className="rounded bg-muted px-1 py-0.5 font-mono">{'{{contact.first_name}}'}</code> — they resolve when the email is sent.
          </p>
        </div>
      </div>
    </div>
  );
};

export default EmailTemplateEditor;
