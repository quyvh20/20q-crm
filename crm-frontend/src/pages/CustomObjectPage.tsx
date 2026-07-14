import { useCallback, useState } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { ObjectListView } from '../features/objects';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import type { ObjectSchema } from '../lib/api';

// CustomObjectPage is now a thin wrapper over the shared, schema-driven renderer.
// Custom objects were migrated first (lowest risk, plan P3) — the same
// ObjectListView renders Contacts/Deals once objects.unified_read is enabled.
export default function CustomObjectPage() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();

  // The route knows only a slug ("invoices"); the human label ("Invoices") lives
  // in the schema, which ObjectListView already fetches — so it hands it up here
  // rather than us paying for the same document twice (U7.2). Keyed by slug so a
  // click through to a different object doesn't briefly show the old object's
  // name in the tab.
  const [loaded, setLoaded] = useState<{ slug: string; label: string } | null>(null);
  useDocumentTitle(loaded && loaded.slug === slug ? loaded.label : null);

  // Both callbacks are effect dependencies inside ObjectListView — an inline
  // arrow would be a new function every render and refetch the schema each time.
  const handleNotFound = useCallback(() => navigate('/settings'), [navigate]);
  const handleSchemaLoaded = useCallback(
    (schema: ObjectSchema) => setLoaded({ slug: schema.slug, label: schema.label_plural }),
    [],
  );

  if (!slug) return null;
  return <ObjectListView slug={slug} onNotFound={handleNotFound} onSchemaLoaded={handleSchemaLoaded} />;
}
