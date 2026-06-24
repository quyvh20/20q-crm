import { ObjectListView } from '../features/objects';

// Contacts render through the shared, schema-driven renderer (P7). The
// objects.unified_read flag was removed once the generic renderer reached parity
// with the legacy page: company + tag filters, AI semantic search, and CSV import
// all live in ObjectListView now.
export default function ContactsPage() {
  return <ObjectListView slug="contact" />;
}
