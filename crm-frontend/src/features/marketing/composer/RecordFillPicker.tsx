import React, { useEffect, useState } from 'react';
import { Database, Loader2, Search } from 'lucide-react';
import { Select } from '@/components/ui';
import {
  listRegistryObjects,
  listObjectRecordsUnified,
  type ObjectSummary,
  type UniformRecord,
} from '../../../lib/api';
import type { Block } from './blocks';

const esc = (s: string) =>
  s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');

/** productFromRecord maps a CRM record onto product-card fields by key
 *  heuristics. Only RESOLVED values are returned, so filling never wipes a
 *  field the record can't provide. */
export function productFromRecord(rec: UniformRecord): Partial<Block> {
  const f = rec.fields ?? {};
  const str = (v: unknown): string =>
    typeof v === 'string' ? v.trim() : typeof v === 'number' ? String(v) : '';
  const firstMatch = (re: RegExp): string => {
    for (const [k, v] of Object.entries(f)) {
      if (re.test(k)) {
        const s = str(v);
        if (s) return s;
      }
    }
    return '';
  };

  const out: Partial<Block> = {};
  const title = rec.display?.trim() || firstMatch(/^(name|title|product_name)$/i);
  if (title) out.title = title;
  const desc = firstMatch(/desc|summary|details|about/i);
  if (desc) out.text = `<p>${esc(desc)}</p>`;
  const price = firstMatch(/^(price|amount|cost|value|mrr|fee|rate)$/i);
  if (price) out.price = price;
  const img = firstMatch(/image|img|photo|thumb|picture/i);
  if (/^https?:\/\//i.test(img)) out.src = img;
  const url = firstMatch(/^(url|link|website|page|product_url)$/i);
  if (/^https?:\/\//i.test(url)) out.href = url;
  return out;
}

/** RecordFillPicker fills a product card from any CRM record (system or custom
 *  object) — the stored block stays a self-contained snapshot, so sent emails
 *  never depend on live record data. */
export const RecordFillPicker: React.FC<{ onFill: (patch: Partial<Block>) => void }> = ({ onFill }) => {
  const [objects, setObjects] = useState<ObjectSummary[]>([]);
  const [slug, setSlug] = useState('');
  const [q, setQ] = useState('');
  const [records, setRecords] = useState<UniformRecord[]>([]);
  const [loading, setLoading] = useState(false);
  const [failed, setFailed] = useState(false);

  useEffect(() => {
    let cancelled = false;
    listRegistryObjects()
      .then((rows) => {
        if (cancelled) return;
        setObjects(rows);
        // Prefer a custom object (that's where product catalogs live); fall
        // back to the first object.
        const preferred = rows.find((o) => !o.is_system) ?? rows[0];
        if (preferred) setSlug(preferred.slug);
      })
      .catch(() => { if (!cancelled) setFailed(true); });
    return () => { cancelled = true; };
  }, []);

  useEffect(() => {
    if (!slug) return;
    let cancelled = false;
    setLoading(true);
    const t = setTimeout(() => {
      listObjectRecordsUnified(slug, { q: q.trim() || undefined, limit: 6 })
        .then((page) => { if (!cancelled) { setRecords(page.records); setFailed(false); } })
        .catch(() => { if (!cancelled) { setRecords([]); setFailed(true); } })
        .finally(() => { if (!cancelled) setLoading(false); });
    }, q.trim() === '' ? 0 : 250);
    return () => { cancelled = true; clearTimeout(t); };
  }, [slug, q]);

  return (
    <div className="space-y-1.5 rounded-lg border border-border p-2">
      <p className="flex items-center gap-1 text-[11px] font-medium text-muted-foreground">
        <Database className="h-3 w-3" /> Fill from CRM record
      </p>
      <Select value={slug} onChange={(e) => { setSlug(e.target.value); setQ(''); }} aria-label="Record object type">
        {objects.map((o) => (
          <option key={o.slug} value={o.slug}>{o.label_plural}</option>
        ))}
      </Select>
      <div className="relative">
        <Search className="absolute left-2 top-1/2 h-3.5 w-3.5 -translate-y-1/2 text-muted-foreground" />
        <input
          value={q}
          onChange={(e) => setQ(e.target.value)}
          placeholder="Search records…"
          aria-label="Search records"
          className="w-full rounded-lg border border-border/60 bg-background py-1.5 pl-8 pr-2 text-xs text-foreground focus:border-ring focus:outline-none focus:ring-1 focus:ring-ring"
        />
      </div>
      <div className="max-h-40 overflow-y-auto">
        {loading ? (
          <p className="flex items-center gap-1.5 px-1 py-2 text-[11px] text-muted-foreground"><Loader2 className="h-3 w-3 animate-spin" /> Loading…</p>
        ) : failed ? (
          <p className="px-1 py-2 text-[11px] text-destructive">Couldn’t load records.</p>
        ) : records.length === 0 ? (
          <p className="px-1 py-2 text-[11px] text-muted-foreground">No records found.</p>
        ) : (
          records.map((r) => (
            <button
              key={r.id}
              type="button"
              onClick={() => onFill(productFromRecord(r))}
              className="flex w-full items-center gap-1.5 rounded px-1.5 py-1 text-left text-xs hover:bg-accent hover:text-accent-foreground"
            >
              <span className="min-w-0 flex-1 truncate">{r.display || r.number || r.id.slice(0, 8)}</span>
              {r.number && <span className="shrink-0 text-[10px] text-muted-foreground">{r.number}</span>}
            </button>
          ))
        )}
      </div>
      <p className="text-[11px] text-muted-foreground">Filling copies the record’s data into the card — edits stay yours.</p>
    </div>
  );
};

export default RecordFillPicker;
