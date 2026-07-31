import React, { useMemo, useState } from 'react';
import { Link } from 'react-router-dom';
import { AlertCircle, ShieldCheck } from 'lucide-react';
import { usePermissions } from '../../lib/auth';
import AccessDeniedPanel from '../../components/common/AccessDeniedPanel';
import Modal from '../../components/common/Modal';
import { Badge, Button, Input, Label, PageHeader, Select, SpinnerBlock } from '@/components/ui';
import { useSegments } from './segmentsQueries';
import { useGrantableBases, useGrantLawfulBasis, usePreviewGrant } from './consentQueries';
import { BASIS_HELP, basisLabel, type ConsentGrantRequest, type GrantCounts } from './consentApi';

/** Marketing lawful basis (R1).
 *
 *  Why this page exists: marketing sends are gated per recipient on a POSITIVE
 *  lawful basis, and nothing else in the product records one. Without it a campaign
 *  passes every pre-send check and then suppresses its entire roster with
 *  "no_lawful_basis" — visible only in the per-recipient status.
 *
 *  This is a legally significant declaration, so the flow is deliberately
 *  two-stage: you always see how many addresses would be touched before anything
 *  is written, and the declared source is recorded alongside the basis. */
export const MarketingConsentPage: React.FC = () => {
  const { can, loaded } = usePermissions();

  if (!loaded) {
    return (
      <div className="mx-auto w-full max-w-3xl">
        <SpinnerBlock label="Loading…" />
      </div>
    );
  }
  if (!can('marketing.manage')) {
    return (
      <div className="mx-auto w-full max-w-3xl">
        <AccessDeniedPanel capability="marketing.manage" what="marketing consent" />
      </div>
    );
  }
  return <ConsentContent />;
};

