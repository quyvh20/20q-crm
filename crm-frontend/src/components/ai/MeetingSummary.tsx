import { useState, useEffect } from 'react';
import { Brain, Sparkles } from 'lucide-react';
import { submitSummarizeMeeting, getAccessToken } from '../../lib/api';
import Modal from '../common/Modal';
import { Button } from '../ui/button';
import { Spinner } from '../ui/spinner';

interface MeetingSummaryProps {
  dealId?: string;
  contactId?: string;
  onClose: () => void;
  onTasksCreated?: () => void;
}

export default function MeetingSummary({ dealId, contactId, onClose, onTasksCreated }: MeetingSummaryProps) {
  const [transcript, setTranscript] = useState('');
  const [status, setStatus] = useState<'idle' | 'processing' | 'done' | 'error'>('idle');
  const [jobId, setJobId] = useState('');
  const [result, setResult] = useState<any>(null);
  const [error, setError] = useState('');

  // Setup SSE Listener for the specific job
  useEffect(() => {
    if (status !== 'processing' || !jobId) return;

    const token = getAccessToken();
    if (!token) return;

    // Wait, the user's Go backend AuthMiddleware currently checks Authorization header. SSE natively in browsers doesn't support headers via EventSource!
    // Using a polyfill or fetch workaround is preferred. Let's use the fetch workaround.

    const API_BASE = (import.meta as any).env?.VITE_API_URL ?? ((import.meta as any).env?.DEV ? 'http://localhost:8080' : '');
    const abort = new AbortController();

    const pullEvents = async () => {
      try {
        const response = await fetch(`${API_BASE}/api/events`, {
          headers: { 'Authorization': `Bearer ${token}`, 'Accept': 'text/event-stream' },
          credentials: 'include',
          signal: abort.signal
        });
        
        if (!response.ok) throw new Error('SSE failed to connect');
        if (!response.body) return;

        const reader = response.body.getReader();
        const decoder = new TextDecoder();
        let buffer = '';

        while (true) {
          const { done, value } = await reader.read();
          if (done) break;
          buffer += decoder.decode(value, { stream: true });
          const lines = buffer.split('\n');
          buffer = lines.pop() ?? '';
          for (const line of lines) {
            if (line.startsWith('data: ')) {
              const str = line.slice(6);
              if (str === '') continue;
              try {
                const data = JSON.parse(str);
                if (data.type === 'job_complete' && data.job_id === jobId) {
                  if (data.status === 'completed') {
                    setResult(data.result);
                    setStatus('done');
                    if (onTasksCreated) onTasksCreated();
                  } else {
                    setError(data.error || 'Job failed');
                    setStatus('error');
                  }
                  abort.abort(); // close connection
                  return;
                }
              } catch (e) {}
            }
          }
        }
      } catch (e: any) {
        if (e.name !== 'AbortError') {
          setError(e.message);
          setStatus('error');
        }
      }
    };

    pullEvents();

    return () => {
      abort.abort();
    };
  }, [jobId, status]);

  const handleSubmit = async () => {
    setStatus('processing');
    setError('');
    try {
      const res = await submitSummarizeMeeting(transcript, dealId, contactId);
      setJobId(res.job_id);
    } catch (e: any) {
      setError(e.message);
      setStatus('error');
    }
  };

  return (
    // Shared Radix modal (U7). Dismissal is blocked while the summarize job is
    // in flight — closing would abort the SSE listener and strand the job.
    <Modal
      open
      onClose={onClose}
      title="Meeting Summarizer"
      description="Extract themes and auto-create tasks from transcripts."
      size="2xl"
      padded={false}
      dismissable={status !== 'processing'}
    >
      <>
        <div className="p-6">
          {status === 'idle' || status === 'error' ? (
            <div className="space-y-4">
              <textarea
                value={transcript}
                onChange={(e) => setTranscript(e.target.value)}
                placeholder="Paste the meeting transcript here..."
                className="h-64 w-full resize-none rounded-lg border border-input bg-muted/30 px-4 py-3 font-mono text-xs focus:outline-none focus:ring-2 focus:ring-ring"
              />
              {status === 'error' && (
                <div className="rounded-lg border border-destructive/20 bg-destructive/10 p-3 text-sm text-destructive">
                  {error}
                </div>
              )}
            </div>
          ) : status === 'processing' ? (
            <div className="flex flex-col items-center justify-center space-y-6 py-20 text-center">
              <Spinner size="lg" />
              <div>
                <p className="mb-1 flex items-center justify-center gap-2 text-lg font-semibold text-primary">
                  <Brain aria-hidden className="h-5 w-5" />
                  AI is analyzing...
                </p>
                <p className="text-sm text-muted-foreground max-w-sm">
                  We're distilling key insights, identifying decisions, and extracting action items directly into your CRM.
                </p>
              </div>
            </div>
          ) : (
            <div className="space-y-6 animate-in slide-in-from-bottom-4 duration-500 fade-in">
              <div className="bg-emerald-500/10 border border-emerald-500/20 rounded-xl p-4 flex items-start gap-4">
                <Sparkles aria-hidden className="mt-1 h-6 w-6 shrink-0 text-emerald-600 dark:text-emerald-400" />
                <div>
                  <h3 className="font-bold text-emerald-700 dark:text-emerald-400 mb-1">Executive Summary</h3>
                  <p className="text-sm leading-relaxed">{result?.summary}</p>
                </div>
              </div>

              {result?.created_tasks?.length > 0 && (
                <div>
                  <h3 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-3">Tasks Created Automatically</h3>
                  <div className="space-y-2">
                    {result.created_tasks.map((task: any) => (
                      <div key={task.id} className="flex flex-col gap-1 p-3 rounded-xl border bg-muted/20">
                        <div className="flex justify-between items-start gap-4">
                          <p className="text-sm font-semibold">{task.title}</p>
                          <span className="text-[10px] px-2 py-0.5 rounded-full bg-primary/10 text-primary font-bold tracking-wider uppercase">
                            {task.priority || 'medium'}
                          </span>
                        </div>
                        {task.due_at && (
                          <p className="text-xs text-muted-foreground">Due: {new Date(task.due_at).toLocaleDateString()}</p>
                        )}
                      </div>
                    ))}
                  </div>
                </div>
              )}
            </div>
          )}
        </div>

        <div className="px-6 py-4 bg-muted/30 border-t flex justify-end gap-3">
          {(status === 'done' || status === 'error') && (
            <Button
              variant="outline"
              onClick={() => {
                setStatus('idle');
                setTranscript('');
              }}
              className="mr-auto"
            >
              Summarize Another
            </Button>
          )}
          <Button variant="outline" onClick={onClose}>
            {status === 'done' ? 'Done' : 'Cancel'}
          </Button>
          {status === 'idle' && (
            <Button onClick={handleSubmit} disabled={!transcript.trim()}>
              Analyze Meeting
            </Button>
          )}
        </div>
      </>
    </Modal>
  );
}
