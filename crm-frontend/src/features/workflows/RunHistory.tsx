import React, { useEffect, useState, useCallback } from 'react';
import { useParams, useNavigate, useLocation } from 'react-router-dom';
import {
  ArrowLeft,
  Check,
  ChevronDown,
  ChevronLeft,
  ChevronRight,
  Clipboard,
  Hourglass,
  RotateCcw,
  X,
} from 'lucide-react';
import { getWorkflowRuns, getRunDetail, getWorkflow, retryRun } from './api';
import type { WorkflowRun, ActionLog, Workflow } from './types';
import { STATUS_BADGE_VARIANT, ACTION_LABELS } from './types';
import { useAuth } from '../../lib/auth';
import { useDocumentTitle } from '../../lib/useDocumentTitle';
import { canRunWorkflowNow } from './RunNowModal';
import { Badge, Button, EmptyState, SpinnerBlock } from '@/components/ui';

export const RunHistory: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const location = useLocation();
  const { user, hasCapability } = useAuth();
  // A run id handed over via navigation state — set by the "Run started" toast's
  // "View run" link and by the builder's Run Now. When present, auto-open that run's
  // detail and scroll/flash it once it appears in the loaded list.
  const highlightRunId = (location.state as { highlightRunId?: string } | null)?.highlightRunId ?? null;
  const [flashRunId, setFlashRunId] = useState<string | null>(null);
  const [highlightHandled, setHighlightHandled] = useState(false);
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [actionLogs, setActionLogs] = useState<ActionLog[]>([]);
  const [loadingLogs, setLoadingLogs] = useState(false);
  // The failed run currently being re-queued (P21) — disables its button while in flight.
  const [retryingRunId, setRetryingRunId] = useState<string | null>(null);
  const [retryError, setRetryError] = useState<string | null>(null);

  // Tab title from the loaded workflow (U7.2), matching the page heading. Null
  // until it lands ⇒ the bare app name rather than "undefined".
  useDocumentTitle(workflow?.name ? `Run History · ${workflow.name}` : null);

  // Full fetch — shows the loading spinner (used on initial mount + page changes).
  const fetchRuns = useCallback(async () => {
    if (!id) return;
    setLoading(true);
    try {
      const [wf, runsResp] = await Promise.all([
        getWorkflow(id),
        getWorkflowRuns(id, page),
      ]);
      setWorkflow(wf);
      setRuns(runsResp.runs || []);
      setTotal(runsResp.total);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  }, [id, page]);

  // Silent background refresh — no spinner, used by the polling interval.
  const silentRefresh = useCallback(async () => {
    if (!id) return;
    try {
      const [wf, runsResp] = await Promise.all([
        getWorkflow(id),
        getWorkflowRuns(id, page),
      ]);
      setWorkflow(wf);
      setRuns(runsResp.runs || []);
      setTotal(runsResp.total);
    } catch (e) {
      console.error('Background refresh failed:', e);
    }
  }, [id, page]);

  // Retry a failed run (P21): re-queue it server-side, then refresh so the row flips to
  // pending and the live poller (below) tracks it to completion. The run resumes from the
  // step that failed — completed steps are not re-executed.
  const handleRetry = useCallback(async (runId: string) => {
    setRetryingRunId(runId);
    setRetryError(null);
    try {
      await retryRun(runId);
      await silentRefresh();
    } catch (e) {
      setRetryError(e instanceof Error ? e.message : 'Failed to retry run');
    } finally {
      setRetryingRunId(null);
    }
  }, [silentRefresh]);

  // Auto-poll every 5 s while any run is pending or running — or parked on a
  // delay that resumes within the next 10 minutes (longer waits change nothing
  // worth polling for; the visibility-return refresh covers them).
  // Pauses when the tab is hidden; resumes (with an immediate refresh) when visible.
  const ACTIVE_STATUSES = ['pending', 'running'];
  useEffect(() => {
    const wakesSoon = (r: WorkflowRun) =>
      r.status === 'waiting' &&
      !!r.wake_at &&
      new Date(r.wake_at).getTime() - Date.now() < 10 * 60 * 1000;
    const hasActive = runs.some((r) => ACTIVE_STATUSES.includes(r.status) || wakesSoon(r));
    if (!hasActive) return;

    let interval: ReturnType<typeof setInterval> | null = null;

    const start = () => {
      if (interval) return; // already running
      interval = setInterval(silentRefresh, 5000);
    };

    const stop = () => {
      if (interval) {
        clearInterval(interval);
        interval = null;
      }
    };

    const onVisibilityChange = () => {
      if (document.hidden) {
        stop();
      } else {
        silentRefresh(); // immediate refresh on tab return
        start();
      }
    };

    document.addEventListener('visibilitychange', onVisibilityChange);
    if (!document.hidden) start(); // only start if tab is currently visible

    return () => {
      stop();
      document.removeEventListener('visibilitychange', onVisibilityChange);
    };
  }, [runs, silentRefresh]);

  useEffect(() => {
    fetchRuns();
  }, [fetchRuns]);

  // Open a run's detail (load its action logs). Always selects — never toggles — so it
  // can be driven both by a user click and by the highlight effect below.
  const openRunDetail = useCallback(async (runId: string) => {
    setSelectedRunId(runId);
    setLoadingLogs(true);
    try {
      const detail = await getRunDetail(runId);
      setActionLogs(detail.action_logs || []);
    } catch (e) {
      console.error(e);
    } finally {
      setLoadingLogs(false);
    }
  }, []);

  const handleSelectRun = (runId: string) => {
    if (selectedRunId === runId) {
      setSelectedRunId(null);
      return;
    }
    openRunDetail(runId);
  };

  // Arriving with a highlightRunId (from a Run Now toast / builder Run Now): open that
  // run's detail, scroll it into view, and flash it briefly. Runs once, after the run
  // shows up in the loaded list.
  useEffect(() => {
    if (highlightHandled || !highlightRunId) return;
    if (!runs.some((r) => r.id === highlightRunId)) return; // wait until the run is loaded
    setHighlightHandled(true);
    openRunDetail(highlightRunId);
    setFlashRunId(highlightRunId);
    const el = document.getElementById(`run-${highlightRunId}`);
    if (el && typeof el.scrollIntoView === 'function') {
      el.scrollIntoView({ behavior: 'smooth', block: 'center' });
    }
    const t = setTimeout(() => setFlashRunId(null), 2500);
    return () => clearTimeout(t);
  }, [runs, highlightRunId, highlightHandled, openRunDetail]);

  const totalPages = Math.ceil(total / 20);

  // Retry permission mirrors the backend (authorizeRunNow): owner/admin/manager, or the
  // workflow's creator. The same rule gates Run Now, so we reuse canRunWorkflowNow. The
  // server still enforces it (403) — this only drives the disabled state in the UI.
  const canRetry = canRunWorkflowNow(hasCapability('workflows.run_any'), user?.id, {
    created_by: workflow?.created_by ?? null,
  });

  return (
    <div className="mx-auto w-full max-w-6xl">
      {/* Header */}
      <div className="mb-8 flex items-center gap-4">
        <button
          onClick={() => navigate('/workflows')}
          className="inline-flex items-center gap-1.5 text-sm text-muted-foreground transition-colors hover:text-foreground focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
        >
          <ArrowLeft aria-hidden className="h-4 w-4" /> Back
        </button>
        <div>
          <h1 className="text-2xl font-semibold tracking-tight text-foreground">
            {workflow?.name || 'Run History'}
          </h1>
          <p className="mt-1 text-sm text-muted-foreground">
            {total} run{total !== 1 ? 's' : ''} total
          </p>
        </div>
      </div>

      {retryError && (
        <div className="mb-4 flex items-center justify-between rounded-lg border border-destructive/40 bg-destructive/10 px-4 py-2.5 text-sm text-destructive">
          <span>{retryError}</span>
          <button
            onClick={() => setRetryError(null)}
            className="ml-4 rounded px-1 text-destructive hover:bg-destructive/20 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
            aria-label="Dismiss error"
          >
            <X aria-hidden className="h-4 w-4" />
          </button>
        </div>
      )}

      {loading ? (
        <SpinnerBlock label="Loading…" />
      ) : runs.length === 0 ? (
        <EmptyState
          icon={Hourglass}
          title="No runs yet"
          description="This workflow hasn't been triggered yet"
        />
      ) : (
        <div className="space-y-2">
          {runs.map((run) => (
            <div key={run.id} id={`run-${run.id}`}>
              <div
                onClick={() => handleSelectRun(run.id)}
                className={`
                  bg-muted/40 border rounded-xl p-4 cursor-pointer transition-all
                  ${selectedRunId === run.id ? 'border-primary bg-card' : 'border-border hover:border-ring'}
                  ${flashRunId === run.id ? 'ring-2 ring-ring' : ''}
                `}
              >
                <div className="flex items-center gap-4">
                  {/* Status badge */}
                  <Badge
                    variant={STATUS_BADGE_VARIANT[run.status] ?? 'secondary'}
                    className="uppercase tracking-wider"
                  >
                    {run.status}
                  </Badge>

                  {/* Run info */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3">
                      <span className="text-xs text-muted-foreground font-mono">
                        {run.id.slice(0, 8)}...
                      </span>
                      <span className="text-xs text-muted-foreground">
                        v{run.workflow_version}
                      </span>
                      {run.retry_count > 0 && (
                        <span className="text-xs text-amber-600 dark:text-amber-400">
                          {run.retry_count} retries
                        </span>
                      )}
                    </div>
                    {run.last_error && (
                      <p className="text-xs text-destructive mt-1 truncate">{run.last_error}</p>
                    )}
                  </div>

                  {/* Timing */}
                  <div className="text-right">
                    <p className="text-xs text-muted-foreground">
                      {new Date(run.created_at).toLocaleString()}
                    </p>
                    {run.status === 'waiting' && run.wake_at && (
                      <p className="inline-flex items-center gap-1 text-xs text-amber-600 dark:text-amber-400">
                        <Hourglass aria-hidden className="h-3 w-3" /> resumes {formatRelativeTime(run.wake_at)}
                      </p>
                    )}
                    {run.finished_at && (
                      <p className="text-xs text-muted-foreground/70">
                        Duration: {formatDuration(run.started_at, run.finished_at)}
                      </p>
                    )}
                  </div>

                  {/* Retry (P21) — failed runs only. Resumes from the failed step,
                      preserving completed work. stopPropagation so it doesn't toggle the row. */}
                  {run.status === 'failed' && (
                    <Button
                      variant="outline"
                      size="sm"
                      onClick={(e) => {
                        e.stopPropagation();
                        handleRetry(run.id);
                      }}
                      disabled={!canRetry || retryingRunId === run.id}
                      title={
                        canRetry
                          ? 'Re-queue this run — resumes from the step that failed, keeping completed steps'
                          : "Only an admin, manager, or the workflow's creator can retry runs"
                      }
                    >
                      <RotateCcw aria-hidden className={retryingRunId === run.id ? 'animate-spin' : undefined} />
                      {retryingRunId === run.id ? 'Retrying…' : 'Retry'}
                    </Button>
                  )}

                  {selectedRunId === run.id ? (
                    <ChevronDown aria-hidden className="h-4 w-4 text-muted-foreground/70" />
                  ) : (
                    <ChevronRight aria-hidden className="h-4 w-4 text-muted-foreground/70" />
                  )}
                </div>
              </div>

              {/* Expanded detail */}
              {selectedRunId === run.id && (
                <div className="ml-8 mt-1 space-y-2 mb-3">
                  {/* Trigger context accordion — above action logs */}
                  {run.trigger_context && Object.keys(run.trigger_context).length > 0 && (
                    <TriggerContextAccordion context={run.trigger_context} />
                  )}

                  {/* Action logs */}
                  {loadingLogs ? (
                    <div className="py-4 text-center text-muted-foreground/70 text-sm">Loading...</div>
                  ) : actionLogs.length === 0 ? (
                    <div className="py-4 text-center text-muted-foreground/70 text-sm">No action logs</div>
                  ) : (
                    actionLogs.map((log) => {
                      const hasDetail = log.input || log.output || log.error;
                      return (
                        <details
                          key={log.id}
                          className="bg-muted/40 border border-border/60 rounded-lg overflow-hidden group/log"
                        >
                          <summary className="flex items-center gap-3 p-3 cursor-pointer select-none hover:bg-accent hover:text-accent-foreground transition-colors list-none">
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="text-xs font-medium text-foreground">
                                  Step {log.action_idx + 1}: {ACTION_LABELS[log.action_type as keyof typeof ACTION_LABELS] || log.action_type}
                                </span>
                                <span className="text-xs text-muted-foreground">
                                  {log.duration_ms}ms
                                </span>
                                {log.attempt_no > 1 && (
                                  <span className="text-xs text-amber-600 dark:text-amber-400">attempt #{log.attempt_no}</span>
                                )}
                              </div>
                              {log.error && (
                                <p className="text-xs text-destructive mt-0.5 truncate group-open/log:hidden">{log.error}</p>
                              )}
                            </div>
                            <Badge variant={STATUS_BADGE_VARIANT[log.status] ?? 'secondary'}>
                              {log.status}
                            </Badge>
                            {/* Deep link (A3.6): jump to this step's node in the builder. Only
                                steps-based runs carry a structural action_path; preventDefault
                                stops the click from toggling the <details>. */}
                            {log.action_path && (
                              <button
                                type="button"
                                onClick={(e) => {
                                  e.preventDefault();
                                  e.stopPropagation();
                                  navigate(`/workflows/${id}?node=${encodeURIComponent(log.action_path!)}`);
                                }}
                                title="Open this step in the builder"
                                className="shrink-0 rounded px-1.5 py-0.5 text-[11px] text-muted-foreground hover:bg-accent hover:text-foreground"
                              >
                                Open in builder
                              </button>
                            )}
                            {hasDetail && (
                              <ChevronRight aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground/70 transition-transform group-open/log:rotate-90" />
                            )}
                          </summary>

                          {hasDetail && (
                            <div className="px-3 pb-3 space-y-2 border-t border-border/60 pt-2">
                              {log.input && Object.keys(log.input).length > 0 && (
                                <div>
                                  <p className="text-xs text-muted-foreground mb-1 font-medium">Input (resolved params)</p>
                                  <pre className="text-xs font-mono leading-relaxed p-2 bg-muted/40 rounded-lg border border-border/60 overflow-x-auto max-h-40 overflow-y-auto">
                                    {syntaxHighlightJSON(JSON.stringify(redactValue(log.input), null, 2))}
                                  </pre>
                                </div>
                              )}
                              {log.output && Object.keys(log.output).length > 0 && (
                                <div>
                                  <p className="text-xs text-muted-foreground mb-1 font-medium">Output</p>
                                  <pre className="text-xs font-mono leading-relaxed p-2 bg-muted/40 rounded-lg border border-border/60 overflow-x-auto max-h-40 overflow-y-auto">
                                    {syntaxHighlightJSON(JSON.stringify(redactValue(log.output), null, 2))}
                                  </pre>
                                </div>
                              )}
                              {log.error && (
                                <div>
                                  <p className="text-xs text-muted-foreground mb-1 font-medium">Error</p>
                                  <pre className="text-xs font-mono text-destructive p-2 bg-destructive/10 rounded-lg border border-destructive/30 whitespace-pre-wrap">
                                    {log.error}
                                  </pre>
                                </div>
                              )}
                            </div>
                          )}
                        </details>
                      );
                    })
                  )}
                </div>
              )}
            </div>
          ))}
        </div>
      )}

      {/* Pagination */}
      {totalPages > 1 && (
        <div className="mt-6 flex items-center justify-center gap-2">
          <Button
            variant="outline"
            size="icon"
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            aria-label="Previous page"
          >
            <ChevronLeft aria-hidden />
          </Button>
          <span className="px-3 py-1.5 text-sm text-muted-foreground">
            Page {page} of {totalPages}
          </span>
          <Button
            variant="outline"
            size="icon"
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
            aria-label="Next page"
          >
            <ChevronRight aria-hidden />
          </Button>
        </div>
      )}
    </div>
  );
};

function formatDuration(startStr?: string, endStr?: string): string {
  if (!startStr || !endStr) return '-';
  const start = new Date(startStr).getTime();
  const end = new Date(endStr).getTime();
  const ms = end - start;
  if (ms < 1000) return `${ms}ms`;
  if (ms < 60000) return `${(ms / 1000).toFixed(1)}s`;
  return `${Math.round(ms / 60000)}min`;
}

// formatRelativeTime renders a future timestamp as "in 2m" / "in 3h 20m" /
// "in 5d"; past-due wakes (sweeper about to pick the run up) show "any moment".
function formatRelativeTime(iso: string): string {
  const ms = new Date(iso).getTime() - Date.now();
  if (ms <= 0) return 'any moment';
  const min = Math.round(ms / 60000);
  if (min < 1) return 'in <1m';
  if (min < 60) return `in ${min}m`;
  const hours = Math.floor(min / 60);
  if (hours < 24) {
    const rem = min % 60;
    return rem > 0 ? `in ${hours}h ${rem}m` : `in ${hours}h`;
  }
  return `in ${Math.round(hours / 24)}d`;
}

// --- Trigger Context Accordion ---

// Keys to redact — matches backend §8 slog redaction list
const REDACT_KEYS = new Set([
  'email', 'phone', 'password', 'token', 'secret', 'api_key',
]);

function redactValue(obj: unknown): unknown {
  if (obj === null || obj === undefined) return obj;
  if (Array.isArray(obj)) return obj.map(redactValue);
  if (typeof obj === 'object') {
    const result: Record<string, unknown> = {};
    for (const [key, val] of Object.entries(obj as Record<string, unknown>)) {
      if (REDACT_KEYS.has(key.toLowerCase())) {
        result[key] = '***';
      } else {
        result[key] = redactValue(val);
      }
    }
    return result;
  }
  return obj;
}

/** Lightweight syntax highlighting for JSON — returns JSX spans */
function syntaxHighlightJSON(json: string): React.ReactNode[] {
  const parts: React.ReactNode[] = [];

  // Simple line-by-line approach for stability
  const lines = json.split('\n');
  for (let i = 0; i < lines.length; i++) {
    const line = lines[i];
    // Highlight keys, string values, numbers, booleans
    const highlighted = line
      .replace(/"([^"\\]|\\.)*"\s*:/g, (match) => {
        const key = match.slice(0, -1).trim(); // remove trailing :
        return `<k>${key}</k>:`;
      })
      .replace(/:\s*"([^"\\]|\\.)*"/g, (match) => {
        return `: <s>${match.slice(match.indexOf('"'))}</s>`;
      })
      .replace(/:\s*(-?\d+\.?\d*)\b/g, (_match, num) => {
        return `: <n>${num}</n>`;
      })
      .replace(/:\s*(true|false|null)\b/g, (_, val) => {
        return `: <b>${val}</b>`;
      });

    // Parse our custom tags into spans
    const elements: React.ReactNode[] = [];
    let tagMatch;
    const tagRegex = /<(k|s|n|b)>(.*?)<\/\1>/g;
    let lastIndex = 0;

    while ((tagMatch = tagRegex.exec(highlighted)) !== null) {
      if (tagMatch.index > lastIndex) {
        elements.push(
          <span key={`t-${i}-${lastIndex}`} className="text-muted-foreground">
            {highlighted.slice(lastIndex, tagMatch.index)}
          </span>
        );
      }
      const tagType = tagMatch[1];
      const content = tagMatch[2];
      // Theme-aware: the code block is a light token surface (bg-muted/40) in the
      // app's light theme, so keys/strings/etc. need the darker -700 shade to stay
      // readable; -400 is kept for a future dark theme.
      const colorClass =
        tagType === 'k' ? 'text-indigo-700 dark:text-indigo-400' :
        tagType === 's' ? 'text-emerald-700 dark:text-emerald-400' :
        tagType === 'n' ? 'text-amber-700 dark:text-amber-400' :
        'text-purple-700 dark:text-purple-400'; // bool/null
      elements.push(
        <span key={`t-${i}-${tagMatch.index}`} className={colorClass}>
          {content}
        </span>
      );
      lastIndex = tagMatch.index + tagMatch[0].length;
    }

    if (lastIndex < highlighted.length) {
      elements.push(
        <span key={`t-${i}-end`} className="text-muted-foreground">
          {highlighted.slice(lastIndex)}
        </span>
      );
    }

    parts.push(
      <React.Fragment key={`line-${i}`}>
        {elements.length > 0 ? elements : <span className="text-muted-foreground">{line}</span>}
        {i < lines.length - 1 ? '\n' : ''}
      </React.Fragment>
    );
  }

  return parts;
}

