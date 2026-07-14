import { useState, useEffect } from 'react';
import { submitSummarizeMeeting, getAccessToken } from '../../lib/api';
import Modal from '../common/Modal';

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
                className="w-full h-64 rounded-xl border bg-muted/30 px-4 py-3 text-sm focus:outline-none focus:ring-2 focus:ring-violet-500 resize-none font-mono text-xs"
              />
              {status === 'error' && (
                <div className="p-3 rounded-lg bg-red-500/10 text-red-500 text-sm border border-red-500/20">
                  {error}
                </div>
              )}
            </div>
          ) : status === 'processing' ? (
            <div className="py-20 flex flex-col items-center justify-center text-center space-y-6">
              <div className="relative h-20 w-20">
                <div className="absolute inset-0 border-4 border-violet-500/30 rounded-full"></div>
                <div className="absolute inset-0 border-4 border-violet-600 rounded-full border-t-transparent animate-spin"></div>
                <div className="absolute inset-0 flex items-center justify-center text-2xl">🧠</div>
              </div>
              <div>
                <p className="font-semibold text-lg text-violet-600 mb-1 flex items-center justify-center gap-2">🧠 AI is analyzing...</p>
                <p className="text-sm text-muted-foreground max-w-sm">
                  We're distilling key insights, identifying decisions, and extracting action items directly into your CRM.
                </p>
              </div>
            </div>
          ) : (
            <div className="space-y-6 animate-in slide-in-from-bottom-4 duration-500 fade-in">
              <div className="bg-emerald-500/10 border border-emerald-500/20 rounded-xl p-4 flex items-start gap-4">
                <span className="text-2xl mt-1">✨</span>
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
                          <span className="text-[10px] px-2 py-0.5 rounded-full bg-blue-500/10 text-blue-500 font-bold tracking-wider uppercase">
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
            <button
              onClick={() => {
                setStatus('idle');
                setTranscript('');
              }}
              className="px-5 py-2 text-sm font-medium rounded-xl border bg-card hover:bg-muted transition-colors mr-auto"
            >
              Summarize Another
            </button>
          )}
          <button
            onClick={onClose}
            className="px-5 py-2 text-sm font-medium rounded-xl hover:bg-muted transition-colors border bg-card"
          >
            {status === 'done' ? 'Done' : 'Cancel'}
          </button>
          {status === 'idle' && (
            <button
              onClick={handleSubmit}
              disabled={!transcript.trim()}
              className="px-5 py-2 text-sm font-bold rounded-xl bg-violet-600 text-white hover:bg-violet-700 transition-colors disabled:opacity-50"
            >
              Analyze Meeting
            </button>
          )}
        </div>
      </>
    </Modal>
  );
}
