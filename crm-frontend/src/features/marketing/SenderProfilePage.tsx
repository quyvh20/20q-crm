import React, { useEffect, useState } from 'react';
import { AlertCircle, CheckCircle2, ShieldCheck, Plus, Trash2, Tag, Pencil, X, Check } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import {
  Badge, Button, EmptyState, Input, Label, PageHeader, SpinnerBlock, Textarea,
} from '@/components/ui';
import {
  useSenderProfile, useSaveSenderProfile, useResumeMarketing,
  useTopics, useCreateTopic, useUpdateTopic, useDeleteTopic,
} from './senderProfileQueries';
import { senderReasonLabel, type MarketingTopic } from './senderProfileApi';

export const SenderProfilePage: React.FC = () => {
  const { can, loaded } = usePermissions();
  if (!loaded) {
    return <div className="mx-auto w-full max-w-3xl"><SpinnerBlock label="Loading…" /></div>;
  }
  if (!can('marketing.manage')) {
    return (
      <div className="mx-auto w-full max-w-3xl">
        <AccessDeniedPanel capability="marketing.manage" what="the marketing sender profile" />
      </div>
    );
  }
  return <SenderProfileContent />;
};

const SenderProfileContent: React.FC = () => {
  const { data: profile, isLoading, isError } = useSenderProfile();
  const saveMut = useSaveSenderProfile();
  const resumeMut = useResumeMarketing();
  const [toast, setToast] = useState<{ msg: string; type: 'success' | 'error' } | null>(null);

  // draft-over-server dirty tracking (WorkspaceGeneralSection pattern).
  const [draft, setDraft] = useState({ from_name: '', reply_to: '', physical_postal_address: '' });
  const [seeded, setSeeded] = useState(false);
  useEffect(() => {
    if (profile && !seeded) {
      setDraft({
        from_name: profile.from_name,
        reply_to: profile.reply_to,
        physical_postal_address: profile.physical_postal_address,
      });
      setSeeded(true);
    }
  }, [profile, seeded]);

  const dirty = !!profile && (
    draft.from_name !== profile.from_name ||
    draft.reply_to !== profile.reply_to ||
    draft.physical_postal_address !== profile.physical_postal_address
  );

  const showToast = (msg: string, type: 'success' | 'error' = 'success') => {
    setToast({ msg, type });
    setTimeout(() => setToast(null), 4000);
  };

  const save = () => {
    // Trim on submit (the backend trims too) and re-sync the draft to the saved,
    // server-normalized values on success — otherwise trailing whitespace the user
    // pasted keeps `dirty` true forever (the form never settles after Save).
    const trimmed = {
      from_name: draft.from_name.trim(),
      reply_to: draft.reply_to.trim(),
      physical_postal_address: draft.physical_postal_address.trim(),
    };
    saveMut.mutate(trimmed, {
      onSuccess: (saved) => {
        setDraft({
          from_name: saved.from_name,
          reply_to: saved.reply_to,
          physical_postal_address: saved.physical_postal_address,
        });
        showToast('Sender profile saved');
      },
      onError: (e) => showToast((e as Error).message || 'Failed to save', 'error'),
    });
  };

  const resume = () => {
    resumeMut.mutate(undefined, {
      onSuccess: () => showToast('Marketing resumed'),
      onError: (e) => showToast((e as Error).message || 'Failed to resume', 'error'),
    });
  };

  if (isLoading) return <div className="mx-auto w-full max-w-3xl"><SpinnerBlock label="Loading…" /></div>;
  if (isError) {
    return (
      <div className="mx-auto w-full max-w-3xl">
        <div className="flex items-start gap-3 rounded-xl border border-destructive/30 bg-destructive/10 p-4 text-sm">
          <AlertCircle aria-hidden className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
          <div>
            <p className="font-medium text-foreground">Couldn’t load the sender profile</p>
            <p className="text-muted-foreground">Reload the page to try again.</p>
          </div>
        </div>
      </div>
    );
  }

  const sendable = profile?.sendable ?? false;
  const paused = profile?.marketing_paused ?? false;

  return (
    <div className="mx-auto w-full max-w-3xl">
      {toast && (
        <div className="fixed right-4 top-4 z-50 flex items-center gap-3 rounded-lg border border-border bg-card px-4 py-3 text-sm font-medium text-foreground shadow-lg">
          {toast.type === 'error'
            ? <AlertCircle aria-hidden className="h-4 w-4 shrink-0 text-destructive" />
            : <CheckCircle2 aria-hidden className="h-4 w-4 shrink-0 text-primary" />}
          {toast.msg}
        </div>
      )}

      <PageHeader
        title="Sender profile"
        description="The from-identity and physical postal address included in every marketing email. Anti-spam law (CAN-SPAM, CASL) requires a real postal address — marketing sending stays blocked until it’s set."
      />

      {/* Readiness banner */}
      <div className={`mb-6 flex items-start gap-3 rounded-xl border p-4 text-sm ${sendable ? 'border-emerald-500/30 bg-emerald-500/10' : 'border-amber-500/30 bg-amber-500/10'}`}>
        <ShieldCheck aria-hidden className={`mt-0.5 h-5 w-5 shrink-0 ${sendable ? 'text-emerald-600 dark:text-emerald-400' : 'text-amber-600 dark:text-amber-400'}`} />
        <div className="flex-1">
          <p className="font-medium text-foreground">
            {sendable ? 'Sender profile is complete' : paused ? 'Marketing is paused' : 'Marketing sending is blocked'}
          </p>
          <p className="text-muted-foreground">
            {sendable ? 'Your marketing emails will carry a compliant from-identity and postal footer.' : senderReasonLabel(profile?.reason ?? '')}
          </p>
        </div>
        {paused && (
          <Button size="sm" variant="outline" onClick={resume} disabled={resumeMut.isPending}>Resume marketing</Button>
        )}
      </div>

      {/* Profile form */}
      <div className="space-y-4 rounded-xl border border-border bg-card p-5">
        <div>
          <Label htmlFor="from_name">From name</Label>
          <Input id="from_name" value={draft.from_name} placeholder="Acme Marketing"
            onChange={(e) => setDraft((d) => ({ ...d, from_name: e.target.value }))} />
          <p className="mt-1 text-xs text-muted-foreground">The display name recipients see in their inbox.</p>
        </div>
        <div>
          <Label htmlFor="reply_to">Reply-to address <span className="text-muted-foreground">(optional)</span></Label>
          <Input id="reply_to" type="email" value={draft.reply_to} placeholder="hello@yourcompany.com"
            onChange={(e) => setDraft((d) => ({ ...d, reply_to: e.target.value }))} />
        </div>
        <div>
          <Label htmlFor="postal">Physical postal address</Label>
          <Textarea id="postal" rows={3} value={draft.physical_postal_address}
            placeholder="Acme Inc, 123 Main St, Springfield, IL 62704, USA"
            onChange={(e) => setDraft((d) => ({ ...d, physical_postal_address: e.target.value }))} />
          <p className="mt-1 text-xs text-muted-foreground">Rendered in the footer of every marketing email (legally required).</p>
        </div>
        <div className="flex justify-end">
          <Button onClick={save} disabled={!dirty || saveMut.isPending}>Save sender profile</Button>
        </div>
      </div>

      <TopicsSection onToast={showToast} />
    </div>
  );
};

