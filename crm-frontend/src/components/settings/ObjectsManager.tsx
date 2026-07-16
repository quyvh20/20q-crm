import { useState, useEffect, useCallback } from 'react';
import { Link } from 'react-router-dom';
import { Check, ChevronDown, ChevronUp, Pencil, Plus, Search, Trash2, X } from 'lucide-react';
import {
  listRegistryObjects, getObjectSchema, getObjectDef,
  createObjectDef, updateObjectDef, deleteObjectDef,
  createFieldDef, updateFieldDef, deleteFieldDef, setObjectNumberPrefix,
  listObjectLayouts, createObjectLayout, updateObjectLayout, deleteObjectLayout, setLayoutRoles,
  getPermissionGrid,
  type ObjectSummary, type CustomFieldDef, type FieldType, type ObjectFieldDescriptor,
  type ObjectLayout, type LayoutSection, type LayoutField, type PermRoleInfo,
} from '../../lib/api';
import { useConfirm } from '../common/ConfirmDialog';
import { prettyRole } from '../../lib/roles';
import {
  Badge, Button, Input, Label, Select, SpinnerBlock,
  Table, TableBody, TableCell, TableHead, TableHeader, TableRow, TableShell,
} from '@/components/ui';

// ObjectsManager is the single admin surface for every object's schema (P7 — it
// replaces the separate CustomFieldManager + ObjectDefManager now that object_defs/
// object_fields is one store). It lists all objects from the registry; custom
// objects are created/edited/deleted via the custom-object API, while system
// objects' native fields are read-only and their admin-defined custom fields are
// managed through the settings field API. Both write to object_fields.
//
// P8 adds a Layouts tab to every object editor (except deals, which have a fixed
// Kanban-centric layout).

const ICONS = ['📦', '🏗️', '🚗', '📋', '🎯', '💼', '🏠', '📊', '🔧', '📝', '🎪', '🧩', '📁', '🗂️', '⚙️', '🛒'];
const FIELD_TYPES: FieldType[] = ['text', 'number', 'date', 'select', 'boolean', 'url', 'relation', 'mirror'];
// Type option labels keep their leading glyph: they render inside native <option>
// elements, which cannot host an SVG icon.
const typeLabel = (t: string) => ({ text: 'Aa Text', number: '# Number', date: '📅 Date', select: '▼ Select', boolean: '✓ Yes/No', url: '🔗 URL', relation: '↗ Relation', mirror: '⇄ Mirror' }[t] || t);
const autoSlug = (s: string) => s.toLowerCase().replace(/[^a-z0-9]+/g, '_').replace(/^_|_$/g, '').slice(0, 50);

/** Emoji chosen as an object's icon is user data — box it in a neutral container. */
function ObjectIcon({ icon, className = '' }: { icon: string; className?: string }) {
  return (
    <span aria-hidden className={`inline-flex h-7 w-7 shrink-0 items-center justify-center rounded bg-muted text-base ${className}`}>
      {icon}
    </span>
  );
}

type Mode = { kind: 'list' } | { kind: 'new' } | { kind: 'edit'; slug: string; isSystem: boolean };