const TriggerContextAccordion: React.FC<{ context: Record<string, unknown> }> = ({ context }) => {
  const [copied, setCopied] = useState(false);

  const redacted = redactValue(context) as Record<string, unknown>;
  const jsonString = JSON.stringify(redacted, null, 2);

  const handleCopy = (e: React.MouseEvent) => {
    e.preventDefault(); // don't toggle accordion
    e.stopPropagation();
    navigator.clipboard.writeText(jsonString).then(() => {
      setCopied(true);
      setTimeout(() => setCopied(false), 2000);
    });
  };

  return (
    <details className="bg-muted/40 border border-border/60 rounded-lg overflow-hidden group/ctx">
      <summary className="flex items-center gap-2 px-3 py-2.5 cursor-pointer select-none hover:bg-accent hover:text-accent-foreground transition-colors list-none">
        <ChevronRight aria-hidden className="h-3.5 w-3.5 shrink-0 text-muted-foreground transition-transform group-open/ctx:rotate-90" />
        <span className="text-xs font-medium text-foreground">Trigger Context</span>
        <span className="text-xs text-muted-foreground/70 ml-1">
          ({Object.keys(context).length} field{Object.keys(context).length !== 1 ? 's' : ''})
        </span>
        <button
          onClick={handleCopy}
          className={`ml-auto inline-flex items-center gap-1 rounded px-2 py-0.5 text-xs transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring ${
            copied
              ? 'text-primary bg-primary/10'
              : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
          }`}
          title="Copy to clipboard"
        >
          {copied ? (
            <>
              <Check aria-hidden className="h-3 w-3" /> Copied
            </>
          ) : (
            <>
              <Clipboard aria-hidden className="h-3 w-3" /> Copy
            </>
          )}
        </button>
      </summary>
      <div className="px-3 pb-3">
        <pre className="text-xs font-mono leading-relaxed overflow-x-auto max-h-80 overflow-y-auto p-3 bg-muted/40 rounded-lg border border-border/60">
          {syntaxHighlightJSON(jsonString)}
        </pre>
      </div>
    </details>
  );
};

export default RunHistory;
