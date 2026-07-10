import React, { useEffect, useMemo, useRef, useState } from 'react';
import { useNavigate, useParams } from 'react-router-dom';
import { useQuery } from '@tanstack/react-query';
import { ArrowLeft, Save, Send, AlertTriangle } from 'lucide-react';
import { useEmailTemplate, useSaveEmailTemplate, useTestSendEmailTemplate } from './queries';
import { getWorkflowSchema } from './api';
import { EmailTemplateBodyEditor, type VariableGroup } from './builder/config/EmailTemplateBodyEditor';

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
    return (
      <div className="flex justify-center py-24">
        <div className="h-8 w-8 animate-spin rounded-full border-2 border-primary border-t-transparent" />
      </div>
    );
  }

  // A failed load of an existing template must NOT fall through to a blank, editable
  // form — saving that would overwrite the real subject/body/scope with empties.
  // Block with an explicit error + retry when the fetch errored and we have no data.
  if (!isNew && isError && !existing) {
    return (
      <div className="mx-auto max-w-3xl px-4 py-8">
        <button
          onClick={() => navigate('/workflows/email-templates')}
          className="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
        >
          <ArrowLeft className="h-4 w-4" /> Email Templates
        </button>
        <div className="flex flex-col items-center gap-3 rounded-2xl border border-destructive/40 bg-destructive/10 py-16 text-center">
          <AlertTriangle className="h-8 w-8 text-destructive" />
          <p className="text-foreground">Couldn't load this template.</p>
          <button
            onClick={() => refetch()}
            className="rounded-lg bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:bg-primary/90"
          >
            Retry
          </button>
        </div>
      </div>
    );
  }

  return (
    <div className="mx-auto max-w-3xl px-4 py-8">
      {toast && (
        <div
          className={`fixed right-4 top-4 z-50 rounded-xl px-4 py-3 text-sm font-medium text-white shadow-lg ${
            toast.type === 'error' ? 'bg-red-500/90' : 'bg-emerald-500/90'
          }`}
        >
          {toast.msg}
        </div>
      )}

      <button
        onClick={() => navigate('/workflows/email-templates')}
        className="mb-4 inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground"
      >
        <ArrowLeft className="h-4 w-4" /> Email Templates
      </button>

      <div className="mb-6 flex items-center justify-between gap-4">
        <h1 className="text-2xl font-bold text-foreground">{isNew ? 'New email template' : 'Edit email template'}</h1>
        <div className="flex items-center gap-2">
          {!isNew && (
            <button
              onClick={handleTestSend}
              disabled={testSend.isPending || dirty}
              title={dirty ? 'Save your changes first' : 'Send a test to yourself'}
              className="inline-flex items-center gap-1.5 rounded-lg border border-border bg-background px-3 py-2 text-sm text-foreground transition-colors hover:bg-accent disabled:opacity-50"
            >
              <Send className="h-4 w-4" /> {testSend.isPending ? 'Sending…' : 'Test send'}
            </button>
          )}
          <button
            onClick={handleSave}
            disabled={saveMutation.isPending}
            className="inline-flex items-center gap-1.5 rounded-lg bg-primary px-4 py-2 text-sm font-medium text-primary-foreground transition-colors hover:bg-primary/90 disabled:opacity-50"
          >
            <Save className="h-4 w-4" /> {saveMutation.isPending ? 'Saving…' : 'Save'}
          </button>
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