export default function ObjectsManager() {
  const [objects, setObjects] = useState<ObjectSummary[]>([]);
  const [loading, setLoading] = useState(true);
  const [mode, setMode] = useState<Mode>({ kind: 'list' });
  const [error, setError] = useState('');
  const [confirmDelete, setConfirmDelete] = useState<string | null>(null);
  // Label of an object created this visit — drives the access-review nudge
  // (U3.6): a new object is invisible to any role without an access grant, and
  // creation is the moment the admin can still remember to fix that.
  const [justCreated, setJustCreated] = useState('');

  const fetchObjects = useCallback(async () => {
    setLoading(true);
    try {
      setObjects(await listRegistryObjects());
      setError('');
    } catch (e) {
      // A load failure used to render as an empty object list — say what happened.
      setObjects([]);
      setError(e instanceof Error ? e.message : 'Failed to load objects');
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { fetchObjects(); }, [fetchObjects]);

  const handleDelete = async (slug: string) => {
    setError('');
    try {
      await deleteObjectDef(slug);
      setConfirmDelete(null);
      fetchObjects();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Delete failed');
    }
  };

  if (loading) return <SpinnerBlock label="Loading…" />;

  if (mode.kind === 'new') {
    return (
      <CustomObjectForm
        onDone={(createdLabel) => {
          setMode({ kind: 'list' });
          fetchObjects();
          if (createdLabel) setJustCreated(createdLabel);
        }}
        onCancel={() => setMode({ kind: 'list' })}
      />
    );
  }
  if (mode.kind === 'edit') {
    return mode.isSystem
      ? <SystemFieldsEditor slug={mode.slug} onBack={() => { setMode({ kind: 'list' }); fetchObjects(); }} />
      : <CustomObjectForm editSlug={mode.slug} onDone={() => { setMode({ kind: 'list' }); fetchObjects(); }} onCancel={() => setMode({ kind: 'list' })} />;
  }

  return (
    <div>
      <p className="mt-0 text-sm text-muted-foreground">
        Every object — built-in or custom — and its fields, in one place.
      </p>
      {error && <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>}

      {/* Post-creation access nudge (U3.6): roles without an access grant can't
          see the new object anywhere — say so while the admin is still here. */}
      {justCreated && (
        <div className="mb-3 flex items-start gap-2 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-sm text-amber-600 dark:text-amber-400">
          <span className="flex-1">
            <strong>{justCreated}</strong> was created. Roles without an access grant won't see
            it anywhere — review who can use it in{' '}
            <Link to="/settings/object-access" className="font-semibold underline">
              Object Access
            </Link>.
          </span>
          <button
            type="button"
            onClick={() => setJustCreated('')}
            aria-label="Dismiss access reminder"
            className="rounded p-0.5 leading-none hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
          >
            <X className="h-4 w-4" />
          </button>
        </div>
      )}

      <TableShell className="mb-4">
        <Table>
          <TableHeader>
            <TableRow className="hover:bg-transparent">
              <TableHead className="w-10" />
              <TableHead>Label</TableHead>
              <TableHead>Slug</TableHead>
              <TableHead>Type</TableHead>
              <TableHead>Fields</TableHead>
              <TableHead className="text-right" />
            </TableRow>
          </TableHeader>
          <TableBody>
            {objects.map(o => (
              <TableRow key={o.slug} className="hover:bg-transparent">
                <TableCell><ObjectIcon icon={o.icon} /></TableCell>
                <TableCell className="font-medium">{o.label} <span className="font-normal text-muted-foreground">/ {o.label_plural}</span></TableCell>
                <TableCell><code className="rounded bg-muted px-1.5 py-0.5 text-[13px]">{o.slug}</code></TableCell>
                <TableCell>
                  <Badge variant={o.is_system ? 'default' : 'success'}>{o.is_system ? 'Built-in' : 'Custom'}</Badge>
                </TableCell>
                <TableCell className="text-muted-foreground">{o.field_count}</TableCell>
                <TableCell className="text-right">
                  {confirmDelete === o.slug ? (
                    <div className="flex items-center justify-end gap-2">
                      <Button variant="destructive" size="sm" onClick={() => handleDelete(o.slug)}>Confirm</Button>
                      <Button variant="secondary" size="sm" onClick={() => setConfirmDelete(null)}>Cancel</Button>
                    </div>
                  ) : (
                    <div className="flex items-center justify-end gap-1">
                      <Button variant="ghost" size="icon" className="h-8 w-8" onClick={() => setMode({ kind: 'edit', slug: o.slug, isSystem: o.is_system })} title="Edit fields">
                        <Pencil className="h-4 w-4" />
                      </Button>
                      {!o.is_system && (
                        <Button variant="ghost" size="icon" className="h-8 w-8 text-muted-foreground hover:text-destructive" onClick={() => setConfirmDelete(o.slug)} title="Delete object">
                          <Trash2 className="h-4 w-4" />
                        </Button>
                      )}
                    </div>
                  )}
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
      </TableShell>

      <Button onClick={() => setMode({ kind: 'new' })}>
        <Plus aria-hidden /> New Object
      </Button>
    </div>
  );
}

// ============================================================
// Shared tab bar
// ============================================================

function TabBar({ active, onSelect, tabs }: { active: string; onSelect: (t: string) => void; tabs: string[] }) {
  return (
    <div className="mb-5 flex gap-0.5 border-b border-border">
      {tabs.map(t => (
        <button
          key={t}
          type="button"
          onClick={() => onSelect(t)}
          className={`px-4 py-2 text-[13px] capitalize transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
            active === t
              ? 'border-b-2 border-primary font-semibold text-primary'
              : 'border-b-2 border-transparent text-muted-foreground hover:text-foreground'
          }`}
        >
          {t}
        </button>
      ))}
    </div>
  );
}

// ============================================================
// Record-number prefix editor (admin sets the DEAL-0001 style prefix per object)
// ============================================================

function NumberPrefixEditor({ slug }: { slug: string }) {
  const [prefix, setPrefix] = useState('');
  const [saving, setSaving] = useState(false);
  const [saved, setSaved] = useState(false);
  const [err, setErr] = useState('');

  useEffect(() => {
    let cancelled = false;
    getObjectSchema(slug)
      .then(s => { if (!cancelled) setPrefix(s.number_prefix || ''); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [slug]);

  const save = async () => {
    setSaving(true); setErr(''); setSaved(false);
    try { await setObjectNumberPrefix(slug, prefix.trim()); setSaved(true); }
    catch (e) { setErr(e instanceof Error ? e.message : 'Failed to save prefix'); }
    finally { setSaving(false); }
  };

  const sample = `${(prefix.trim() || slug).toUpperCase()}-0001`;
  return (
    <div className="mb-3 rounded-lg border border-border bg-muted p-3">
      <Label className="mb-1 block text-[13px]">Record number prefix</Label>
      <div className="flex flex-wrap items-center gap-2">
        <Input
          value={prefix}
          onChange={e => { setPrefix(e.target.value.toUpperCase().slice(0, 16)); setSaved(false); }}
          placeholder={slug.toUpperCase()}
          className="w-40"
        />
        <span className="text-xs text-muted-foreground">e.g. <code>{sample}</code></span>
        <Button size="sm" onClick={save} disabled={saving}>{saving ? 'Saving…' : 'Save'}</Button>
        {saved && <span className="inline-flex items-center gap-1 text-xs text-emerald-600 dark:text-emerald-400"><Check className="h-3.5 w-3.5" aria-hidden /> Saved</span>}
      </div>
      {err && <p className="mt-1 text-xs text-destructive">{err}</p>}
      <p className="mt-1 text-[11px] text-muted-foreground">A friendly identifier shown on each record instead of its database id. Blank uses the object name.</p>
    </div>
  );
}

// ============================================================
// Field builder (shared add/edit form for one field)
// ============================================================

interface FieldDraft {
  key: string;
  label: string;
  type: string;
  options: string[];
  required: boolean;
  target_slug?: string;
  via_field?: string;
  source_field?: string;
}
const emptyDraft: FieldDraft = { key: '', label: '', type: 'text', options: [], required: false, target_slug: '', via_field: '', source_field: '' };

function FieldBuilder({ draft, setDraft, onAdd, editing, currentSlug, relations = [] }: {
  draft: FieldDraft; setDraft: (d: FieldDraft) => void; onAdd: () => void; editing: boolean;
  /** The object being edited, excluded from relation targets to avoid an obvious self-loop. */
  currentSlug?: string;
  /** This object's relation fields (from the current draft), used as a mirror's "via" choices. */
  relations?: { key: string; label: string; target_slug?: string }[];
}) {
  const [optInput, setOptInput] = useState('');
  const [objects, setObjects] = useState<ObjectSummary[]>([]);
  // Fields of the currently-chosen via relation's target (the mirror "source" choices).
  const [sourceFields, setSourceFields] = useState<ObjectFieldDescriptor[]>([]);

  // Relation targets are every registered object; loaded lazily so the field
  // builder only pays for it when relations are in play.
  useEffect(() => {
    let cancelled = false;
    listRegistryObjects().then(o => { if (!cancelled) setObjects(o); }).catch(() => {});
    return () => { cancelled = true; };
  }, []);

  // A mirror follows one of this object's relation fields. Use the relations the
  // parent is currently editing (so a just-added, not-yet-saved relation shows up),
  // limited to those with a resolvable target to read a field from.
  const viaCandidates = relations.filter(r => !!r.target_slug);

  // Once a via relation is chosen, load its target object's fields to mirror from.
  const viaTarget = viaCandidates.find(r => r.key === draft.via_field)?.target_slug;
  useEffect(() => {
    if (draft.type !== 'mirror' || !viaTarget) { setSourceFields([]); return; }
    let cancelled = false;
    getObjectSchema(viaTarget)
      .then(s => { if (!cancelled) setSourceFields(s.fields); })
      .catch(() => {});
    return () => { cancelled = true; };
  }, [draft.type, viaTarget]);

  const isRelation = draft.type === 'relation';
  const isMirror = draft.type === 'mirror';
  // A relation needs a target; a mirror needs both a via relation and a source field.
  const canSubmit = draft.label.trim()
    && (!isRelation || !!draft.target_slug)
    && (!isMirror || (!!draft.via_field && !!draft.source_field));

  return (
    <div className="rounded-lg border border-border bg-muted p-3">
      <div className="grid grid-cols-[1fr_1fr_auto_auto] items-end gap-2">
        <div>
          <Label className="text-xs text-muted-foreground">Field Label</Label>
          <Input value={draft.label} onChange={e => setDraft({ ...draft, label: e.target.value, key: editing ? draft.key : autoSlug(e.target.value) })} placeholder="e.g. Priority" />
        </div>
        <div>
          <Label className="text-xs text-muted-foreground">Type</Label>
          <Select value={draft.type} onChange={e => setDraft({ ...draft, type: e.target.value })}>
            {FIELD_TYPES.map(t => <option key={t} value={t}>{typeLabel(t)}</option>)}
          </Select>
        </div>
        <label className="flex cursor-pointer items-center gap-1 pb-2 text-xs text-muted-foreground">
          <input type="checkbox" checked={draft.required} onChange={e => setDraft({ ...draft, required: e.target.checked })} /> Req
        </label>
        <Button size="sm" onClick={onAdd} disabled={!canSubmit}>{editing ? 'Save' : '+ Add'}</Button>
      </div>
      {isRelation && (
        <div className="mt-2">
          <Label className="text-xs text-muted-foreground">Related object</Label>
          <Select
            value={draft.target_slug || ''}
            onChange={e => setDraft({ ...draft, target_slug: e.target.value })}
          >
            <option value="">— Choose an object —</option>
            {objects.filter(o => o.slug !== currentSlug).map(o => (
              <option key={o.slug} value={o.slug}>{o.icon} {o.label}</option>
            ))}
          </Select>
          <p className="mt-1 text-[11px] text-muted-foreground">
            This field links each record to one {draft.target_slug ? objects.find(o => o.slug === draft.target_slug)?.label || 'record' : 'record'}; the related object will show these in a related list.
          </p>
        </div>
      )}
      {isMirror && (
        <div className="mt-2 grid grid-cols-2 gap-2">
          <div>
            <Label className="text-xs text-muted-foreground">Via relation</Label>
            <Select
              value={draft.via_field || ''}
              onChange={e => setDraft({ ...draft, via_field: e.target.value, source_field: '' })}
            >
              <option value="">— Choose a relation —</option>
              {viaCandidates.map(r => <option key={r.key} value={r.key}>{r.label}</option>)}
            </Select>
          </div>
          <div>
            <Label className="text-xs text-muted-foreground">Show which field</Label>
            <Select
              value={draft.source_field || ''}
              onChange={e => setDraft({ ...draft, source_field: e.target.value })}
              disabled={!draft.via_field}
            >
              <option value="">{draft.via_field ? '— Choose a field —' : 'Pick a relation first'}</option>
              {sourceFields.map(f => <option key={f.key} value={f.key}>{f.label}</option>)}
            </Select>
          </div>
          <p className="col-span-2 m-0 text-[11px] text-muted-foreground">
            {relations.length === 0
              ? 'Add a Relation field to this object first — a mirror displays a field from the record it links to.'
              : viaCandidates.length === 0
                ? 'This object has relation fields, but none has a related object set yet. Give a relation a target first, then a mirror can pull a field from it.'
                : `Read-only: shows the ${sourceFields.find(f => f.key === draft.source_field)?.label || 'chosen field'} of the linked ${objects.find(o => o.slug === viaTarget)?.label || 'record'}, kept in sync.`}
          </p>
        </div>
      )}
      {draft.type === 'select' && (
        <div className="mt-2">
          <Label className="text-xs text-muted-foreground">Options (press Enter)</Label>
          <div className="mb-1 flex flex-wrap gap-1">
            {draft.options.map(opt => (
              <Badge key={opt} className="gap-1">
                {opt}
                <button type="button" onClick={() => setDraft({ ...draft, options: draft.options.filter(o => o !== opt) })} className="rounded hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                  <X className="h-3 w-3" />
                </button>
              </Badge>
            ))}
          </div>
          <Input value={optInput} onChange={e => setOptInput(e.target.value)}
            onKeyDown={e => { if (e.key === 'Enter' && optInput.trim()) { e.preventDefault(); setDraft({ ...draft, options: [...draft.options, optInput.trim()] }); setOptInput(''); } }}
            placeholder="Type and press Enter" />
        </div>
      )}
    </div>
  );
}

// ============================================================
// Custom object create/edit (label/icon/searchable + fields array)
// ============================================================

// onDone receives the created object's label on the CREATE path (undefined on
// edit) so the list view can show the access-review nudge (U3.6).
function CustomObjectForm({ editSlug, onDone, onCancel }: { editSlug?: string; onDone: (createdLabel?: string) => void; onCancel: () => void }) {
  const [tab, setTab] = useState<'fields' | 'layouts'>('fields');
  const [label, setLabel] = useState('');
  const [slug, setSlug] = useState('');
  const [labelPlural, setLabelPlural] = useState('');
  const [icon, setIcon] = useState('📦');
  const [searchable, setSearchable] = useState(false);
  const [fields, setFields] = useState<CustomFieldDef[]>([]);
  const [draft, setDraft] = useState<FieldDraft>({ ...emptyDraft });
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(!!editSlug);

  useEffect(() => {
    if (!editSlug) return;
    getObjectDef(editSlug).then(def => {
      setLabel(def.label); setSlug(def.slug); setLabelPlural(def.label_plural);
      setIcon(def.icon); setSearchable(def.searchable ?? false); setFields(def.fields || []);
      setLoading(false);
    }).catch(() => { setError('Failed to load object'); setLoading(false); });
  }, [editSlug]);

  const onLabel = (v: string) => { setLabel(v); if (!editSlug) { setSlug(autoSlug(v)); setLabelPlural(v ? v + 's' : ''); } };

  const addField = () => {
    if (!draft.label.trim()) return;
    const key = draft.key || autoSlug(draft.label);
    if (fields.some(f => f.key === key)) { setError(`Duplicate field key: ${key}`); return; }
    if (draft.type === 'relation' && !draft.target_slug) { setError('Choose a related object for the relation field'); return; }
    if (draft.type === 'mirror' && (!draft.via_field || !draft.source_field)) { setError('Choose a relation and a field for the mirror'); return; }
    const f: CustomFieldDef = { key, label: draft.label.trim(), type: draft.type as FieldType, required: draft.required, position: fields.length };
    if (draft.type === 'select') f.options = [...draft.options];
    if (draft.type === 'relation') f.target_slug = draft.target_slug;
    if (draft.type === 'mirror') { f.via_field = draft.via_field; f.source_field = draft.source_field; }
    setFields([...fields, f]);
    setDraft({ ...emptyDraft });
    setError('');
  };

  const save = async () => {
    setError('');
    try {
      if (editSlug) {
        await updateObjectDef(editSlug, { label, label_plural: labelPlural, icon, fields, searchable });
        onDone();
      } else {
        await createObjectDef({ slug, label, label_plural: labelPlural, icon, fields, searchable });
        onDone(label);
      }
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    }
  };

  if (loading) return <SpinnerBlock label="Loading…" />;

  return (
    <div>
      <h4 className="mb-4 mt-0 text-base font-semibold">{editSlug ? `Edit ${label}` : 'New Custom Object'}</h4>
      {error && <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>}

      {/* Layout tab is only available once the object exists */}
      {editSlug && <TabBar active={tab} onSelect={t => setTab(t as 'fields' | 'layouts')} tabs={['fields', 'layouts']} />}

      {tab === 'fields' ? (
        <>
          <div className="mb-4 grid grid-cols-3 gap-3">
            <div><Label className="mb-1 block text-[13px]">Label *</Label><Input value={label} onChange={e => onLabel(e.target.value)} placeholder="e.g. Project" /></div>
            <div><Label className="mb-1 block text-[13px]">Slug</Label><Input value={slug} onChange={e => setSlug(e.target.value)} disabled={!!editSlug} /></div>
            <div><Label className="mb-1 block text-[13px]">Plural Label</Label><Input value={labelPlural} onChange={e => setLabelPlural(e.target.value)} placeholder="e.g. Projects" /></div>
          </div>

          <div className="mb-4">
            <Label className="mb-1 block text-[13px]">Icon</Label>
            <div className="flex flex-wrap gap-1">
              {ICONS.map(ic => (
                <button
                  key={ic}
                  type="button"
                  onClick={() => setIcon(ic)}
                  aria-label={`Use icon ${ic}`}
                  className={`rounded-lg border px-2 py-1 text-xl focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${icon === ic ? 'border-primary bg-primary/10' : 'border-border bg-card hover:bg-accent'}`}
                >{ic}</button>
              ))}
            </div>
          </div>

          <div className="mb-4">
            <label className="flex cursor-pointer items-center gap-2 text-[13px] font-medium">
              <input type="checkbox" checked={searchable} onChange={e => setSearchable(e.target.checked)} />
              <Search className="h-3.5 w-3.5 text-muted-foreground" aria-hidden /> Searchable
            </label>
            <p className="ml-6 mt-1 text-xs text-muted-foreground">Index records for semantic + full-text global search and AI.</p>
          </div>

          {/* Record-number prefix is editable once the object exists (has a slug). */}
          {editSlug && <NumberPrefixEditor slug={editSlug} />}

          <div className="mb-4">
            <Label className="mb-2 block text-[13px]">Fields ({fields.length})</Label>
            {fields.length > 0 && (
              <div className="mb-2 overflow-hidden rounded-lg border border-border">
                {fields.map((f, i) => (
                  <div key={f.key} className={`flex items-center px-2.5 py-1.5 ${i < fields.length - 1 ? 'border-b border-border' : ''}`}>
                    <span className="flex-1 text-[13px] font-medium">{f.label}</span>
                    <code className="mr-2 text-xs text-muted-foreground">{f.key}</code>
                    <span className="mr-2 text-xs text-primary">{typeLabel(f.type)}</span>
                    {f.required && <span className="mr-2 text-[11px] text-destructive">Required</span>}
                    <button type="button" onClick={() => setFields(fields.filter(x => x.key !== f.key))} aria-label={`Remove ${f.label}`} className="rounded text-muted-foreground hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring">
                      <X className="h-4 w-4" />
                    </button>
                  </div>
                ))}
              </div>
            )}
            <FieldBuilder
              draft={draft} setDraft={setDraft} onAdd={addField} editing={false} currentSlug={slug}
              relations={fields.filter(f => f.type === 'relation').map(f => ({ key: f.key, label: f.label, target_slug: f.target_slug }))}
            />
          </div>

          <div className="flex gap-2">
            <Button onClick={save}>{editSlug ? 'Update Object' : 'Create Object'}</Button>
            <Button variant="secondary" onClick={onCancel}>Cancel</Button>
          </div>
        </>
      ) : (
        // Layouts tab — only reachable when editSlug is set
        <LayoutsEditor slug={editSlug!} fieldKeys={fields.map(f => ({ key: f.key, label: f.label }))} />
      )}
    </div>
  );
}

// ============================================================
// System object fields editor (native read-only; custom fields via settings API)
// ============================================================

interface SchemaFieldRow { key: string; label: string; type: string; is_system: boolean; required: boolean; options?: string[]; target_slug?: string; via_field?: string; source_field?: string }

function SystemFieldsEditor({ slug, onBack }: { slug: string; onBack: () => void }) {
  const [tab, setTab] = useState<'fields' | 'layouts'>('fields');
  const [label, setLabel] = useState(slug);
  const [icon, setIcon] = useState('📦');
  const [rows, setRows] = useState<SchemaFieldRow[]>([]);
  const [draft, setDraft] = useState<FieldDraft>({ ...emptyDraft });
  const [editingKey, setEditingKey] = useState<string | null>(null);
  const [error, setError] = useState('');
  const [loading, setLoading] = useState(true);
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

  const load = useCallback(async () => {
    setLoading(true);
    try {
      const s = await getObjectSchema(slug);
      setLabel(s.label); setIcon(s.icon);
      setRows(s.fields.map(f => ({ key: f.key, label: f.label, type: f.type, is_system: f.is_system, required: f.required, options: f.options, target_slug: f.target_slug, via_field: f.via_field, source_field: f.source_field })));
    } catch {
      setError('Failed to load fields');
    } finally {
      setLoading(false);
    }
  }, [slug]);
  useEffect(() => { load(); }, [load]);

  const saveField = async () => {
    if (!draft.label.trim()) return;
    setError('');
    const key = draft.key || autoSlug(draft.label);
    if (draft.type === 'relation' && !draft.target_slug) { setError('Choose a related object for the relation field'); return; }
    if (draft.type === 'mirror' && (!draft.via_field || !draft.source_field)) { setError('Choose a relation and a field for the mirror'); return; }
    const payload = {
      label: draft.label.trim(), type: draft.type, required: draft.required,
      options: draft.type === 'select' ? draft.options : undefined,
      target_slug: draft.type === 'relation' ? draft.target_slug : undefined,
      via_field: draft.type === 'mirror' ? draft.via_field : undefined,
      source_field: draft.type === 'mirror' ? draft.source_field : undefined,
    };
    try {
      if (editingKey) {
        await updateFieldDef(editingKey, payload);
      } else {
        if (rows.some(r => r.key === key)) { setError(`Duplicate field key: ${key}`); return; }
        await createFieldDef({ key, entity_type: slug, ...payload });
      }
      setDraft({ ...emptyDraft });
      setEditingKey(null);
      load();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
    }
  };

  const editField = (r: SchemaFieldRow) => {
    setEditingKey(r.key);
    setDraft({ key: r.key, label: r.label, type: r.type, options: r.options || [], required: r.required, target_slug: r.target_slug || '', via_field: r.via_field || '', source_field: r.source_field || '' });
  };

  const removeField = async (key: string) => {
    // Deleting a field deletes its DATA on every record — never one-click.
    if (!(await confirmDialog({
      title: `Delete the "${key}" field`,
      body: 'This removes the field and its values from every record of this object. This cannot be undone.',
      confirmLabel: 'Delete field',
    }))) return;
    setError('');
    try { await deleteFieldDef(key); load(); } catch (e) { setError(e instanceof Error ? e.message : 'Delete failed'); }
  };

  if (loading) return <SpinnerBlock label="Loading…" />;

  // Deals use a Kanban layout — no point offering a custom section builder for them.
  const showLayouts = slug !== 'deal';

  return (
    <div>
      <h4 className="mb-1 mt-0 flex items-center gap-2 text-base font-semibold"><ObjectIcon icon={icon} className="h-6 w-6 text-sm" /> {label} <span className="text-xs font-normal text-primary">· Built-in</span></h4>

      {showLayouts
        ? <TabBar active={tab} onSelect={t => setTab(t as 'fields' | 'layouts')} tabs={['fields', 'layouts']} />
        : <p className="mt-0 text-[13px] text-muted-foreground">Built-in fields are fixed; add or edit your own custom fields below.</p>
      }

      {tab === 'fields' ? (
        <>
          {showLayouts || (
            <p className="mt-0 text-[13px] text-muted-foreground">Built-in fields are fixed; add or edit your own custom fields below.</p>
          )}
          {error && <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>}

          <NumberPrefixEditor slug={slug} />

          <div className="mb-3 overflow-hidden rounded-lg border border-border">
            {rows.map((r, i) => (
              <div key={r.key} className={`flex items-center px-2.5 py-2 ${i < rows.length - 1 ? 'border-b border-border' : ''} ${r.is_system ? 'bg-muted' : 'bg-card'}`}>
                <span className="flex-1 text-[13px] font-medium">{r.label}</span>
                <code className="mr-2 text-xs text-muted-foreground">{r.key}</code>
                <span className="mr-2 text-xs text-primary">{typeLabel(r.type)}</span>
                {r.is_system ? (
                  <span className="text-[11px] text-muted-foreground">Built-in</span>
                ) : (
                  <div className="flex items-center gap-1">
                    <button type="button" onClick={() => editField(r)} title="Edit" className="rounded p-0.5 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Pencil className="h-3.5 w-3.5" /></button>
                    <button type="button" onClick={() => removeField(r.key)} title="Delete" className="rounded p-0.5 text-muted-foreground hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-4 w-4" /></button>
                  </div>
                )}
              </div>
            ))}
          </div>

          <Label className="mb-1.5 block text-[13px]">{editingKey ? `Edit field "${editingKey}"` : 'Add a custom field'}</Label>
          <FieldBuilder
            draft={draft} setDraft={setDraft} onAdd={saveField} editing={!!editingKey} currentSlug={slug}
            relations={rows.filter(r => r.type === 'relation').map(r => ({ key: r.key, label: r.label, target_slug: r.target_slug }))}
          />

          <div className="mt-4 flex gap-2">
            <Button variant="secondary" onClick={onBack}>← Back to objects</Button>
            {editingKey && <Button variant="outline" onClick={() => { setEditingKey(null); setDraft({ ...emptyDraft }); }}>Cancel edit</Button>}
          </div>
        </>
      ) : (
        <LayoutsEditor
          slug={slug}
          fieldKeys={rows.map(r => ({ key: r.key, label: r.label }))}
        />
      )}

      {tab === 'layouts' && (
        <div className="mt-5">
          <Button variant="secondary" onClick={onBack}>← Back to objects</Button>
        </div>
      )}
      {confirmDialogEl}
    </div>
  );
}

// ============================================================
// P8 — LayoutsEditor (list + create/edit a single layout)
// ============================================================

interface FieldEntry { key: string; label: string }

function LayoutsEditor({ slug, fieldKeys }: { slug: string; fieldKeys: FieldEntry[] }) {
  const [layouts, setLayouts] = useState<ObjectLayout[]>([]);
  const [roles, setRoles] = useState<PermRoleInfo[]>([]);
  const [editing, setEditing] = useState<ObjectLayout | null>(null);
  const [creating, setCreating] = useState(false);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState('');
  const { confirm: confirmDialog, dialog: confirmDialogEl } = useConfirm();

  const load = useCallback(async () => {
    setLoading(true);
    setError('');
    try {
      const [ls, grid] = await Promise.all([listObjectLayouts(slug), getPermissionGrid()]);
      setLayouts(ls);
      // Filter out owner-bypass roles since layout assignment for them is meaningless.
      setRoles(grid.roles.filter(r => !r.is_owner));
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Failed to load layouts');
    } finally {
      setLoading(false);
    }
  }, [slug]);

  useEffect(() => { load(); }, [load]);

  const handleDelete = async (id: string) => {
    if (!(await confirmDialog({
      title: 'Delete layout',
      body: 'Roles assigned to this layout fall back to the default layout (or a flat field list). This cannot be undone.',
      confirmLabel: 'Delete layout',
    }))) return;
    try { await deleteObjectLayout(slug, id); load(); } catch (e) { setError(e instanceof Error ? e.message : 'Delete failed'); }
  };

  if (loading) return <SpinnerBlock label="Loading layouts…" />;

  if (creating || editing) {
    return (
      <LayoutForm
        slug={slug}
        fieldKeys={fieldKeys}
        roles={roles}
        initial={editing ?? undefined}
        onSave={() => { setCreating(false); setEditing(null); load(); }}
        onCancel={() => { setCreating(false); setEditing(null); }}
      />
    );
  }

  const roleMap = Object.fromEntries(roles.map(r => [r.id, r.name]));

  return (
    <div>
      <p className="mt-0 text-[13px] text-muted-foreground">
        Layouts control how fields are arranged on a record's detail page, per role. A role sees
        the layout assigned to it; roles without one see the default layout, or a simple field
        list if no default exists.
      </p>
      {error && <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>}

      {layouts.length === 0 ? (
        <div className="py-8 text-center text-[13px] text-muted-foreground">
          No layouts yet. All roles see a flat list of fields ordered by position.
        </div>
      ) : (
        <div className="mb-3 overflow-hidden rounded-lg border border-border">
          {layouts.map((l, i) => (
            <div key={l.id} className={`flex items-center px-3 py-2.5 ${i < layouts.length - 1 ? 'border-b border-border' : ''}`}>
              <div className="flex-1">
                <div className="flex items-center gap-2">
                  <span className="text-[13px] font-medium">{l.name}</span>
                  {l.is_default && <Badge variant="success">default</Badge>}
                </div>
                {l.role_ids.length > 0 && (
                  <div className="mt-1 flex flex-wrap gap-1">
                    {l.role_ids.map(rid => (
                      <Badge key={rid}>{roleMap[rid] ?? rid}</Badge>
                    ))}
                  </div>
                )}
              </div>
              <span className="mr-3 text-xs text-muted-foreground">{(l.layout ?? []).length} sections</span>
              <button type="button" onClick={() => setEditing(l)} title="Edit" className="rounded p-1 text-muted-foreground hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Pencil className="h-4 w-4" /></button>
              <button type="button" onClick={() => handleDelete(l.id)} title="Delete" className="rounded p-1 text-muted-foreground hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><Trash2 className="h-4 w-4" /></button>
            </div>
          ))}
        </div>
      )}

      <Button onClick={() => setCreating(true)}><Plus aria-hidden /> New Layout</Button>
      {confirmDialogEl}
    </div>
  );
}

// ============================================================
// LayoutForm — create or edit one named layout
// ============================================================

function LayoutForm({
  slug,
  fieldKeys,
  roles,
  initial,
  onSave,
  onCancel,
}: {
  slug: string;
  fieldKeys: FieldEntry[];
  roles: PermRoleInfo[];
  initial?: ObjectLayout;
  onSave: () => void;
  onCancel: () => void;
}) {
  const [name, setName] = useState(initial?.name ?? '');
  const [isDefault, setIsDefault] = useState(initial?.is_default ?? false);
  const [selectedRoles, setSelectedRoles] = useState<Set<string>>(new Set(initial?.role_ids ?? []));
  const [sections, setSections] = useState<LayoutSection[]>(initial?.layout ?? []);
  const [error, setError] = useState('');
  const [saving, setSaving] = useState(false);

  const addSection = () => {
    const id = `sec_${Date.now()}`;
    setSections(prev => [...prev, { id, label: 'New Section', columns: 1, fields: [] }]);
  };

  const removeSection = (id: string) => setSections(prev => prev.filter(s => s.id !== id));

  const updateSection = (id: string, patch: Partial<LayoutSection>) =>
    setSections(prev => prev.map(s => s.id === id ? { ...s, ...patch } : s));

  const addFieldToSection = (sectionId: string, key: string) => {
    setSections(prev => prev.map(s => {
      if (s.id !== sectionId) return s;
      if (s.fields.some(f => f.key === key)) return s;
      return { ...s, fields: [...s.fields, { key }] };
    }));
  };

  const removeFieldFromSection = (sectionId: string, key: string) =>
    setSections(prev => prev.map(s =>
      s.id === sectionId ? { ...s, fields: s.fields.filter(f => f.key !== key) } : s
    ));

  const setFieldWidth = (sectionId: string, key: string, width: LayoutField['width']) =>
    setSections(prev => prev.map(s =>
      s.id === sectionId
        ? { ...s, fields: s.fields.map(f => f.key === key ? { ...f, width } : f) }
        : s
    ));

  const moveSectionUp = (idx: number) => {
    if (idx === 0) return;
    setSections(prev => { const a = [...prev]; [a[idx - 1], a[idx]] = [a[idx], a[idx - 1]]; return a; });
  };
  const moveSectionDown = (idx: number) => {
    setSections(prev => {
      if (idx >= prev.length - 1) return prev;
      const a = [...prev]; [a[idx], a[idx + 1]] = [a[idx + 1], a[idx]]; return a;
    });
  };

  const toggleRole = (id: string) =>
    setSelectedRoles(prev => { const s = new Set(prev); s.has(id) ? s.delete(id) : s.add(id); return s; });

  const save = async () => {
    if (!name.trim()) { setError('Name is required'); return; }
    setSaving(true); setError('');
    try {
      const roleIds = Array.from(selectedRoles);
      if (initial) {
        await updateObjectLayout(slug, initial.id, { name, layout: sections, is_default: isDefault });
        await setLayoutRoles(slug, initial.id, roleIds);
      } else {
        await createObjectLayout(slug, { name, layout: sections, is_default: isDefault, role_ids: roleIds });
      }
      onSave();
    } catch (e) {
      setError(e instanceof Error ? e.message : 'Save failed');
      setSaving(false);
    }
  };

  // Fields already placed in any section (across all sections) — to show in the "add" pickers.
  const placedKeys = new Set(sections.flatMap(s => s.fields.map(f => f.key)));
  const unplacedFields = fieldKeys.filter(f => !placedKeys.has(f.key));
  const fieldLabelMap = Object.fromEntries(fieldKeys.map(f => [f.key, f.label]));

  return (
    <div>
      <h4 className="mb-4 mt-0 text-base font-semibold">{initial ? `Edit layout: ${initial.name}` : 'New layout'}</h4>
      {error && <div className="mb-3 rounded-lg bg-destructive/10 px-3 py-2 text-sm text-destructive">{error}</div>}

      {/* Name + options row */}
      <div className="mb-4 grid grid-cols-[1fr_auto] items-end gap-4">
        <div>
          <Label className="mb-1 block text-[13px]">Layout name *</Label>
          <Input value={name} onChange={e => setName(e.target.value)} placeholder="e.g. Sales view" />
        </div>
        <label className="flex cursor-pointer items-center gap-1.5 pb-2 text-[13px] font-medium">
          <input type="checkbox" checked={isDefault} onChange={e => setIsDefault(e.target.checked)} />
          Default for all roles
        </label>
      </div>

      {/* Role assignment */}
      {roles.length > 0 && (
        <div className="mb-4">
          <Label className="mb-1.5 block text-[13px]">Show this layout to</Label>
          <div className="flex flex-wrap gap-2">
            {roles.map(r => {
              const active = selectedRoles.has(r.id);
              return (
                <button
                  key={r.id}
                  type="button"
                  onClick={() => toggleRole(r.id)}
                  className={`rounded-full border px-3 py-1 text-xs font-medium transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
                    active ? 'border-primary bg-primary/10 text-primary' : 'border-input bg-card text-muted-foreground hover:bg-accent hover:text-accent-foreground'
                  }`}
                >
                  {prettyRole(r.name)}
                </button>
              );
            })}
          </div>
          <p className="mt-1 text-[11px] text-muted-foreground">
            Roles without an assignment fall back to the default layout, then flat field order.
          </p>
        </div>
      )}

      {/* Unplaced fields hint */}
      {unplacedFields.length > 0 && (
        <div className="mb-3 rounded-lg border border-amber-500/40 bg-amber-500/10 px-3 py-2 text-xs text-amber-600 dark:text-amber-400">
          {unplacedFields.length} field{unplacedFields.length > 1 ? 's' : ''} not in any section ({unplacedFields.map(f => f.label).join(', ')}) — they will appear in an "Other" section on the detail page.
        </div>
      )}

      {/* Sections */}
      <div className="mb-3">
        <div className="mb-2 flex items-center justify-between">
          <Label className="text-[13px]">Sections ({sections.length})</Label>
          <Button size="sm" onClick={addSection}><Plus aria-hidden /> Section</Button>
        </div>

        {sections.length === 0 && (
          <div className="rounded-lg border border-dashed border-input px-6 py-6 text-center text-[13px] text-muted-foreground">
            Add sections to group fields. Without sections, all fields render in the "Other" fallback.
          </div>
        )}

        {sections.map((sec, idx) => (
          <SectionEditor
            key={sec.id}
            section={sec}
            fieldLabelMap={fieldLabelMap}
            availableFields={fieldKeys.filter(f => !placedKeys.has(f.key))}
            onUpdate={patch => updateSection(sec.id, patch)}
            onRemove={() => removeSection(sec.id)}
            onMoveUp={idx > 0 ? () => moveSectionUp(idx) : undefined}
            onMoveDown={idx < sections.length - 1 ? () => moveSectionDown(idx) : undefined}
            onAddField={key => addFieldToSection(sec.id, key)}
            onRemoveField={key => removeFieldFromSection(sec.id, key)}
            onSetFieldWidth={(key, w) => setFieldWidth(sec.id, key, w)}
          />
        ))}
      </div>

      <div className="flex gap-2">
        <Button onClick={save} disabled={saving}>
          {saving ? 'Saving…' : initial ? 'Update Layout' : 'Create Layout'}
        </Button>
        <Button variant="secondary" onClick={onCancel}>Cancel</Button>
      </div>
    </div>
  );
}

// ============================================================
// SectionEditor — one section's label, columns, and field list
// ============================================================

function SectionEditor({
  section,
  fieldLabelMap,
  availableFields,
  onUpdate,
  onRemove,
  onMoveUp,
  onMoveDown,
  onAddField,
  onRemoveField,
  onSetFieldWidth,
}: {
  section: LayoutSection;
  fieldLabelMap: Record<string, string>;
  availableFields: FieldEntry[];
  onUpdate: (patch: Partial<LayoutSection>) => void;
  onRemove: () => void;
  onMoveUp?: () => void;
  onMoveDown?: () => void;
  onAddField: (key: string) => void;
  onRemoveField: (key: string) => void;
  onSetFieldWidth: (key: string, width: LayoutField['width']) => void;
}) {
  const [fieldPick, setFieldPick] = useState('');

  return (
    <div className="mb-2 rounded-xl border border-border bg-card p-3">
      {/* Section header controls */}
      <div className="mb-2.5 flex items-center gap-2">
        <Input
          value={section.label}
          onChange={e => onUpdate({ label: e.target.value })}
          placeholder="Section label"
        />
        <Select
          value={section.columns}
          onChange={e => onUpdate({ columns: Number(e.target.value) as 1 | 2 })}
          className="w-auto"
        >
          <option value={1}>1 column</option>
          <option value={2}>2 columns</option>
        </Select>
        <div className="flex gap-0.5">
          <Button variant="outline" size="icon" className="h-8 w-8" onClick={onMoveUp} disabled={!onMoveUp} aria-label="Move section up"><ChevronUp className="h-4 w-4" /></Button>
          <Button variant="outline" size="icon" className="h-8 w-8" onClick={onMoveDown} disabled={!onMoveDown} aria-label="Move section down"><ChevronDown className="h-4 w-4" /></Button>
        </div>
        <button type="button" onClick={onRemove} title="Remove section" className="rounded p-0.5 text-muted-foreground hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-4 w-4" /></button>
      </div>

      {/* Field list */}
      {section.fields.length === 0 ? (
        <p className="m-0 mb-2 text-xs text-muted-foreground">No fields yet — add from the list below.</p>
      ) : (
        <div className="mb-2 overflow-hidden rounded-lg border border-border">
          {section.fields.map((f, fi) => (
            <div key={f.key} className={`flex items-center px-2 py-1.5 text-[13px] ${fi < section.fields.length - 1 ? 'border-b border-border' : ''}`}>
              <span className="flex-1 text-foreground">{fieldLabelMap[f.key] ?? f.key}</span>
              <code className="mr-2 text-[11px] text-muted-foreground">{f.key}</code>
              {section.columns === 2 && (
                <Select
                  value={f.width ?? 'half'}
                  onChange={e => onSetFieldWidth(f.key, e.target.value as LayoutField['width'])}
                  aria-label={`Width for ${fieldLabelMap[f.key] ?? f.key}`}
                  className="mr-2 w-auto"
                >
                  <option value="half">½ col</option>
                  <option value="full">full</option>
                </Select>
              )}
              <button type="button" onClick={() => onRemoveField(f.key)} aria-label={`Remove ${fieldLabelMap[f.key] ?? f.key}`} className="rounded text-muted-foreground hover:text-destructive focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"><X className="h-3.5 w-3.5" /></button>
            </div>
          ))}
        </div>
      )}

      {/* Add field picker */}
      {availableFields.length > 0 && (
        <div className="flex gap-1.5">
          <Select
            value={fieldPick}
            onChange={e => setFieldPick(e.target.value)}
            aria-label="Add a field to this section"
            className="flex-1"
          >
            <option value="">— add a field —</option>
            {availableFields.map(f => <option key={f.key} value={f.key}>{f.label} ({f.key})</option>)}
          </Select>
          <Button size="sm" onClick={() => { if (fieldPick) { onAddField(fieldPick); setFieldPick(''); } }} disabled={!fieldPick}>Add</Button>
        </div>
      )}
    </div>
  );
}
