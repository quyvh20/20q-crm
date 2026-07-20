import { useEffect, useMemo, useState } from 'react';
import { AlertTriangle, Check, Copy, Plus, X } from 'lucide-react';
import { Badge, Button, Input, Select } from '@/components/ui';
import { useUpdateSource } from '../../features/integrations/queries';
import {
  FORM_FIELD_TYPES,
  type FormField,
  type LeadSource,
} from '../../features/integrations/types';

// The web-to-lead setup panel: define the fields, name the websites allowed to
// submit, and copy the snippet.
//
// Two pieces of copy here are load-bearing rather than decorative, because both
// describe a limit someone would otherwise assume away:
//   - an empty website list accepts nothing, which is the state of every new form;
//   - the website list stops other SITES, not scripts. Anyone who reads the page
//     source has the URL and can post to it with curl, where no browser and no
//     origin check is involved. If that sentence disappears, someone eventually
//     removes the bot check "because we have an allowlist".

interface Props {
  source: LeadSource;
}

/** buildSnippet generates the paste-in HTML + JS for this form's definition. */
function buildSnippet(source: LeadSource, endpoint: string): string {
  const form = source.config?.form;
  const fields = form?.fields ?? [];
  const honeypot = form?.honeypot ?? '';
  const siteKey = form?.turnstile_site_key ?? '';
  const thanks = form?.thank_you || 'Thanks — we’ll be in touch.';

  const inputs = fields
    .map((f) => {
      const req = f.required ? ' required' : '';
      const label = `    <label>${f.label || f.name}\n`;
      const control =
        f.type === 'textarea'
          ? `      <textarea name="${f.name}"${req}></textarea>\n`
          : `      <input type="${f.type || 'text'}" name="${f.name}"${req} />\n`;
      return `${label}${control}    </label>`;
    })
    .join('\n');

  // The honeypot is positioned off-screen rather than display:none — some bots skip
  // hidden inputs, and the point is that they DO fill it in.
  const trap = honeypot
    ? `\n    <div style="position:absolute;left:-9999px" aria-hidden="true">
      <label>Leave this empty<input type="text" name="${honeypot}" tabindex="-1" autocomplete="off" /></label>
    </div>`
    : '';

  const widget = siteKey
    ? `\n    <div class="cf-turnstile" data-sitekey="${siteKey}"></div>`
    : '';
  const widgetScript = siteKey
    ? `<script src="https://challenges.cloudflare.com/turnstile/v0/api.js" async defer></script>\n`
    : '';
  const widgetRead = siteKey
    ? `\n    var t = form.querySelector('[name="cf-turnstile-response"]');\n    if (t) body.turnstile_token = t.value;`
    : '';

  return `${widgetScript}<form id="crm-lead-form">
${inputs}${trap}${widget}
    <button type="submit">Send</button>
  </form>
  <p id="crm-lead-done" hidden>${thanks}</p>

  <script>
  (function () {
    var form = document.getElementById('crm-lead-form');
    var done = document.getElementById('crm-lead-done');
    form.addEventListener('submit', function (e) {
      e.preventDefault();
      var data = new FormData(form);
      var fields = {};
      data.forEach(function (v, k) { fields[k] = v; });
      var body = {
        fields: fields,
        // location.href carries any utm_* the visitor arrived with, so attribution
        // needs nothing extra from you.
        context: { page_url: location.href, referrer: document.referrer }
      };${widgetRead}
      fetch('${endpoint}', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify(body)
      }).then(function (r) {
        if (!r.ok) throw new Error('submit failed');
        form.hidden = true;
        done.hidden = false;
      }).catch(function (err) {
        console.error(err);
        alert('Sorry — that did not send. Please try again.');
      });
    });
  })();
  </script>`;
}

