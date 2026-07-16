import { useState } from 'react';
import { Copy, Loader2 } from 'lucide-react';
import { composeEmail } from '../../lib/api';
import Modal from '../common/Modal';
import { Button } from '../ui/button';

interface EmailComposerProps {
  contactId?: string;
  dealId?: string;
  contactName?: string;
  onClose: () => void;
}

export default function EmailComposer({ contactId, dealId, contactName, onClose }: EmailComposerProps) {
  const [instruction, setInstruction] = useState('');
  const [tone, setTone] = useState('professional');
  const [output, setOutput] = useState('');
  const [isGenerating, setIsGenerating] = useState(false);
  const [error, setError] = useState('');

  const generateEmail = async () => {
    setIsGenerating(true);
    setOutput('');
    setError('');

    try {
      await composeEmail(
        instruction,
        tone,
        (chunk) => {
          setOutput((prev) => prev + chunk);
        },
        () => {
          setIsGenerating(false);
        },
        (err) => {
          setError(err);
          setIsGenerating(false);
        },
        contactId,
        dealId
      );
    } catch (e: any) {
      setError(e.message);
      setIsGenerating(false);
    }
  };

  const wrapTextWithParagraphs = (text: string) => {
    return text.split('\n').map((line, i) => (
      <p key={i} className="min-h-[1em] mb-2">{line}</p>
    ));
  };

  const copyToClipboard = () => {
    navigator.clipboard.writeText(output);
  };

  return (
    // Shared Radix modal (U7): Escape, focus trap/restore and aria for free.
    // Dismissal is blocked while a draft is streaming — Escape mid-stream would
    // orphan the SSE connection.
    <Modal
      open
      onClose={onClose}
      title="AI Email Composer"
      description={contactName ? `Drafting for ${contactName}` : undefined}
      size="3xl"
      padded={false}
      dismissable={!isGenerating}
    >
      <>
        <div className="p-6 grid grid-cols-1 md:grid-cols-2 gap-6">
          {/* Left: Inputs */}
          <div className="space-y-4">
            <div className="space-y-1.5">
              <label className="text-sm font-semibold">What is the goal of this email?</label>
              <textarea
                value={instruction}
                onChange={(e) => setInstruction(e.target.value)}
                placeholder="e.g., Follow up on our meeting from yesterday and ask for their Q3 budget requirements."
                className="h-32 w-full resize-none rounded-lg border border-input bg-muted/30 px-4 py-3 text-sm placeholder:text-muted-foreground/60 focus:outline-none focus:ring-2 focus:ring-ring"
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-sm font-semibold">Tone</label>
              <select
                value={tone}
                onChange={(e) => setTone(e.target.value)}
                className="w-full rounded-lg border border-input bg-muted/30 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-ring"
              >
                <option value="professional">👔 Professional</option>
                <option value="friendly">👋 Friendly & Casual</option>
                <option value="urgent">⏱️ Urgent Action Required</option>
                <option value="persuasive">🎯 Hard Sell / Persuasive</option>
              </select>
            </div>

            <Button
              onClick={generateEmail}
              disabled={isGenerating || !instruction.trim()}
              className="w-full"
            >
              {isGenerating ? (
                <>
                  <Loader2 aria-hidden className="animate-spin" />
                  Drafting...
                </>
              ) : (
                'Generate Draft'
              )}
            </Button>

            {error && (
              <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
                {error}
              </div>
            )}
          </div>

          {/* Right: Output */}
          <div className="flex flex-col">
            <h3 className="text-sm font-semibold mb-3 flex items-center justify-between">
              Preview
              {output && (
                <button
                  onClick={copyToClipboard}
                  className="flex items-center gap-1 rounded bg-primary/10 px-2 py-1 text-xs text-primary transition-colors hover:text-primary/80"
                >
                  <Copy aria-hidden className="h-3 w-3" />
                  Copy
                </button>
              )}
            </h3>
            <div className="flex-1 rounded-xl border bg-card p-5 text-sm whitespace-pre-wrap overflow-y-auto relative min-h-[250px] shadow-inner">
              {output ? (
                <div className="text-foreground leading-relaxed">
                  {wrapTextWithParagraphs(output)}
                  {isGenerating && <span className="ml-1 inline-block h-4 w-2 animate-pulse bg-primary align-middle"></span>}
                </div>
              ) : (
                <div className="absolute inset-0 flex items-center justify-center text-muted-foreground/50 text-center px-6">
                  Generated email will appear here...
                </div>
              )}
            </div>
          </div>
        </div>

        <div className="px-6 py-4 bg-muted/30 border-t flex justify-end">
          <Button variant="outline" onClick={onClose}>
            Close
          </Button>
        </div>
      </>
    </Modal>
  );
}