const TopicsSection: React.FC<{ onToast: (msg: string, type?: 'success' | 'error') => void }> = ({ onToast }) => {
  const { data: topics, isLoading, isError } = useTopics();
  const createMut = useCreateTopic();
  const deleteMut = useDeleteTopic();
  const [name, setName] = useState('');
  const [description, setDescription] = useState('');
  const [optIn, setOptIn] = useState(false);

  const add = () => {
    const n = name.trim();
    if (!n) return;
    createMut.mutate({ name: n, description: description.trim(), opt_in_default: optIn }, {
      onSuccess: () => { setName(''); setDescription(''); setOptIn(false); onToast('Topic created'); },
      onError: (e) => onToast((e as Error).message || 'Failed to create topic', 'error'),
    });
  };

  const remove = (t: MarketingTopic) => {
    if (!confirm(`Delete the “${t.name}” topic? Existing opt-outs for it are kept.`)) return;
    deleteMut.mutate(t.id, {
      onSuccess: () => onToast('Topic deleted'),
      onError: (e) => onToast((e as Error).message || 'Failed to delete topic', 'error'),
    });
  };

  return (
    <div className="mt-8">
      <PageHeader
        title="Topics"
        description="Optional subscription categories recipients can opt out of individually. A topic’s default (opt-in vs opt-out) is fixed once it’s created."
      />

      <div className="mb-4 space-y-3 rounded-xl border border-border bg-card p-4">
        <div className="flex flex-wrap items-end gap-2">
          <div className="min-w-[12rem] flex-1">
            <Label htmlFor="topic-name">Topic name</Label>
            <Input id="topic-name" value={name} onChange={(e) => setName(e.target.value)} placeholder="Product news" />
          </div>
          <div className="min-w-[12rem] flex-[2]">
            <Label htmlFor="topic-desc">Description <span className="text-muted-foreground">(optional)</span></Label>
            <Input id="topic-desc" value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Feature launches and release notes" />
          </div>
        </div>
        <div className="flex items-center justify-between">
          <label className="flex items-center gap-2 text-sm text-muted-foreground">
            <input type="checkbox" checked={optIn} onChange={(e) => setOptIn(e.target.checked)}
              className="h-4 w-4 rounded border-border" />
            Recipients are opted in by default (immutable after creation)
          </label>
          <Button onClick={add} disabled={createMut.isPending || !name.trim()}>
            <Plus aria-hidden /> Add topic
          </Button>
        </div>
      </div>

      {isLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : isError ? (
        <div className="flex items-start gap-3 rounded-xl border border-destructive/30 bg-destructive/10 p-4 text-sm">
          <AlertCircle aria-hidden className="mt-0.5 h-5 w-5 shrink-0 text-destructive" />
          <div>
            <p className="font-medium text-foreground">Couldn’t load topics</p>
            <p className="text-muted-foreground">Reload the page to try again — your topics haven’t been lost.</p>
          </div>
        </div>
      ) : !topics || topics.length === 0 ? (
        <EmptyState icon={Tag} title="No topics yet" description="Topics are optional. Without them, recipients simply unsubscribe from all marketing." />
      ) : (
        <div className="space-y-2">
          {topics.map((t) => (
            <TopicRow key={t.id} topic={t} onDelete={() => remove(t)} onToast={onToast} />
          ))}
        </div>
      )}
    </div>
  );
};

