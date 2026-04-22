import React, { useEffect, useState, useCallback } from 'react';
import { useNavigate } from 'react-router-dom';
import { getWorkflows, deleteWorkflow, toggleWorkflow } from './api';
import type { Workflow } from './types';
import { TRIGGER_LABELS, STATUS_COLORS } from './types';

export const WorkflowList: React.FC = () => {
  const [workflows, setWorkflows] = useState<Workflow[]>([]);
  const [total, setTotal] = useState(0);
  const [page, setPage] = useState(1);
  const [loading, setLoading] = useState(true);
  const [filterActive, setFilterActive] = useState<boolean | undefined>(undefined);
  const navigate = useNavigate();

  const fetchWorkflows = useCallback(async () => {
    setLoading(true);
    try {
      const resp = await getWorkflows({ active: filterActive, page, size: 20 });
      setWorkflows(resp.workflows || []);
      setTotal(resp.total);
    } catch (e) {
      console.error(e);
    } finally {
      setLoading(false);
    }
  }, [page, filterActive]);

  useEffect(() => {
    fetchWorkflows();
  }, [fetchWorkflows]);

  const handleToggle = async (wf: Workflow) => {
    try {
      const updated = await toggleWorkflow(wf.id);
      setWorkflows((prev) => prev.map((w) => (w.id === wf.id ? updated : w)));
    } catch (e: any) {
      alert(e.message);
    }
  };

  const handleDelete = async (wf: Workflow) => {
    if (!confirm(`Delete "${wf.name}"?`)) return;
    try {
      await deleteWorkflow(wf.id);
      setWorkflows((prev) => prev.filter((w) => w.id !== wf.id));
    } catch (e: any) {
      alert(e.message);
    }
  };

  const totalPages = Math.ceil(total / 20);

  return (
    <div className="max-w-5xl mx-auto py-8 px-4">
      {/* Header */}
      <div className="flex items-center justify-between mb-8">
        <div>
          <h1 className="text-2xl font-bold text-white">Workflow Automations</h1>
          <p className="text-sm text-gray-400 mt-1">Automate repetitive tasks with triggers and actions</p>
        </div>
        <button
          onClick={() => navigate('/workflows/new')}
          className="px-4 py-2.5 rounded-xl bg-gradient-to-r from-indigo-500 to-purple-500 text-white text-sm font-medium hover:from-indigo-600 hover:to-purple-600 transition-all shadow-lg shadow-indigo-500/20"
        >
          + New Workflow
        </button>
      </div>

      {/* Filter */}
      <div className="flex gap-2 mb-6">
        {[
          { label: 'All', value: undefined },
          { label: 'Active', value: true },
          { label: 'Inactive', value: false },
        ].map((opt) => (
          <button
            key={String(opt.value)}
            onClick={() => { setFilterActive(opt.value); setPage(1); }}
            className={`px-3 py-1.5 rounded-lg text-sm font-medium transition-colors ${
              filterActive === opt.value
                ? 'bg-indigo-500 text-white'
                : 'bg-gray-800 text-gray-400 hover:text-white'
            }`}
          >
            {opt.label}
          </button>
        ))}
        <span className="ml-auto text-sm text-gray-500 self-center">{total} workflow{total !== 1 ? 's' : ''}</span>
      </div>

      {/* Table */}
      {loading ? (
        <div className="flex justify-center py-16">
          <div className="w-8 h-8 border-2 border-indigo-500 border-t-transparent rounded-full animate-spin" />
        </div>
      ) : workflows.length === 0 ? (
        <div className="text-center py-16 text-gray-500">
          <p className="text-lg mb-2">No workflows yet</p>
          <p className="text-sm">Create your first automation to get started</p>
        </div>
      ) : (
        <div className="space-y-3">
          {workflows.map((wf) => (
            <div
              key={wf.id}
              className="bg-gray-800/60 border border-gray-700 rounded-xl p-4 hover:border-gray-600 transition-all group"
            >
              <div className="flex items-center gap-4">
                {/* Status dot */}
                <div className="relative">
                  <div
                    className={`w-3 h-3 rounded-full ${wf.is_active ? 'bg-emerald-500' : 'bg-gray-600'}`}
                  />
                  {wf.is_active && (
                    <div className="absolute inset-0 w-3 h-3 rounded-full bg-emerald-500 animate-ping opacity-30" />
                  )}
                </div>

                {/* Info */}
                <div
                  className="flex-1 min-w-0 cursor-pointer"
                  onClick={() => navigate(`/workflows/${wf.id}`)}
                >
                  <h3 className="text-sm font-semibold text-white truncate group-hover:text-indigo-400 transition-colors">
                    {wf.name}
                  </h3>
                  <div className="flex items-center gap-3 mt-1">
                    <span className="text-xs text-gray-500">
                      ⚡ {TRIGGER_LABELS[wf.trigger?.type] || 'Unknown'}
                    </span>
                    <span className="text-xs text-gray-500">
                      v{wf.version}
                    </span>
                    <span className="text-xs text-gray-600">
                      {new Date(wf.updated_at).toLocaleDateString()}
                    </span>
                  </div>
                </div>

                {/* Actions */}
                <div className="flex items-center gap-2 opacity-0 group-hover:opacity-100 transition-opacity">
                  <button
                    onClick={() => navigate(`/workflows/${wf.id}/history`)}
                    className="px-3 py-1.5 rounded-lg text-xs bg-gray-700 text-gray-300 hover:text-white hover:bg-gray-600 transition-colors"
                    title="View run history"
                  >
                    📊 History
                  </button>
                  <button
                    onClick={() => handleToggle(wf)}
                    className={`px-3 py-1.5 rounded-lg text-xs font-medium transition-colors ${
                      wf.is_active
                        ? 'bg-amber-500/10 text-amber-400 hover:bg-amber-500/20'
                        : 'bg-emerald-500/10 text-emerald-400 hover:bg-emerald-500/20'
                    }`}
                  >
                    {wf.is_active ? 'Deactivate' : 'Activate'}
                  </button>
                  <button
                    onClick={() => handleDelete(wf)}
                    className="px-3 py-1.5 rounded-lg text-xs bg-red-500/10 text-red-400 hover:bg-red-500/20 transition-colors"
                  >
                    Delete
                  </button>
                </div>
              </div>
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

export default WorkflowList;
