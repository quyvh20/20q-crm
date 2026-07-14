import { useState, useEffect } from 'react';
import {
  type ObjectSchema,
  type UniformRecord,
  type LayoutSection,
  type ObjectFieldDescriptor,
  type RelatedList,
  type Tag,
  getObjectRecordUnified,
  getStages,
  getWorkspaceMembers,
  getUsers,
} from '../../lib/api';
import { formatFieldValue } from './fieldHelpers';
import RecordTags from './RecordTags';
import RelatedLists from './RelatedLists';

interface ObjectDetailViewProps {
  schema: ObjectSchema;
  record: UniformRecord;
  // Pre-fetched data from the parent page so child components render instantly
  // instead of starting their own fetch waterfall after mount.
  prefetchedRelatedLists?: RelatedList[] | null;
  prefetchedTags?: Tag[] | null;
  prefetchedAllTags?: Tag[] | null;
  // Server-resolved display strings from the composite record-page endpoint.
  // When provided, the per-relation/mirror fetches below are skipped entirely
  // (the stage pseudo-relation stays a client lookup — it has no target slug).
  prefetchedRelationLabels?: Record<string, string>;
  prefetchedMirrorValues?: Record<string, string>;
}

// FieldRow is the shared single-field renderer, used by every section so the
// display is always identical.
function FieldRow({
  field,
  value,
  relationLabel,
  mirrorValue,
}: {
  field: ObjectFieldDescriptor;
  value: unknown;
  relationLabel?: string;
  mirrorValue?: string;
}) {
  return (
    <div style={{ marginBottom: 16 }}>
      <div style={{ fontSize: 12, fontWeight: 600, color: '#64748b', textTransform: 'uppercase', marginBottom: 2 }}>
        {field.label}
      </div>
      <div style={{ fontSize: 14, color: '#0f172a' }}>
        {/* A mirror stores no value of its own; it shows the resolved value pulled
            from the linked record (empty renders as an em dash). */}
        {field.type === 'mirror'
          ? (mirrorValue ? mirrorValue : <span style={{ color: '#94a3b8' }}>—</span>)
          : formatFieldValue(field, value, relationLabel)}
      </div>
    </div>
  );
}

// SectionPanel renders one LayoutSection with its fields in a 1- or 2-column grid.
function SectionPanel({
  section,
  schema,
  record,
  relationLabels,
  mirrorValues,
}: {
  section: LayoutSection;
  schema: ObjectSchema;
  record: UniformRecord;
  relationLabels: Record<string, string>;
  mirrorValues: Record<string, string>;
}) {
  const fieldMap = Object.fromEntries(schema.fields.map((f) => [f.key, f]));

  // Only fields that actually exist in the schema render. A section whose fields
  // are all stale (renamed/removed) or stripped by FLS would otherwise show an
  // empty heading with no body — so we drop the whole section in that case.
  const visibleFields = section.fields.filter((slot) => fieldMap[slot.key]);
  if (visibleFields.length === 0) return null;

  return (
    <div style={{ marginBottom: 24 }}>
      {section.label && (
        <div style={{
          fontSize: 11,
          fontWeight: 700,
          color: '#94a3b8',
          textTransform: 'uppercase',
          letterSpacing: '0.08em',
          borderBottom: '1px solid #e2e8f0',
          paddingBottom: 6,
          marginBottom: 14,
        }}>
          {section.label}
        </div>
      )}
      <div
        style={
          section.columns === 2
            ? { display: 'grid', gridTemplateColumns: '1fr 1fr', columnGap: 24 }
            : undefined
        }
      >
        {visibleFields.map((slot) => {
          const field = fieldMap[slot.key];
          const span = section.columns === 2 && slot.width === 'full' ? { gridColumn: '1 / -1' } : undefined;
          return (
            <div key={slot.key} style={span}>
              <FieldRow
                field={field}
                value={record.fields[slot.key]}
                relationLabel={relationLabels[slot.key]}
                mirrorValue={mirrorValues[slot.key]}
              />
            </div>
          );
        })}
      </div>
    </div>
  );
}

