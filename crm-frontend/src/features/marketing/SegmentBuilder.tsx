import { useEffect, useMemo, useRef, useState } from 'react';
import { X, Tag as TagIcon } from 'lucide-react';
import { Button } from '@/components/ui';
import type { Tag } from '../../lib/api';
import type { SegmentFilter, SegmentFieldDescriptor } from './segmentsApi';

// The M5 segment builder: a recursive AND/OR/NOT tree over whitelisted contact
// fields, custom fields, and tag membership. It edits an internal normalized model
// (Group/Leaf) and emits the backend domain.SegmentFilter AST. NOT is a per-group
// "exclude" wrapper (serialized as {op:'not', rules:[inner]}), which the backend
// reads as NOT(inner) — the honest, useful negation ("exclude anyone matching
// this group"); leaf-level negation also lives on operators (not-equals) and tags
// (does-not-have).

// ── operator catalog (a subset of the backend reportOperatorsByType the builder
// offers; every option here is accepted by the compiler) ────────────────────────

interface OpDef { value: string; label: string; needsValue: boolean }

const TEXT_OPS: OpDef[] = [
  { value: 'eq', label: 'equals', needsValue: true },
  { value: 'neq', label: 'not equals', needsValue: true },
  { value: 'contains', label: 'contains', needsValue: true },
  { value: 'not_contains', label: "doesn't contain", needsValue: true },
  { value: 'starts_with', label: 'starts with', needsValue: true },
  { value: 'ends_with', label: 'ends with', needsValue: true },
  { value: 'is_empty', label: 'is empty', needsValue: false },
  { value: 'is_not_empty', label: 'is not empty', needsValue: false },
];