const TopicRow: React.FC<{
  topic: MarketingTopic;
  onDelete: () => void;
  onToast: (msg: string, type?: 'success' | 'error') => void;
}> = ({ topic, onDelete, onToast }) => {
  const updateMut = useUpdateTopic();
  const [editing, setEditing] = useState(false);
  const [name, setName] = useState(topic.name);
  const [description, setDescription] = useState(topic.description);

  const saveEdit = () => {
    const n = name.trim();
    if (!n) return;
    updateMut.mutate({ id: topic.id, name: n, description: description.trim() }, {
      onSuccess: () => { setEditing(false); onToast('Topic updated'); },
      onError: (e) => onToast((e as Error).message || 'Failed to update topic', 'error'),
    });
  };

  if (editing) {
    return (
      <div className="flex flex-wrap items-end gap-2 rounded-lg border border-border bg-card p-3">
        <div className="min-w-[10rem] flex-1">
          <Input value={name} onChange={(e) => setName(e.target.value)} placeholder="Topic name" />
        </div>
        <div className="min-w-[10rem] flex-[2]">
          <Input value={description} onChange={(e) => setDescription(e.target.value)} placeholder="Description" />
        </div>
        <Button size="sm" onClick={saveEdit} disabled={updateMut.isPending || !name.trim()}><Check aria-hidden className="h-3.5 w-3.5" /> Save</Button>
        <Button size="sm" variant="ghost" onClick={() => { setEditing(false); setName(topic.name); setDescription(topic.description); }}><X aria-hidden className="h-3.5 w-3.5" /></Button>
      </div>
    );
  }

  return (
    <div className="flex items-center justify-between gap-3 rounded-lg border border-border bg-card p-3">
      <div className="min-w-0">
        <div className="flex items-center gap-2">
          <p className="truncate text-sm font-medium text-foreground">{topic.name}</p>
          <Badge variant={topic.opt_in_default ? 'success' : 'secondary'}>
            {topic.opt_in_default ? 'opt-in default' : 'opt-out default'}
          </Badge>
        </div>
        {topic.description && <p className="truncate text-xs text-muted-foreground">{topic.description}</p>}
      </div>
      <div className="flex shrink-0 items-center gap-1">
        <Button size="sm" variant="ghost" onClick={() => setEditing(true)} title="Edit topic"><Pencil aria-hidden className="h-4 w-4" /></Button>
        <Button size="sm" variant="ghost" onClick={onDelete} className="text-destructive hover:text-destructive" title="Delete topic"><Trash2 aria-hidden className="h-4 w-4" /></Button>
      </div>
    </div>
  );
};

export default SenderProfilePage;
