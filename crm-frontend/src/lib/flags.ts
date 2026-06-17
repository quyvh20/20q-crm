// Lightweight client feature flags for the object-model convergence (plan §12).
//
// `objects.unified_read` gates pointing the system-object pages (Contacts/Deals)
// at the shared, schema-driven renderer in features/objects. Default OFF: the
// rich legacy pages remain the fallback, so flipping the flag never changes data
// — only which component renders. A localStorage override lets a dogfooding
// browser toggle it without a rebuild; otherwise the build-time env var decides.

export function isUnifiedObjectReadEnabled(): boolean {
  try {
    const override = localStorage.getItem('objects.unified_read');
    if (override === 'true') return true;
    if (override === 'false') return false;
  } catch {
    // localStorage unavailable (SSR/tests) — fall through to env default.
  }
  return import.meta.env.VITE_OBJECTS_UNIFIED_READ === 'true';
}
