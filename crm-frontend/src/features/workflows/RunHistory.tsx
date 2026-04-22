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

              {/* Action logs (expanded) */}
              {selectedRunId === run.id && (
                <div className="ml-8 mt-1 space-y-1 mb-3">
                  {loadingLogs ? (
                    <div className="py-4 text-center text-gray-600 text-sm">Loading...</div>
                  ) : actionLogs.length === 0 ? (
                    <div className="py-4 text-center text-gray-600 text-sm">No action logs</div>
                  ) : (
                    actionLogs.map((log) => (
                      <div
                        key={log.id}
                        className="bg-gray-800/40 border border-gray-700/50 rounded-lg p-3 flex items-center gap-3"
                      >
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
                            <p className="text-xs text-red-400 mt-0.5 truncate">{log.error}</p>
                          )}
                        </div>
                        <span
                          className="text-xs font-medium"
                          style={{ color: STATUS_COLORS[log.status] || '#666' }}
                        >
                          {log.status}
                        </span>
                      </div>
                    ))
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

export default RunHistory;