const ConsentContent: React.FC = () => {
  const { data: bases, isLoading: basesLoading } = useGrantableBases();
  const { data: segments } = useSegments();
  const preview = usePreviewGrant();
  const grant = useGrantLawfulBasis();

  const [basis, setBasis] = useState('');
  const [source, setSource] = useState('');
  const [segmentId, setSegmentId] = useState('');
  const [expiresAt, setExpiresAt] = useState('');
  const [confirmOpen, setConfirmOpen] = useState(false);
  const [result, setResult] = useState<GrantCounts | null>(null);
  const [error, setError] = useState<string | null>(null);

  const selectedBasis = useMemo(
    () => (bases ?? []).find((b) => b.value === basis),
    [bases, basis],
  );
  const needsExpiry = selectedBasis?.requires_casl_expiry ?? false;

  const complete =
    basis !== '' && source.trim() !== '' && segmentId !== '' && (!needsExpiry || expiresAt !== '');

  const request = (): ConsentGrantRequest => ({
    basis,
    source: source.trim(),
    segment_ids: [segmentId],
    // Sent only for the CASL bases; the backend rejects an expiry on a standing
    // basis, so passing one unconditionally would 400 every other grant.
    casl_expires_at: needsExpiry && expiresAt ? new Date(expiresAt).toISOString() : undefined,
  });

  const onPreview = () => {
    setError(null);
    setResult(null);
    preview.mutate(request(), {
      onSuccess: () => setConfirmOpen(true),
      onError: (e) => setError(e.message),
    });
  };

  const onConfirm = () => {
    setError(null);
    grant.mutate(request(), {
      onSuccess: (counts) => {
        setResult(counts);
        setConfirmOpen(false);
      },
      onError: (e) => {
        setError(e.message);
        setConfirmOpen(false);
      },
    });
  };

  return (
    <div className="mx-auto w-full max-w-3xl">
      <PageHeader
        title="Marketing lawful basis"
        description="Record why this workspace may market to a group of its contacts. Marketing sends are refused per recipient without one."
      />

      <div className="mb-4 flex items-start gap-2 rounded-xl border border-border bg-muted/40 p-4 text-sm text-muted-foreground">
        <ShieldCheck aria-hidden className="mt-0.5 h-4 w-4 shrink-0 text-primary" />
        <p>
          Declaring a basis is a statement about a relationship that already exists — it is not a
          substitute for consent. Contacts who have unsubscribed are never affected, and an
          affirmative opt-in can only come from the subscriber, so it cannot be recorded here.
        </p>
      </div>

      {basesLoading ? (
        <SpinnerBlock label="Loading…" />
      ) : (
        <div className="space-y-4 rounded-xl border border-border bg-card p-4">
          <div>
            <Label htmlFor="consent-basis">Lawful basis</Label>
            <Select
              id="consent-basis"
              value={basis}
              onChange={(e) => {
                setBasis(e.target.value);
                setExpiresAt('');
              }}
            >
              <option value="">Select a basis…</option>
              {(bases ?? []).map((b) => (
                <option key={b.value} value={b.value}>{basisLabel(b.value)}</option>
              ))}
            </Select>
            {basis && (
              <p className="mt-1 text-xs text-muted-foreground">{BASIS_HELP[basis] ?? ''}</p>
            )}
          </div>

          {needsExpiry && (
            <div>
              <Label htmlFor="consent-expiry">Consent expires</Label>
              <Input
                id="consent-expiry"
                type="date"
                value={expiresAt}
                onChange={(e) => setExpiresAt(e.target.value)}
              />
              <p className="mt-1 text-xs text-muted-foreground">
                CASL implied consent lapses. After this date these contacts stop being mailable.
              </p>
            </div>
          )}

          <div>
            <Label htmlFor="consent-segment">Apply to audience</Label>
            <Select id="consent-segment" value={segmentId} onChange={(e) => setSegmentId(e.target.value)}>
              <option value="">Select an audience…</option>
              {(segments ?? []).map((s) => (
                <option key={s.id} value={s.id}>{s.name}</option>
              ))}
            </Select>
            <p className="mt-1 text-xs text-muted-foreground">
              Audiences are managed on the{' '}
              <Link className="underline" to="/marketing/segments">Audiences</Link> page.
            </p>
          </div>

          <div>
            <Label htmlFor="consent-source">Declared source</Label>
            <Input
              id="consent-source"
              value={source}
              maxLength={64}
              placeholder="e.g. 2024 customer import"
              onChange={(e) => setSource(e.target.value)}
            />
            <p className="mt-1 text-xs text-muted-foreground">
              Recorded against every address, alongside who made the declaration and when. Keep it
              specific enough to stand up later.
            </p>
          </div>

          {error && (
            <div
              role="alert"
              className="flex items-start gap-2 rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive"
            >
              <AlertCircle aria-hidden className="mt-0.5 h-4 w-4 shrink-0" />
              <span>{error}</span>
            </div>
          )}

          <div className="flex justify-end">
            <Button onClick={onPreview} disabled={!complete || preview.isPending}>
              {preview.isPending ? 'Checking…' : 'Review and record'}
            </Button>
          </div>
        </div>
      )}

      {result && (
        <div className="mt-4 rounded-xl border border-border bg-card p-4">
          <div className="mb-2 flex items-center gap-2">
            <Badge variant="success">Recorded</Badge>
            <span className="text-sm font-semibold text-foreground">
              {result.granted.toLocaleString()} of {result.candidates.toLocaleString()} addresses
            </span>
          </div>
          <ul className="space-y-1 text-sm text-muted-foreground">
            {result.skipped > 0 && (
              <li>{result.skipped.toLocaleString()} skipped — already unsubscribed or cleaned.</li>
            )}
            {result.suppressed > 0 && (
              <li>
                {result.suppressed.toLocaleString()} carry a suppression entry and still will not be
                mailed. <Link className="underline" to="/marketing/suppressions">Suppression list</Link>
              </li>
            )}
          </ul>
        </div>
      )}

      <Modal
        open={confirmOpen}
        onClose={() => setConfirmOpen(false)}
        title="Record this lawful basis?"
        size="md"
      >
        <div className="space-y-4">
          <p className="text-sm text-muted-foreground">
            You are declaring <span className="font-medium text-foreground">{basisLabel(basis)}</span>{' '}
            for the addresses below, sourced from{' '}
            <span className="font-medium text-foreground">{source.trim()}</span>.
          </p>

          <dl className="space-y-1 rounded-lg border border-border p-3 text-sm">
            <div className="flex justify-between">
              <dt className="text-muted-foreground">Addresses in this audience</dt>
              <dd className="font-medium tabular-nums text-foreground">
                {(preview.data?.candidates ?? 0).toLocaleString()}
              </dd>
            </div>
            {(preview.data?.skipped ?? 0) > 0 && (
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Already opted out (unaffected)</dt>
                <dd className="font-medium tabular-nums text-foreground">
                  {(preview.data?.skipped ?? 0).toLocaleString()}
                </dd>
              </div>
            )}
            {(preview.data?.suppressed ?? 0) > 0 && (
              <div className="flex justify-between">
                <dt className="text-muted-foreground">Suppressed (will not be mailed)</dt>
                <dd className="font-medium tabular-nums text-foreground">
                  {(preview.data?.suppressed ?? 0).toLocaleString()}
                </dd>
              </div>
            )}
          </dl>

          <div className="flex justify-end gap-2">
            <Button variant="outline" onClick={() => setConfirmOpen(false)}>Cancel</Button>
            <Button onClick={onConfirm} disabled={grant.isPending}>
              {grant.isPending ? 'Recording…' : 'Record basis'}
            </Button>
          </div>
        </div>
      </Modal>
    </div>
  );
};

export default MarketingConsentPage;
