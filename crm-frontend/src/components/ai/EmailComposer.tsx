import { useState } from 'react';
import { composeEmail } from '../../lib/api';

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
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200 p-4">
      <div className="bg-card w-full max-w-3xl rounded-2xl shadow-xl overflow-hidden animate-in zoom-in-95 duration-200 flex flex-col max-h-[90vh]">
        <div className="p-6 border-b bg-muted/10">
          <div className="flex items-center gap-3">
            <div className="h-10 w-10 shrink-0 bg-blue-600/10 text-blue-600 rounded-xl flex items-center justify-center">
              <svg xmlns="http://www.w3.org/2000/svg" width="20" height="20" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect width="20" height="16" x="2" y="4" rx="2"/><path d="m22 7-8.97 5.7a1.94 1.94 0 0 1-2.06 0L2 7"/></svg>
            </div>
            <div>
              <h2 className="text-lg font-bold">AI Email Composer</h2>
              {contactName && <p className="text-sm text-muted-foreground">Drafting for {contactName}</p>}
            </div>
          </div>
        </div>

        <div className="flex-1 overflow-y-auto p-6 grid grid-cols-1 md:grid-cols-2 gap-6">
          {/* Left: Inputs */}
          <div className="space-y-4">
            <div className="space-y-1.5">
              <label className="text-sm font-semibold">What is the goal of this email?</label>
              <textarea
                value={instruction}
                onChange={(e) => setInstruction(e.target.value)}
                placeholder="e.g., Follow up on our meeting from yesterday and ask for their Q3 budget requirements."
                className="w-full h-32 rounded-xl border bg-muted/30 px-4 py-3 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none placeholder:text-muted-foreground/60"
              />
            </div>

            <div className="space-y-1.5">
              <label className="text-sm font-semibold">Tone</label>
              <select
                value={tone}
                onChange={(e) => setTone(e.target.value)}
                className="w-full rounded-xl border bg-muted/30 px-4 py-2.5 text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
              >
                <option value="professional">👔 Professional</option>
                <option value="friendly">👋 Friendly & Casual</option>
                <option value="urgent">⏱️ Urgent Action Required</option>
                <option value="persuasive">🎯 Hard Sell / Persuasive</option>
              </select>
            </div>

            <button
              onClick={generateEmail}
              disabled={isGenerating || !instruction.trim()}
              className="w-full rounded-xl bg-blue-600 px-4 py-3 text-sm font-bold text-white transition-all hover:bg-blue-700 disabled:opacity-50 flex items-center justify-center gap-2"
            >
              {isGenerating ? (
                <>
                  <svg className="h-4 w-4 animate-spin" xmlns="http://www.w3.org/2000/svg" fill="none" viewBox="0 0 24 24"><circle className="opacity-25" cx="12" cy="12" r="10" stroke="currentColor" strokeWidth="4"></circle><path className="opacity-75" fill="currentColor" d="M4 12a8 8 0 018-8V0C5.373 0 0 5.373 0 12h4zm2 5.291A7.962 7.962 0 014 12H0c0 3.042 1.135 5.824 3 7.938l3-2.647z"></path></svg>
                  Drafting...
                </>
              ) : (
                'Generate Draft'
              )}
            </button>

            {error && (
              <div className="p-3 rounded-lg bg-red-500/10 text-red-500 text-sm border border-red-500/20">
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
                  className="text-blue-600 hover:text-blue-700 text-xs flex items-center gap-1 bg-blue-600/10 px-2 py-1 rounded transition-colors"
                >
                  <svg xmlns="http://www.w3.org/2000/svg" width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><rect width="14" height="14" x="8" y="8" rx="2" ry="2"/><path d="M4 16c-1.1 0-2-.9-2-2V4c0-1.1.9-2 2-2h10c1.1 0 2 .9 2 2"/></svg>
                  Copy
                </button>
              )}
            </h3>
            <div className="flex-1 rounded-xl border bg-card p-5 text-sm whitespace-pre-wrap overflow-y-auto relative min-h-[250px] shadow-inner">
              {output ? (
                <div className="text-foreground leading-relaxed">
                  {wrapTextWithParagraphs(output)}
                  {isGenerating && <span className="inline-block w-2 h-4 bg-blue-600 animate-pulse ml-1 align-middle"></span>}
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
          <button
            onClick={onClose}
            className="px-5 py-2 text-sm font-medium rounded-xl hover:bg-muted transition-colors border"
          >
            Close
          </button>
        </div>
      </div>
    </div>
  );
}
