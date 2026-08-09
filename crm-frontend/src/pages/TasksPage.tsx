import { useState, useEffect } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { useSearchParams } from 'react-router-dom';
import { CheckSquare } from 'lucide-react';
import { getTasksPage, updateTask, type Task, type TaskFilter } from '../lib/api';
import { useAuth } from '../lib/auth';
import { PageHeader } from '../components/ui/page-header';
import { EmptyState } from '../components/ui/empty-state';
import { Skeleton } from '../components/ui/skeleton';
import { Button } from '../components/ui/button';

// R8.1: the first global task view. Three tabs over the same list endpoint —
// "My tasks" (assigned to me, incomplete), "Today" (due today or overdue,
// assigned to me), "Overdue" (past due, assigned to me). Cross-rep visibility
// still runs through the same row-scope predicate as everything else (an
// own-scoped user's "My tasks" and an all-scoped manager's are the same query,
// just different results).
type TabKey = 'mine' | 'today' | 'overdue';

const TABS: { key: TabKey; label: string }[] = [
  { key: 'mine', label: 'My tasks' },
  { key: 'today', label: 'Today' },
  { key: 'overdue', label: 'Overdue' },
];

function startOfTodayISO(): string {
  const d = new Date();
  d.setHours(0, 0, 0, 0);
  return d.toISOString();
}
function endOfTodayISO(): string {
  const d = new Date();
  d.setHours(23, 59, 59, 999);
  return d.toISOString();
}

function buildFilter(tab: TabKey, myUserId: string | undefined, cursor?: string): TaskFilter {
  const base: TaskFilter = { completed: false, limit: 50, cursor };
  if (tab === 'mine') {
    return { ...base, assigned_to: myUserId };
  }
  if (tab === 'today') {
    return { ...base, assigned_to: myUserId, due_after: startOfTodayISO(), due_before: endOfTodayISO() };
  }
  // overdue
  return { ...base, assigned_to: myUserId, due_before: new Date().toISOString() };
}

export default function TasksPage() {
  const { user } = useAuth();
  const queryClient = useQueryClient();
  const [searchParams, setSearchParams] = useSearchParams();
  const tab = (searchParams.get('tab') as TabKey) || 'mine';
  const [pendingIds, setPendingIds] = useState<Record<string, boolean>>({});

  const filter = buildFilter(tab, user?.id);
  const { data, isLoading, error, fetchNextPage, hasNextPage, isFetchingNextPage } = usePagedTasks(filter, tab);

  const toggleMutation = useMutation({
    mutationFn: ({ id, completed }: { id: string; completed: boolean }) => updateTask(id, { completed }),
    onMutate: async ({ id }) => {
      setPendingIds((p) => ({ ...p, [id]: true }));
    },
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks-page'] });
    },
    onSettled: (_data, _err, vars) => {
      setPendingIds((p) => ({ ...p, [vars.id]: false }));
    },
  });

  const tasks = data ?? [];

  return (
    <div className="mx-auto w-full max-w-4xl space-y-6">
      <PageHeader
        className="mb-0"
        title="Tasks"
        description="Tasks assigned to you, across every contact and deal."
      />

      <div className="flex gap-1 border-b border-border">
        {TABS.map((t) => (
          <button
            key={t.key}
            type="button"
            onClick={() => setSearchParams(t.key === 'mine' ? {} : { tab: t.key })}
            className={`px-3 py-2 text-sm font-medium transition-colors ${
              tab === t.key
                ? 'border-b-2 border-primary text-foreground'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {t.label}
          </button>
        ))}
      </div>

      {isLoading ? (
        <Skeleton className="h-40 rounded-xl" />
      ) : error ? (
        <div className="rounded-xl border border-destructive/30 bg-destructive/10 px-4 py-3 text-sm text-destructive">
          {error instanceof Error ? error.message : 'Failed to load tasks.'}
        </div>
      ) : tasks.length === 0 ? (
        <EmptyState icon={CheckSquare} title="Nothing here." description="You're all caught up." />
      ) : (
        <div className="overflow-hidden rounded-xl border border-border">
          {tasks.map((t: Task) => (
            <TaskRow
              key={t.id}
              task={t}
              pending={!!pendingIds[t.id]}
              onToggle={() => toggleMutation.mutate({ id: t.id, completed: !t.completed_at })}
            />
          ))}
        </div>
      )}

      {hasNextPage && (
        <div className="flex justify-center">
          <Button variant="secondary" onClick={() => fetchNextPage()} disabled={isFetchingNextPage}>
            {isFetchingNextPage ? 'Loading…' : 'Load more'}
          </Button>
        </div>
      )}
    </div>
  );
}

function TaskRow({ task, pending, onToggle }: { task: Task; pending: boolean; onToggle: () => void }) {
  const overdue = task.due_at && !task.completed_at && new Date(task.due_at) < new Date();
  return (
    <div className="flex items-center gap-3 border-b border-border px-4 py-3 last:border-0">
      <input
        type="checkbox"
        checked={!!task.completed_at}
        disabled={pending}
        onChange={onToggle}
        className="h-4 w-4 rounded border-border"
        aria-label={`Mark "${task.title}" ${task.completed_at ? 'incomplete' : 'complete'}`}
      />
      <div className="min-w-0 flex-1">
        <div className={`truncate text-sm ${task.completed_at ? 'text-muted-foreground line-through' : 'text-foreground'}`}>
          {task.title}
        </div>
      </div>
      {task.due_at && (
        <span className={`text-xs ${overdue ? 'font-medium text-destructive' : 'text-muted-foreground'}`}>
          {new Date(task.due_at).toLocaleDateString()}
        </span>
      )}
      <span className="text-xs capitalize text-muted-foreground">{task.priority}</span>
    </div>
  );
}

// usePagedTasks accumulates cursor-paged results client-side, resetting when
// the filter identity (tab) changes. A dedicated hook rather than
// useInfiniteQuery so the "reset on tab switch" behavior stays explicit.
function usePagedTasks(filter: TaskFilter, resetKey: string) {
  const [pages, setPages] = useState<{ key: string; tasks: Task[]; cursor: string | null }>({
    key: resetKey,
    tasks: [],
    cursor: null,
  });
  const [isFetchingNextPage, setIsFetchingNextPage] = useState(false);

  const firstPageQuery = useQuery({
    queryKey: ['tasks-page', resetKey, filter.assigned_to, filter.due_after, filter.due_before],
    queryFn: () => getTasksPage({ ...filter, cursor: undefined }),
  });

  // Reset accumulated pages whenever the tab changes or a fresh first page
  // lands (e.g. after a toggle invalidates the query).
  useEffect(() => {
    if (firstPageQuery.data) {
      setPages({ key: resetKey, tasks: firstPageQuery.data.tasks, cursor: firstPageQuery.data.next_cursor });
    }
  }, [resetKey, firstPageQuery.data]);

  const tasks = pages.key === resetKey ? pages.tasks : [];
  const nextCursor = pages.key === resetKey ? pages.cursor : null;

  const fetchNextPage = async () => {
    if (!nextCursor) return;
    setIsFetchingNextPage(true);
    try {
      const page = await getTasksPage({ ...filter, cursor: nextCursor });
      setPages((p) => ({ key: resetKey, tasks: [...p.tasks, ...page.tasks], cursor: page.next_cursor }));
    } finally {
      setIsFetchingNextPage(false);
    }
  };

  return {
    data: tasks,
    isLoading: firstPageQuery.isLoading,
    error: firstPageQuery.error,
    hasNextPage: !!nextCursor,
    isFetchingNextPage,
    fetchNextPage,
  };
}
