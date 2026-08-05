import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, X } from 'lucide-react';
import { getTasks, createTask, updateTask, type Task } from '../../lib/api';

// TaskPanel is the generic "tasks on this record" widget (R8.1). It only
// understands contact_id today — Task has no generic FK, so it can't attach
// to an arbitrary custom object yet (that needs Task joining the object
// registry, deferred per the R8.1 plan). DealDetailPage keeps its own
// deal_id-scoped inline version rather than adopting this one, to avoid
// churning a page that already works.
export default function TaskPanel({ contactId }: { contactId: string }) {
  const queryClient = useQueryClient();
  const queryKey = ['tasks', 'contact', contactId];
  const { data: tasks = [] } = useQuery<Task[]>({
    queryKey,
    queryFn: () => getTasks({ contact_id: contactId }),
  });

  const [showAdd, setShowAdd] = useState(false);
  const [title, setTitle] = useState('');
  const [dueAt, setDueAt] = useState('');
  const [pending, setPending] = useState<Record<string, boolean>>({});

  const createMutation = useMutation({
    mutationFn: () => createTask({
      title,
      contact_id: contactId,
      due_at: dueAt ? new Date(dueAt).toISOString() : undefined,
    }),
    onSuccess: () => {
      setTitle('');
      setDueAt('');
      setShowAdd(false);
      queryClient.invalidateQueries({ queryKey });
    },
  });

  const toggleMutation = useMutation({
    mutationFn: ({ id, completed }: { id: string; completed: boolean }) => updateTask(id, { completed }),
    onMutate: ({ id }) => setPending((p) => ({ ...p, [id]: true })),
    onSettled: (_d, _e, vars) => {
      setPending((p) => ({ ...p, [vars.id]: false }));
      queryClient.invalidateQueries({ queryKey });
    },
  });

  return (
    <div className="mb-6">
      <div className="mb-3 flex items-center justify-between">
        <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground">Tasks</h2>
        <button
          type="button"
          onClick={() => setShowAdd((v) => !v)}
          className="inline-flex items-center gap-1 text-xs font-medium text-primary hover:underline"
        >
          {showAdd ? (<><X aria-hidden size={14} /> Cancel</>) : (<><Plus aria-hidden size={14} /> Add Task</>)}
        </button>
      </div>

      {showAdd && (
        <div className="mb-3 space-y-2 rounded-lg border border-border p-3">
          <input
            type="text"
            placeholder="Task title..."
            value={title}
            onChange={(e) => setTitle(e.target.value)}
            className="w-full rounded-md border border-border bg-background px-2 py-1.5 text-sm"
          />
          <input
            type="date"
            value={dueAt}
            onChange={(e) => setDueAt(e.target.value)}
            className="rounded-md border border-border bg-background px-2 py-1.5 text-sm"
          />
          <div>
            <button
              type="button"
              onClick={() => createMutation.mutate()}
              disabled={!title.trim() || createMutation.isPending}
              className="rounded-md bg-primary px-3 py-1.5 text-xs font-medium text-primary-foreground disabled:opacity-50"
            >
              {createMutation.isPending ? 'Saving...' : 'Create Task'}
            </button>
          </div>
        </div>
      )}

      {tasks.length === 0 && !showAdd ? (
        <p className="text-sm text-muted-foreground">No tasks yet.</p>
      ) : (
        <div className="space-y-1">
          {tasks.map((t) => (
            <label key={t.id} className="flex items-center gap-2 rounded-md px-1 py-1 text-sm hover:bg-accent">
              <input
                type="checkbox"
                checked={!!t.completed_at}
                disabled={!!pending[t.id]}
                onChange={() => toggleMutation.mutate({ id: t.id, completed: !t.completed_at })}
                className="h-4 w-4 rounded border-border"
              />
              <span className={t.completed_at ? 'text-muted-foreground line-through' : ''}>{t.title}</span>
              {t.due_at && (
                <span className="ml-auto text-xs text-muted-foreground">
                  {new Date(t.due_at).toLocaleDateString()}
                </span>
              )}
            </label>
          ))}
        </div>
      )}
    </div>
  );
}
