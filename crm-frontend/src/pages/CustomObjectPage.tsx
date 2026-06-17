import { useParams, useNavigate } from 'react-router-dom';
import { ObjectListView } from '../features/objects';

// CustomObjectPage is now a thin wrapper over the shared, schema-driven renderer.
// Custom objects were migrated first (lowest risk, plan P3) — the same
// ObjectListView renders Contacts/Deals once objects.unified_read is enabled.
export default function CustomObjectPage() {
  const { slug } = useParams<{ slug: string }>();
  const navigate = useNavigate();
  if (!slug) return null;
  return <ObjectListView slug={slug} onNotFound={() => navigate('/settings')} />;
}
