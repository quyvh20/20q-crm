import React, { useEffect, useState, useCallback } from 'react';
import { useParams, useNavigate } from 'react-router-dom';
import { getWorkflowRuns, getRunDetail, getWorkflow } from './api';
import type { WorkflowRun, ActionLog, Workflow } from './types';
import { STATUS_COLORS, ACTION_LABELS } from './types';

export const RunHistory: React.FC = () => {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const [workflow, setWorkflow] = useState<Workflow | null>(null);
  const [runs, setRuns] = useState<WorkflowRun[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [selectedRunId, setSelectedRunId] = useState<string | null>(null);
  const [actionLogs, setActionLogs] = useState<ActionLog[]>([]);
  const [loadingLogs, setLoadingLogs] = useState(false);

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

  useEffect(() => {
    fetchRuns();
  }, [fetchRuns]);

  const handleSelectRun = async (runId: string) => {
    if (selectedRunId === runId) {
      setSelectedRunId(null);
      return;
    }
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
  };

  const totalPages = Math.ceil(total / 20);

  return (
    <div className="max-w-5xl mx-auto py-8 px-4">
      {/* Header */}
      <div className="flex items-center gap-4 mb-8">
        <button
          onClick={() => navigate('/workflows')}
          className="text-gray-500 hover:text-white transition-colors"
        >
          ← Back
        </button>
        <div>
          <h1 className="text-2xl font-bold text-white">
            {workflow?.name || 'Run History'}
          </h1>
          <p className="text-sm text-gray-400 mt-1">
            {total} run{total !== 1 ? 's' : ''} total
          </p>
        </div>
      </div>

      {loading ? (
        <div className="flex justify-center py-16">
          <div className="w-8 h-8 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
        </div>
      ) : runs.length === 0 ? (
        <div className="text-center py-16 text-gray-500">
          <p className="text-lg mb-2">No runs yet</p>
          <p className="text-sm">This workflow hasn't been triggered yet</p>
        </div>
      ) : (
        <div className="space-y-2">
          {runs.map((run) => (
            <div key={run.id}>
              <div
                onClick={() => handleSelectRun(run.id)}
                className={`
                  bg-gray-800/60 border rounded-xl p-4 cursor-pointer transition-all
                  ${selectedRunId === run.id ? 'border-indigo-500 bg-gray-800' : 'border-gray-700 hover:border-gray-600'}
                `}
              >
                <div className="flex items-center gap-4">
                  {/* Status badge */}
                  <span
                    className="px-2.5 py-1 rounded-full text-xs font-semibold uppercase tracking-wider"
                    style={{
                      backgroundColor: `${STATUS_COLORS[run.status]}20`,
                      color: STATUS_COLORS[run.status],
                    }}
                  >
                    {run.status}
                  </span>

                  {/* Run info */}
                  <div className="flex-1 min-w-0">
                    <div className="flex items-center gap-3">
                      <span className="text-xs text-gray-500 font-mono">
                        {run.id.slice(0, 8)}...
                      </span>
                      <span className="text-xs text-gray-500">
                        v{run.workflow_version}
                      </span>
                      {run.retry_count > 0 && (
                        <span className="text-xs text-amber-400">
                          {run.retry_count} retries
                        </span>
                      )}
                    </div>
                    {run.last_error && (
                      <p className="text-xs text-red-400 mt-1 truncate">{run.last_error}</p>
                    )}
                  </div>

                  {/* Timing */}
                  <div className="text-right">
                    <p className="text-xs text-gray-500">
                      {new Date(run.created_at).toLocaleString()}
                    </p>
                    {run.finished_at && (
                      <p className="text-xs text-gray-600">
                        Duration: {formatDuration(run.started_at, run.finished_at)}
                      </p>
                    )}
                  </div>

                  <span className="text-gray-600 text-sm">
                    {selectedRunId === run.id ? '▼' : '▶'}
                  </span>
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
                    <div className="py-4 text-center text-gray-600 text-sm">Loading...</div>
                  ) : actionLogs.length === 0 ? (
                    <div className="py-4 text-center text-gray-600 text-sm">No action logs</div>
                  ) : (
                    actionLogs.map((log) => {
                      const hasDetail = log.input || log.output || log.error;
                      return (
                        <details
                          key={log.id}
                          className="bg-gray-800/40 border border-gray-700/50 rounded-lg overflow-hidden group/log"
                        >
                          <summary className="flex items-center gap-3 p-3 cursor-pointer select-none hover:bg-gray-800/60 transition-colors list-none">
                            <span
                              className="w-2 h-2 rounded-full flex-shrink-0"
                              style={{ backgroundColor: STATUS_COLORS[log.status] || '#666' }}
                            />
                            <div className="flex-1 min-w-0">
                              <div className="flex items-center gap-2">
                                <span className="text-xs font-medium text-white">
                                  Step {log.action_idx + 1}: {ACTION_LABELS[log.action_type as keyof typeof ACTION_LABELS] || log.action_type}
                                </span>
                                <span className="text-xs text-gray-500">
                                  {log.duration_ms}ms
                                </span>
                                {log.attempt_no > 1 && (
                                  <span className="text-xs text-amber-400">attempt #{log.attempt_no}</span>
                                )}
                              </div>
                              {log.error && (
                                <p className="text-xs text-red-400 mt-0.5 truncate group-open/log:hidden">{log.error}</p>
                              )}
                            </div>
                            <span
                              className="text-xs font-medium"
                              style={{ color: STATUS_COLORS[log.status] || '#666' }}
                            >
                              {log.status}
                            </span>
                            {hasDetail && (
                              <span className="text-gray-600 text-xs transition-transform group-open/log:rotate-90">▶</span>
                            )}
                          </summary>

                          {hasDetail && (
                            <div className="px-3 pb-3 space-y-2 border-t border-gray-700/30 pt-2">
                              {log.input && Object.keys(log.input).length > 0 && (
                                <div>
                                  <p className="text-xs text-gray-500 mb-1 font-medium">Input (resolved params)</p>
                                  <pre className="text-xs font-mono leading-relaxed p-2 bg-gray-900/60 rounded-lg border border-gray-700/30 overflow-x-auto max-h-40 overflow-y-auto">
                                    {syntaxHighlightJSON(JSON.stringify(redactValue(log.input), null, 2))}
                                  </pre>
                                </div>
                              )}
                              {log.output && Object.keys(log.output).length > 0 && (
                                <div>
                                  <p className="text-xs text-gray-500 mb-1 font-medium">Output</p>
                                  <pre className="text-xs font-mono leading-relaxed p-2 bg-gray-900/60 rounded-lg border border-gray-700/30 overflow-x-auto max-h-40 overflow-y-auto">
                                    {syntaxHighlightJSON(JSON.stringify(redactValue(log.output), null, 2))}
                                  </pre>
                                </div>
                              )}
                              {log.error && (
                                <div>
                                  <p className="text-xs text-gray-500 mb-1 font-medium">Error</p>
                                  <pre className="text-xs font-mono text-red-400 p-2 bg-red-500/5 rounded-lg border border-red-500/20 whitespace-pre-wrap">
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
        <div className="flex justify-center gap-2 mt-6">
          <button
            onClick={() => setPage((p) => Math.max(1, p - 1))}
            disabled={page <= 1}
            className="px-3 py-1.5 rounded-lg text-sm bg-gray-800 text-gray-400 hover:text-white disabled:opacity-30"
          >
            ←
          </button>
          <span className="px-3 py-1.5 text-sm text-gray-500">
            Page {page} of {totalPages}
          </span>
          <button
            onClick={() => setPage((p) => Math.min(totalPages, p + 1))}
            disabled={page >= totalPages}
            className="px-3 py-1.5 rounded-lg text-sm bg-gray-800 text-gray-400 hover:text-white disabled:opacity-30"
          >
            →
          </button>
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
          <span key={`t-${i}-${lastIndex}`} className="text-gray-400">
            {highlighted.slice(lastIndex, tagMatch.index)}
          </span>
        );
      }
      const tagType = tagMatch[1];
      const content = tagMatch[2];
      const colorClass =
        tagType === 'k' ? 'text-indigo-400' :
        tagType === 's' ? 'text-emerald-400' :
        tagType === 'n' ? 'text-amber-400' :
        'text-purple-400'; // bool/null
      elements.push(
        <span key={`t-${i}-${tagMatch.index}`} className={colorClass}>
          {content}
        </span>
      );
      lastIndex = tagMatch.index + tagMatch[0].length;
    }

    if (lastIndex < highlighted.length) {
      elements.push(
        <span key={`t-${i}-end`} className="text-gray-400">
          {highlighted.slice(lastIndex)}
        </span>
      );
    }

    parts.push(
      <React.Fragment key={`line-${i}`}>
        {elements.length > 0 ? elements : <span className="text-gray-400">{line}</span>}
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
    <details className="bg-gray-800/40 border border-gray-700/50 rounded-lg overflow-hidden group/ctx">
      <summary className="flex items-center gap-2 px-3 py-2.5 cursor-pointer select-none hover:bg-gray-800/60 transition-colors list-none">
        <span className="text-gray-500 text-xs transition-transform group-open/ctx:rotate-90">▶</span>
        <span className="text-xs font-medium text-gray-300">Trigger Context</span>
        <span className="text-xs text-gray-600 ml-1">
          ({Object.keys(context).length} field{Object.keys(context).length !== 1 ? 's' : ''})
        </span>
        <button
          onClick={handleCopy}
          className={`ml-auto px-2 py-0.5 rounded text-xs transition-colors ${
            copied
              ? 'text-emerald-400 bg-emerald-400/10'
              : 'text-gray-500 hover:text-white hover:bg-gray-700'
          }`}
          title="Copy to clipboard"
        >
          {copied ? '✓ Copied' : '📋 Copy'}
        </button>
      </summary>
      <div className="px-3 pb-3">
        <pre className="text-xs font-mono leading-relaxed overflow-x-auto max-h-80 overflow-y-auto p-3 bg-gray-900/60 rounded-lg border border-gray-700/30">
          {syntaxHighlightJSON(jsonString)}
        </pre>
      </div>
    </details>
  );
};

export default RunHistory;