const OPERATORS: Record<string, OpDef[]> = {
  text: TEXT_OPS,
  url: TEXT_OPS,
  select: [
    { value: 'eq', label: 'is', needsValue: true },
    { value: 'neq', label: 'is not', needsValue: true },
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
  number: [
    { value: 'eq', label: '=', needsValue: true },
    { value: 'neq', label: '≠', needsValue: true },
    { value: 'gt', label: '>', needsValue: true },
    { value: 'gte', label: '≥', needsValue: true },
    { value: 'lt', label: '<', needsValue: true },
    { value: 'lte', label: '≤', needsValue: true },
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
  date: [
    { value: 'gte', label: 'on or after', needsValue: true },
    { value: 'gt', label: 'after', needsValue: true },
    { value: 'lte', label: 'on or before', needsValue: true },
    { value: 'lt', label: 'before', needsValue: true },
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
  boolean: [{ value: 'eq', label: 'is', needsValue: true }],
  relation: [
    { value: 'is_empty', label: 'is empty', needsValue: false },
    { value: 'is_not_empty', label: 'is not empty', needsValue: false },
  ],
};

function opsFor(type: string): OpDef[] { return OPERATORS[type] ?? TEXT_OPS; }

function defaultValueFor(f: SegmentFieldDescriptor): unknown {
  switch (f.type) {
    case 'boolean': return true;
    case 'number': return 0;
    case 'select': return f.options?.[0] ?? '';
    default: return '';
  }
}

const MAX_DEPTH = 4; // backend caps at 5 — leave headroom

// ── internal normalized model ────────────────────────────────────────────────

type FieldLeaf = { kind: 'field'; field: string; operator: string; value: unknown };
type TagLeaf = { kind: 'tag'; tagId: string; not: boolean };
type Group = { kind: 'group'; join: 'and' | 'or'; negate: boolean; children: Node[] };
type Node = FieldLeaf | TagLeaf | Group;

function fromAST(f: SegmentFilter | undefined | null): Node {
  if (!f || Object.keys(f).length === 0) return { kind: 'group', join: 'and', negate: false, children: [] };
  if (f.tag_id) return { kind: 'tag', tagId: f.tag_id, not: !!f.tag_not };
  if (f.field) return { kind: 'field', field: f.field, operator: f.operator ?? 'eq', value: f.value };
  const op = (f.op ?? 'and').toLowerCase();
  if (op === 'not') {
    const inner = f.rules?.[0];
    if (inner && !inner.field && !inner.tag_id) {
      const g = fromAST(inner) as Group;
      return { ...g, negate: true };
    }
    // NOT wrapping leaves directly.
    return { kind: 'group', join: 'and', negate: true, children: (f.rules ?? []).map(fromAST) };
  }
  return { kind: 'group', join: op === 'or' ? 'or' : 'and', negate: false, children: (f.rules ?? []).map(fromAST) };
}

function toAST(n: Node): SegmentFilter {
  if (n.kind === 'field') return { field: n.field, operator: n.operator, value: n.value };
  if (n.kind === 'tag') return n.not ? { tag_id: n.tagId, tag_not: true } : { tag_id: n.tagId };
  const inner: SegmentFilter = { op: n.join, rules: n.children.map(toAST) };
  return n.negate ? { op: 'not', rules: [inner] } : inner;
}

/** nodeRunnable reports whether every leaf is complete enough that the backend
 *  won't 400 the dry-run (empty field/operator, valueless operator missing its
 *  value, tag leaf with no tag). typeOf resolves a field key to its type. */
function nodeRunnable(n: Node, typeOf: (key: string) => string): boolean {
  if (n.kind === 'field') {
    if (!n.field || !n.operator) return false;
    const od = opsFor(typeOf(n.field)).find((o) => o.value === n.operator);
    if (od?.needsValue && (n.value === undefined || n.value === '')) return false;
    return true;
  }
  if (n.kind === 'tag') return !!n.tagId;
  return n.children.every((c) => nodeRunnable(c, typeOf));
}

// ── component ─────────────────────────────────────────────────────────────────

interface Props {
  fields: SegmentFieldDescriptor[];
  tags: Tag[];
  /** The initial AST (from the loaded segment). Seeded once. */
  initial: SegmentFilter | undefined;
  onChange: (ast: SegmentFilter, runnable: boolean) => void;
}

export default function SegmentBuilder({ fields, tags, initial, onChange }: Props) {
  const [root, setRoot] = useState<Group>(() => {
    const n = fromAST(initial);
    return n.kind === 'group' ? n : { kind: 'group', join: 'and', negate: false, children: [n] };
  });

  const fieldByKey = useMemo(() => {
    const m = new Map<string, SegmentFieldDescriptor>();
    for (const f of fields) m.set(f.key, f);
    return m;
  }, [fields]);
  const typeOf = (k: string) => fieldByKey.get(k)?.type ?? 'text';

  // Emit after each edit. Kept in a ref-guarded effect so the parent gets the AST +
  // a "runnable" flag it uses to gate the live count. typeOf is read through a ref
  // so the effect depends only on the tree, not on catalog identity.
  const emitRef = useRef({ onChange, typeOf });
  // The ref refresh MUST stay in its own dependency-less effect declared ABOVE the
  // emit effect: writing a ref during render is not concurrent-safe (a render that
  // React throws away would still have mutated it). Dep-less effects re-run after
  // every commit and effects fire in declaration order, so by the time the emit
  // effect below runs, emitRef.current already holds this render's callbacks.
  useEffect(() => {
    emitRef.current = { onChange, typeOf };
  });
  useEffect(() => {
    const { onChange: cb, typeOf: t } = emitRef.current;
    cb(toAST(root), nodeRunnable(root, t));
  }, [root]);

  return (
    <GroupEditor
      group={root}
      fields={fields}
      tags={tags}
      depth={0}
      onChange={setRoot}
    />
  );
}

function GroupEditor({
  group, fields, tags, depth, onChange, onRemove,
}: {
  group: Group;
  fields: SegmentFieldDescriptor[];
  tags: Tag[];
  depth: number;
  onChange: (g: Group) => void;
  onRemove?: () => void;
}) {
  const setChild = (i: number, child: Node) =>
    onChange({ ...group, children: group.children.map((c, j) => (j === i ? child : c)) });
  const removeChild = (i: number) =>
    onChange({ ...group, children: group.children.filter((_, j) => j !== i) });

  const addField = () => {
    const f = fields[0];
    if (!f) return;
    const op = opsFor(f.type)[0];
    onChange({ ...group, children: [...group.children, { kind: 'field', field: f.key, operator: op.value, value: defaultValueFor(f) }] });
  };
  const addTag = () => {
    const t = tags[0];
    onChange({ ...group, children: [...group.children, { kind: 'tag', tagId: t?.id ?? '', not: false }] });
  };
  const addGroup = () =>
    onChange({ ...group, children: [...group.children, { kind: 'group', join: 'and', negate: false, children: [] }] });

  return (
    <div className={depth === 0 ? '' : 'rounded-lg border border-border bg-muted/30 p-3'}>
      <div className="mb-2 flex flex-wrap items-center gap-2 text-xs">
        <span className="text-muted-foreground">Match</span>
        <select
          aria-label="Match type"
          value={group.join}
          onChange={(e) => onChange({ ...group, join: e.target.value === 'or' ? 'or' : 'and' })}
          className="rounded-md border bg-background px-2 py-1 text-xs font-medium"
        >
          <option value="and">ALL of the following (AND)</option>
          <option value="or">ANY of the following (OR)</option>
        </select>
        <label className="flex items-center gap-1 text-muted-foreground">
          <input
            type="checkbox"
            checked={group.negate}
            onChange={(e) => onChange({ ...group, negate: e.target.checked })}
            aria-label="Exclude (NOT)"
          />
          Exclude matches (NOT)
        </label>
        {onRemove && (
          <Button type="button" variant="ghost" size="icon" aria-label="Remove group"
            onClick={onRemove} className="ml-auto h-6 w-6 text-muted-foreground hover:text-foreground">
            <X aria-hidden />
          </Button>
        )}
      </div>

      <div className="space-y-2 border-l-2 border-border pl-3">
        {group.children.length === 0 && (
          <p className="text-xs italic text-muted-foreground">No conditions yet — this matches everyone.</p>
        )}
        {group.children.map((child, i) => {
          if (child.kind === 'group') {
            return (
              <GroupEditor key={i} group={child} fields={fields} tags={tags} depth={depth + 1}
                onChange={(g) => setChild(i, g)} onRemove={() => removeChild(i)} />
            );
          }
          if (child.kind === 'tag') {
            return <TagRow key={i} leaf={child} tags={tags} onChange={(l) => setChild(i, l)} onRemove={() => removeChild(i)} />;
          }
          return <FieldRow key={i} leaf={child} fields={fields} onChange={(l) => setChild(i, l)} onRemove={() => removeChild(i)} />;
        })}

        <div className="flex flex-wrap gap-2 pt-1">
          <AddBtn onClick={addField} label="+ Condition" />
          {tags.length > 0 && <AddBtn onClick={addTag} label="+ Tag" />}
          {depth < MAX_DEPTH && <AddBtn onClick={addGroup} label="+ Group" />}
        </div>
      </div>
    </div>
  );
}

function AddBtn({ onClick, label }: { onClick: () => void; label: string }) {
  return (
    <button type="button" onClick={onClick}
      className="rounded-md border border-dashed px-3 py-1 text-xs text-muted-foreground hover:bg-accent hover:text-foreground">
      {label}
    </button>
  );
}

function FieldRow({
  leaf, fields, onChange, onRemove,
}: {
  leaf: FieldLeaf;
  fields: SegmentFieldDescriptor[];
  onChange: (l: FieldLeaf) => void;
  onRemove: () => void;
}) {
  const field = fields.find((f) => f.key === leaf.field) ?? fields[0];
  const ops = opsFor(field?.type ?? 'text');
  const opDef = ops.find((o) => o.value === leaf.operator) ?? ops[0];
  return (
    <div className="flex flex-wrap items-center gap-2">
      <select
        aria-label="Field"
        value={leaf.field}
        onChange={(e) => {
          const f = fields.find((x) => x.key === e.target.value);
          if (!f) return;
          const op = opsFor(f.type)[0];
          onChange({ kind: 'field', field: f.key, operator: op.value, value: defaultValueFor(f) });
        }}
        className="w-44 rounded-md border bg-background px-2 py-1.5 text-sm"
      >
        {fields.map((f) => <option key={f.key} value={f.key}>{f.label}</option>)}
      </select>

      <select
        aria-label="Operator"
        value={leaf.operator}
        onChange={(e) => {
          const nextOp = ops.find((o) => o.value === e.target.value);
          const value = nextOp?.needsValue ? (leaf.value ?? (field ? defaultValueFor(field) : '')) : undefined;
          onChange({ ...leaf, operator: e.target.value, value });
        }}
        className="w-36 rounded-md border bg-background px-2 py-1.5 text-sm"
      >
        {ops.map((o) => <option key={o.value} value={o.value}>{o.label}</option>)}
      </select>

      {opDef.needsValue && field && (
        <ValueInput field={field} value={leaf.value} onChange={(v) => onChange({ ...leaf, value: v })} />
      )}

      <Button type="button" variant="ghost" size="icon" aria-label="Remove condition"
        onClick={onRemove} className="h-7 w-7 shrink-0 text-muted-foreground hover:text-foreground">
        <X aria-hidden />
      </Button>
    </div>
  );
}

function TagRow({
  leaf, tags, onChange, onRemove,
}: {
  leaf: TagLeaf;
  tags: Tag[];
  onChange: (l: TagLeaf) => void;
  onRemove: () => void;
}) {
  return (
    <div className="flex flex-wrap items-center gap-2">
      <TagIcon aria-hidden className="h-4 w-4 shrink-0 text-muted-foreground" />
      <select
        aria-label="Tag condition"
        value={leaf.not ? 'without' : 'with'}
        onChange={(e) => onChange({ ...leaf, not: e.target.value === 'without' })}
        className="w-36 rounded-md border bg-background px-2 py-1.5 text-sm"
      >
        <option value="with">has tag</option>
        <option value="without">does not have</option>
      </select>
      <select
        aria-label="Tag"
        value={leaf.tagId}
        onChange={(e) => onChange({ ...leaf, tagId: e.target.value })}
        className="w-48 rounded-md border bg-background px-2 py-1.5 text-sm"
      >
        {tags.map((t) => <option key={t.id} value={t.id}>{t.name}</option>)}
      </select>
      <Button type="button" variant="ghost" size="icon" aria-label="Remove tag condition"
        onClick={onRemove} className="h-7 w-7 shrink-0 text-muted-foreground hover:text-foreground">
        <X aria-hidden />
      </Button>
    </div>
  );
}

function ValueInput({ field, value, onChange }: {
  field: SegmentFieldDescriptor;
  value: unknown;
  onChange: (v: unknown) => void;
}) {
  const cls = 'w-44 rounded-md border bg-background px-2 py-1.5 text-sm';
  switch (field.type) {
    case 'boolean':
      return (
        <select aria-label="Value" className={cls} value={value === false ? 'false' : 'true'}
          onChange={(e) => onChange(e.target.value === 'true')}>
          <option value="true">Yes</option>
          <option value="false">No</option>
        </select>
      );
    case 'number':
      return (
        <input aria-label="Value" type="number" className={cls}
          value={typeof value === 'number' ? value : ''}
          onChange={(e) => onChange(e.target.value === '' ? 0 : Number(e.target.value))} />
      );
    case 'date':
      return (
        <input aria-label="Value" type="date" className={cls}
          value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)} />
      );
    case 'select':
      return (
        <select aria-label="Value" className={cls} value={typeof value === 'string' ? value : ''}
          onChange={(e) => onChange(e.target.value)}>
          {(field.options ?? []).map((o) => <option key={o} value={o}>{o}</option>)}
        </select>
      );
    default:
      return (
        <input aria-label="Value" type="text" className={cls}
          value={typeof value === 'string' ? value : String(value ?? '')}
          onChange={(e) => onChange(e.target.value)} />
      );
  }
}