// buildDefaultSections synthesizes a clean, business-ready layout when no admin
// layout is configured: every field in a single 2-column "Details" section, in
// schema order. So whenever an object has fields, its record page is structured
// rather than a flat dump (an object with zero visible fields renders just the
// relationships panel). Presentation only; FLS already stripped any hidden fields
// from schema.fields server-side, so this can never widen access.
export function buildDefaultSections(schema: ObjectSchema): LayoutSection[] {
  if (schema.fields.length === 0) return [];
  return [
    {
      id: '__details__',
      label: 'Details',
      columns: 2,
      fields: schema.fields.map((f) => ({ key: f.key })),
    },
  ];
}

// ObjectDetailView is the read-only record body for every object, rendered from
// the same schema as the list and form. It is the body of the full record page
// (ObjectRecordPage) — page chrome (title, back, edit/delete) lives there.
//
// Layout resolution:
//  - schema.layout present (admin/role-resolved, P8) → render those sections, and
//    collect any field absent from every section into a trailing "Other" section
//    so nothing is ever lost.
//  - schema.layout absent/empty → synthesize a default 2-column "Details" section
//    (buildDefaultSections) so the page is never blank.
// Layout controls visual priority, not visibility (FLS controls that).
export default function ObjectDetailView({
  schema,
  record,
  prefetchedRelatedLists,
  prefetchedTags,
  prefetchedAllTags,
  prefetchedRelationLabels,
  prefetchedMirrorValues,
}: ObjectDetailViewProps) {
  const [relationLabels, setRelationLabels] = useState<Record<string, string>>(prefetchedRelationLabels ?? {});
  const [mirrorValues, setMirrorValues] = useState<Record<string, string>>(prefetchedMirrorValues ?? {});
  const [ownerName, setOwnerName] = useState('');
  // Managed mode: the composite endpoint already resolved these server-side,
  // so the per-target fetches below are skipped.
  const managedLabels = prefetchedRelationLabels !== undefined;
  const managedMirrors = prefetchedMirrorValues !== undefined;

  // Keep state in sync when the parent reloads with a new payload.
  useEffect(() => {
    if (prefetchedRelationLabels) setRelationLabels(prefetchedRelationLabels);
  }, [prefetchedRelationLabels]);
  useEffect(() => {
    if (prefetchedMirrorValues) setMirrorValues(prefetchedMirrorValues);
  }, [prefetchedMirrorValues]);

  // Owner (U6) is not a registry field, so it has no FieldRow of its own — it is
  // read off the record (the server mirrors it into fields too) and resolved to a
  // name. Members first; /api/users is the fallback for a role that can't list
  // members. Unresolvable ⇒ we still say SOMEONE owns it, never "Unassigned".
  const ownerId = schema.has_owner
    ? ((record.owner_user_id ?? (record.fields.owner_user_id as string | null | undefined)) || '')
    : '';
  useEffect(() => {
    if (!ownerId) {
      setOwnerName('');
      return;
    }
    let cancelled = false;
    (async () => {
      try {
        const members = await getWorkspaceMembers();
        const hit = members.find((m) => m.user_id === ownerId);
        if (hit) {
          if (!cancelled) setOwnerName(hit.full_name || `${hit.first_name} ${hit.last_name}`.trim() || hit.email);
          return;
        }
      } catch {
        // fall through to the users list
      }
      try {
        const users = await getUsers();
        const hit = users.find((u) => u.id === ownerId);
        if (!cancelled) setOwnerName(hit ? `${hit.first_name} ${hit.last_name}`.trim() || hit.email : '');
      } catch {
        if (!cancelled) setOwnerName('');
      }
    })();
    return () => {
      cancelled = true;
    };
  }, [ownerId]);

  // Resolve mirror fields: follow each mirror's via relation to the linked record
  // and read its source field. Best-effort per field; an unreadable link shows "—".
  useEffect(() => {
    if (managedMirrors) return;
    let cancelled = false;
    const byKey = Object.fromEntries(schema.fields.map((f) => [f.key, f]));
    const mirrors = schema.fields.filter((f) => f.type === 'mirror' && f.via_field && f.source_field);
    if (mirrors.length === 0) {
      setMirrorValues({});
      return;
    }
    Promise.all(
      mirrors.map(async (m) => {
        const via = byKey[m.via_field!];
        const linkedId = via ? record.fields[m.via_field!] : undefined;
        if (!via || !via.target_slug || !linkedId) return [m.key, ''] as const;
        try {
          const target = await getObjectRecordUnified(via.target_slug, String(linkedId));
          const raw = target.fields[m.source_field!];
          return [m.key, raw == null ? '' : String(raw)] as const;
        } catch {
          return [m.key, ''] as const;
        }
      }),
    ).then((pairs) => {
      if (cancelled) return;
      const map: Record<string, string> = {};
      for (const [k, v] of pairs) map[k] = v;
      setMirrorValues(map);
    });
    return () => {
      cancelled = true;
    };
  }, [schema, record, managedMirrors]);

  useEffect(() => {
    let cancelled = false;
    // In managed mode the server resolved every typed relation; only the stage
    // pseudo-relation (empty target slug) still needs the client-side lookup
    // against the pipeline-stage list.
    const relations = managedLabels
      ? []
      : schema.fields.filter((f) => f.type === 'relation' && f.target_slug && record.fields[f.key]);
    const stageField = schema.fields.find(
      (f) => f.key === 'stage' && f.type === 'relation' && !f.target_slug && record.fields[f.key],
    );
    if (relations.length === 0 && !stageField) {
      if (!managedLabels) setRelationLabels({});
      return;
    }
    Promise.all([
      ...relations.map(async (f) => {
        try {
          const target = await getObjectRecordUnified(f.target_slug!, String(record.fields[f.key]));
          return [f.key, target.display] as const;
        } catch {
          return [f.key, ''] as const;
        }
      }),
      ...(stageField
        ? [
            getStages()
              .then((stages) => {
                const match = stages.find((s) => s.id === String(record.fields[stageField.key]));
                return [stageField.key, match?.name ?? ''] as const;
              })
              .catch(() => [stageField.key, ''] as const),
          ]
        : []),
    ]).then((pairs) => {
      if (cancelled) return;
      // Seed from the server-resolved labels (managed mode) so the stage label
      // merges in rather than clobbering them.
      const map: Record<string, string> = managedLabels ? { ...prefetchedRelationLabels } : {};
      for (const [k, v] of pairs) if (v) map[k] = v;
      setRelationLabels(map);
    });
    return () => {
      cancelled = true;
    };
  }, [schema, record, managedLabels, prefetchedRelationLabels]);

  // A configured (admin/role) layout takes precedence; otherwise the built-in
  // default keeps the page structured and never blank.
  const configured = schema.layout && schema.layout.length > 0 ? schema.layout : null;
  const sections = configured ?? buildDefaultSections(schema);

  // "Other" only applies to a configured layout — the default already places
  // every field, so it can never have leftovers.
  let otherSection: LayoutSection | null = null;
  if (configured) {
    const inLayout = new Set(configured.flatMap((s) => s.fields.map((f) => f.key)));
    const missing = schema.fields.filter((f) => !inLayout.has(f.key));
    if (missing.length > 0) {
      otherSection = {
        id: '__other__',
        label: 'Other',
        columns: 1,
        fields: missing.map((f) => ({ key: f.key })),
      };
    }
  }

  return (
    <div>
      {schema.has_owner && (
        <div className="mb-6">
          <div className="mb-0.5 text-xs font-semibold uppercase tracking-wide text-muted-foreground">Owner</div>
          <div className="text-sm text-foreground">
            {ownerId
              ? (ownerName || <span className="text-muted-foreground">A member of this workspace</span>)
              : <span className="text-muted-foreground">Unassigned</span>}
          </div>
        </div>
      )}

      {sections.map((section) => (
        <SectionPanel
          // Namespaced so a configured section can never collide with the
          // synthesized "Other" key below, whatever id an admin picks.
          key={`sec:${section.id}`}
          section={section}
          schema={schema}
          record={record}
          relationLabels={relationLabels}
          mirrorValues={mirrorValues}
        />
      ))}
      {otherSection && (
        <SectionPanel
          key="synth:other"
          section={otherSection}
          schema={schema}
          record={record}
          relationLabels={relationLabels}
          mirrorValues={mirrorValues}
        />
      )}

      {/* Tags (uniform across every object). The former free-text "link any
          record" panel was replaced by schema-driven related lists below. */}
      <RecordTags slug={schema.slug} recordId={record.id} prefetchedTags={prefetchedTags} prefetchedAllTags={prefetchedAllTags} />

      {/* Schema-driven reverse related lists (e.g. a Contact's Deals), derived
          from relation fields that point back at this record. */}
      <RelatedLists slug={schema.slug} recordId={record.id} prefetchedLists={prefetchedRelatedLists} />
    </div>
  );
}
