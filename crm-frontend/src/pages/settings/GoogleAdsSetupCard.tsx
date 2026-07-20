import { useState } from 'react';
import { Check, Copy, RefreshCw } from 'lucide-react';
import { Button } from '@/components/ui';
import SecretReveal from '../../components/settings/SecretReveal';
import { useRotateGoogleKey } from '../../features/integrations/queries';
import type { LeadSource } from '../../features/integrations/types';

// The google_ads setup panel: the two values an advertiser pastes into Google's
// lead-form editor, and the instructions for where they go. This card is the
// entire integration surface — there is no OAuth, no app, no connection flow. If
// the copy here is wrong, the advertiser's leads go nowhere and nobody errors.

interface Props {
  source: LeadSource;
}

export default function GoogleAdsSetupCard({ source }: Props) {
  const rotate = useRotateGoogleKey();
  const [copied, setCopied] = useState(false);
  // The rotated key lives in component state only — the SecretReveal contract.
  const [newKey, setNewKey] = useState<string | null>(null);
  const [error, setError] = useState('');

  // window.location.origin is the established pattern for the app's public URL
  // (the L1 setup recipe uses it): prod serves /api through a same-origin proxy.
  const webhookURL = `${window.location.origin}/api/capture/google-ads/${source.public_token ?? ''}`;

  const copyURL = async () => {
    try {
      await navigator.clipboard.writeText(webhookURL);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setError('Could not copy — select the URL and copy it manually.');
    }
  };

  const handleRotate = async () => {
    setError('');
    try {
      const { google_key } = await rotate.mutateAsync(source.id);
      if (google_key) setNewKey(google_key);
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to rotate the key');
    }
  };

  return (
    <div className="rounded-xl border border-border p-4 space-y-4">
      <div>
        <h3 className="text-sm font-medium text-foreground">Google Ads webhook</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Paste these two values into your lead form asset and Google delivers each lead here as
          it is submitted.
        </p>
      </div>

      {newKey && (
        <SecretReveal
          title="Your new webhook key"
          description="This is the only time you'll see it. Paste it into Google's form editor now — leads sent with the old key are being rejected already, and Google does not retry them. If any arrive before you finish, resend them later through the batch endpoint."
          value={newKey}
          onDone={() => setNewKey(null)}
        />
      )}

      <div className="space-y-1.5">
        <span className="text-xs text-muted-foreground">Webhook URL</span>
        <div className="flex items-center gap-2">
          <code className="flex-1 rounded-lg border border-border bg-muted/40 px-3 py-2 text-xs break-all">
            {webhookURL}
          </code>
          <Button size="sm" variant="outline" onClick={() => void copyURL()}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
      </div>

      <div className="space-y-1.5">
        <span className="text-xs text-muted-foreground">Webhook key</span>
        <div className="flex items-center gap-2">
          <p className="flex-1 text-xs text-muted-foreground">
            Shown once when this source was created. Lost it? Rotate to get a new one — then
            update Google's editor immediately, because leads sent with the old key are rejected
            and Google does not retry them.
          </p>
          <Button size="sm" variant="outline" onClick={() => void handleRotate()} disabled={rotate.isPending}>
            <RefreshCw className="h-3.5 w-3.5" />
            {rotate.isPending ? 'Rotating…' : 'Rotate key'}
          </Button>
        </div>
      </div>

      <ol className="list-decimal space-y-1 pl-5 text-xs text-muted-foreground border-t border-border pt-3">
        <li>In Google Ads, open your lead form asset and choose “Export leads from Google Ads”.</li>
        <li>Under “Other data integration options”, pick “Webhook integration”.</li>
        <li>Paste the URL and the key, then click “Send test data”.</li>
        <li>
          The test appears in the delivery log below within a few seconds, badged{' '}
          <code>test</code> — it creates a flagged test contact, never a real one, and never
          counts in your pipeline.
        </li>
      </ol>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}
    </div>
  );
}