export default function FormEmbedSetupCard({ source }: Props) {
  const updateSource = useUpdateSource();
  const form = source.config?.form;

  const [fields, setFields] = useState<FormField[]>(form?.fields ?? []);
  const [origins, setOrigins] = useState<string[]>(source.allowed_origins ?? []);
  const [originDraft, setOriginDraft] = useState('');
  const [siteKey, setSiteKey] = useState(form?.turnstile_site_key ?? '');
  const [secret, setSecret] = useState('');
  const [copied, setCopied] = useState(false);
  const [error, setError] = useState('');

  useEffect(() => {
    setFields(form?.fields ?? []);
    setOrigins(source.allowed_origins ?? []);
    setSiteKey(form?.turnstile_site_key ?? '');
  }, [source.id, form?.fields, form?.turnstile_site_key, source.allowed_origins]);

  const endpoint = `${window.location.origin}/api/capture/forms/${source.public_token ?? ''}`;
  const snippet = useMemo(
    () => buildSnippet({ ...source, config: { ...source.config, form: { ...form, enabled: true, fields, turnstile_site_key: siteKey } } }, endpoint),
    [source, form, fields, siteKey, endpoint],
  );

  const save = async (input: Parameters<typeof updateSource.mutateAsync>[0]['input']) => {
    setError('');
    try {
      await updateSource.mutateAsync({ id: source.id, input });
    } catch (err) {
      setError(err instanceof Error ? err.message : 'Failed to save');
    }
  };

  const addOrigin = () => {
    const v = originDraft.trim();
    if (!v || origins.includes(v)) return;
    const next = [...origins, v];
    setOrigins(next);
    setOriginDraft('');
    void save({ allowed_origins: next });
  };

  const removeOrigin = (o: string) => {
    const next = origins.filter((x) => x !== o);
    setOrigins(next);
    void save({ allowed_origins: next });
  };

  const saveFields = () =>
    void save({
      form: {
        enabled: true,
        fields,
        honeypot: form?.honeypot || 'company_website',
        thank_you: form?.thank_you,
        turnstile_site_key: siteKey || undefined,
      },
    });

  const copySnippet = async () => {
    try {
      await navigator.clipboard.writeText(snippet);
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    } catch {
      setError('Could not copy — select the snippet and copy it manually.');
    }
  };

  return (
    <div className="rounded-xl border border-border p-4 space-y-5">
      <div>
        <h3 className="text-sm font-medium text-foreground">Website form</h3>
        <p className="text-xs text-muted-foreground mt-0.5">
          Define the fields, say which websites may submit, then paste the snippet into your page.
        </p>
      </div>

      {origins.length === 0 && (
        <div className="flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 p-3 text-xs">
          <AlertTriangle className="h-4 w-4 shrink-0 text-warning" aria-hidden />
          <span className="text-foreground">
            No website is allowed to submit this form yet, so browsers are being turned away. Add
            the site the form lives on below.
          </span>
        </div>
      )}

      {/* Websites */}
      <div className="space-y-1.5">
        <span className="text-xs text-muted-foreground">Websites allowed to submit</span>
        <div className="flex flex-wrap gap-1.5">
          {origins.map((o) => (
            <Badge key={o} variant="secondary" className="gap-1">
              {o}
              <button type="button" onClick={() => removeOrigin(o)} aria-label={`Remove ${o}`}>
                <X className="h-3 w-3" />
              </button>
            </Badge>
          ))}
        </div>
        <div className="flex items-center gap-2">
          <Input
            value={originDraft}
            placeholder="https://example.com"
            onChange={(e) => setOriginDraft(e.target.value)}
            onKeyDown={(e) => {
              if (e.key === 'Enter') {
                e.preventDefault();
                addOrigin();
              }
            }}
            className="w-72"
          />
          <Button size="sm" variant="outline" onClick={addOrigin} disabled={!originDraft.trim()}>
            <Plus className="h-3.5 w-3.5" />
            Add
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Scheme and domain only. This stops other <em>websites</em> embedding your form and
          reading the result — it does not stop a script, which can post to the URL directly. The
          bot check below and the daily limit are what bound that.
        </p>
      </div>

      {/* Fields */}
      <div className="space-y-1.5 border-t border-border pt-4">
        <span className="text-xs text-muted-foreground">Fields this form collects</span>
        {fields.map((f, i) => (
          <div key={i} className="flex items-center gap-2">
            <Input
              value={f.name}
              placeholder="email"
              aria-label={`Field ${i + 1} name`}
              onChange={(e) =>
                setFields(fields.map((x, j) => (j === i ? { ...x, name: e.target.value } : x)))
              }
              className="w-40 font-mono text-xs"
            />
            <Input
              value={f.label}
              placeholder="Email"
              aria-label={`Field ${i + 1} label`}
              onChange={(e) =>
                setFields(fields.map((x, j) => (j === i ? { ...x, label: e.target.value } : x)))
              }
              className="w-44"
            />
            <Select
              value={f.type}
              aria-label={`Field ${i + 1} type`}
              onChange={(e) =>
                setFields(fields.map((x, j) => (j === i ? { ...x, type: e.target.value } : x)))
              }
              className="w-28"
            >
              {FORM_FIELD_TYPES.map((t) => (
                <option key={t} value={t}>{t}</option>
              ))}
            </Select>
            <Button size="sm" variant="ghost" onClick={() => setFields(fields.filter((_, j) => j !== i))} aria-label={`Remove field ${i + 1}`}>
              <X className="h-3.5 w-3.5" />
            </Button>
          </div>
        ))}
        <div className="flex items-center gap-2 pt-1">
          <Button
            size="sm"
            variant="outline"
            onClick={() => setFields([...fields, { name: '', label: '', type: 'text', required: false }])}
          >
            <Plus className="h-3.5 w-3.5" />
            Add field
          </Button>
          <Button size="sm" onClick={saveFields} disabled={updateSource.isPending}>
            {updateSource.isPending ? 'Saving…' : 'Save fields'}
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Only these fields are accepted — anything else a submission carries is ignored, which is
          what stops a stranger writing arbitrary data into your delivery log.
        </p>
      </div>

      {/* Bot check */}
      <div className="space-y-1.5 border-t border-border pt-4">
        <span className="text-xs text-muted-foreground">
          Bot check (Cloudflare Turnstile){' '}
          {source.turnstile_configured && <Badge variant="success">configured</Badge>}
        </span>
        <div className="flex items-center gap-2">
          <Input
            value={siteKey}
            placeholder="Site key"
            aria-label="Turnstile site key"
            onChange={(e) => setSiteKey(e.target.value)}
            className="w-64 font-mono text-xs"
          />
          <Input
            value={secret}
            type="password"
            placeholder={source.turnstile_configured ? 'Secret key (set)' : 'Secret key'}
            aria-label="Turnstile secret key"
            onChange={(e) => setSecret(e.target.value)}
            className="w-64 font-mono text-xs"
          />
          <Button
            size="sm"
            variant="outline"
            onClick={() => {
              void save({
                form: { enabled: true, fields, honeypot: form?.honeypot || 'company_website', turnstile_site_key: siteKey || undefined },
                ...(secret ? { turnstile_secret: secret } : {}),
              });
              setSecret('');
            }}
            disabled={updateSource.isPending}
          >
            Save keys
          </Button>
        </div>
        <p className="text-xs text-muted-foreground">
          Optional, and free from Cloudflare — use your own account's keys. Without it, a form has
          only the hidden honeypot field and the daily limit standing between it and a script.
        </p>
      </div>

      {/* Snippet */}
      <div className="space-y-1.5 border-t border-border pt-4">
        <div className="flex items-center justify-between">
          <span className="text-xs text-muted-foreground">Paste this into your page</span>
          <Button size="sm" variant="outline" onClick={() => void copySnippet()}>
            {copied ? <Check className="h-3.5 w-3.5" /> : <Copy className="h-3.5 w-3.5" />}
            {copied ? 'Copied' : 'Copy'}
          </Button>
        </div>
        <pre className="max-h-80 overflow-auto rounded-lg border border-border bg-muted/40 p-3 font-mono text-[11px] leading-relaxed">
          {snippet}
        </pre>
        <p className="text-xs text-muted-foreground">
          The snippet sends the page address with each submission, so any <code>utm_</code> tags a
          visitor arrived with land on the contact automatically.
        </p>
      </div>

      {error && (
        <div className="rounded-lg border border-destructive/40 bg-destructive/10 p-3 text-sm text-destructive">
          {error}
        </div>
      )}
    </div>
  );
}
