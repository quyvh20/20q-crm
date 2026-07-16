import { useParams, useNavigate } from 'react-router-dom';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import { getDeal, deleteDeal, getActivities, getStages, changeDealStage, updateDeal, getTasks, createTask, updateTask, getUsers, submitScoreDeal, getAccessToken, type Deal, type Activity, type PipelineStage, type Task, type UserListItem } from '../lib/api';
import ActivityForm from '../components/deals/ActivityForm';
import EmailComposer from '../components/ai/EmailComposer';
import MeetingSummary from '../components/ai/MeetingSummary';
import VoiceUploader from '../components/voice/VoiceUploader';
import VoiceLibrary from '../components/voice/VoiceLibrary';
import ShareRecordModal from '../components/records/ShareRecordModal';
import Modal from '../components/common/Modal';
import { usePermissions } from '../lib/auth';
import { useDocumentTitle } from '../lib/useDocumentTitle';
import { useState, useEffect } from 'react';
import {
  ArrowLeft,
  Phone,
  Mail,
  Handshake,
  FileText,
  GitBranch,
  Trophy,
  HeartCrack,
  ClipboardList,
  Pencil,
  Share2,
  Brain,
  Mic,
  Plus,
  X,
  Calendar,
  AlertTriangle,
  User,
  Check,
  Circle,
  type LucideIcon,
} from 'lucide-react';
import { Badge, Button, Input, Label, Select, Spinner } from '@/components/ui';

const ACTIVITY_ICONS: Record<string, LucideIcon> = {
  call: Phone,
  email: Mail,
  meeting: Handshake,
  note: FileText,
  stage_change: GitBranch,
  won: Trophy,
  lost: HeartCrack,
};

type BadgeVariant = 'default' | 'secondary' | 'outline' | 'destructive' | 'success' | 'warning';

const PRIORITY_VARIANT: Record<string, BadgeVariant> = {
  high: 'destructive',
  medium: 'warning',
  low: 'success',
};

const SENTIMENT_VARIANT: Record<string, BadgeVariant> = {
  positive: 'success',
  neutral: 'warning',
  negative: 'destructive',
};

// Probability status color, routed through Badge semantics (no raw palette).
function probabilityVariant(probability: number): BadgeVariant {
  return probability >= 70 ? 'success' : probability >= 30 ? 'warning' : 'destructive';
}

// ── Edit Deal Modal ─────────────────────────────────────────────
function EditDealModal({ deal, onClose }: { deal: Deal; onClose: () => void }) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState(deal.title);
  const [value, setValue] = useState(String(deal.value));
  const [probability, setProbability] = useState(deal.probability);
  const [closeAt, setCloseAt] = useState(
    deal.expected_close_at ? deal.expected_close_at.slice(0, 10) : ''
  );

  const mutation = useMutation({
    mutationFn: () =>
      updateDeal(deal.id, {
        title,
        value: parseFloat(value) || 0,
        probability,
        expected_close_at: closeAt ? new Date(closeAt).toISOString() : undefined,
      }),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deal', deal.id] });
      queryClient.invalidateQueries({ queryKey: ['deals'] });
      onClose();
    },
  });

  return (
    // Shared Radix modal (U7): Escape, focus trap + restore and aria come with it.
    // padded={false} because the footer action row runs edge-to-edge; dismissal is
    // blocked mid-save so a stray Escape can't orphan the request.
    <Modal
      open
      onClose={onClose}
      title="Edit Deal"
      size="md"
      padded={false}
      dismissable={!mutation.isPending}
    >
      <div className="px-6 pb-6 space-y-5">
        <div className="space-y-1.5">
          <Label htmlFor="edit-deal-title" className="text-xs text-muted-foreground">Title</Label>
          <Input
            id="edit-deal-title"
            value={title}
            onChange={e => setTitle(e.target.value)}
          />
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="edit-deal-value" className="text-xs text-muted-foreground">Value ($)</Label>
          <Input
            id="edit-deal-value"
            type="number" min={0}
            value={value}
            onChange={e => setValue(e.target.value)}
          />
        </div>

        <div className="space-y-1.5">
          <div className="flex items-center justify-between">
            <Label htmlFor="edit-deal-probability" className="text-xs text-muted-foreground">Probability</Label>
            <Badge variant={probabilityVariant(probability)}>{probability}%</Badge>
          </div>
          <input
            id="edit-deal-probability"
            type="range" min={0} max={100}
            value={probability}
            onChange={e => setProbability(Number(e.target.value))}
            className="w-full accent-primary"
          />
          <div className="flex justify-between text-[10px] text-muted-foreground">
            <span>0%</span><span>50%</span><span>100%</span>
          </div>
        </div>

        <div className="space-y-1.5">
          <Label htmlFor="edit-deal-close-date" className="text-xs text-muted-foreground">Expected Close Date</Label>
          <Input
            id="edit-deal-close-date"
            type="date"
            value={closeAt}
            onChange={e => setCloseAt(e.target.value)}
          />
        </div>

        {deal.contact && (
          <div className="space-y-1.5">
            <Label className="text-xs text-muted-foreground">Contact</Label>
            <div className="rounded-lg border bg-muted/20 px-3 py-2 text-sm text-muted-foreground">
              {deal.contact.first_name} {deal.contact.last_name}
              {deal.contact.email && ` · ${deal.contact.email}`}
            </div>
          </div>
        )}

        <div className="rounded-lg bg-primary/10 px-4 py-2.5 flex items-center justify-between">
          <span className="text-xs text-muted-foreground">Expected Revenue preview</span>
          <span className="text-sm font-bold text-primary">
            ${Math.round((parseFloat(value) || 0) * probability / 100).toLocaleString()}
          </span>
        </div>
      </div>

      <div className="px-6 py-4 bg-muted/30 flex justify-end gap-3 border-t">
        <Button variant="ghost" onClick={onClose}>Cancel</Button>
        <Button
          id="edit-deal-save"
          onClick={() => mutation.mutate()}
          disabled={mutation.isPending || !title.trim()}
        >
          {mutation.isPending ? 'Saving…' : 'Save Changes'}
        </Button>
      </div>
    </Modal>
  );
}

export default function DealDetailPage() {
  const { id } = useParams<{ id: string }>();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  // OLS-aware buttons (U3.7): Edit/Won/Lost/Delete hide for roles whose deal
  // access lacks edit/delete, instead of 403ing on click. Fails open while
  // permissions load; the server enforces every action regardless.
  const { canAccess } = usePermissions();
  const canEditDeal = canAccess('deal', 'edit');
  const canDeleteDeal = canAccess('deal', 'delete');
  const [showDelete, setShowDelete] = useState(false);
  const [showEdit, setShowEdit] = useState(false);
  // U6: a deal is a shareable record like any other — the deal page just has its
  // own bespoke chrome, so it needs its own Share entry point.
  const [showShare, setShowShare] = useState(false);
  const [showAddTask, setShowAddTask] = useState(false);
  const [newTaskTitle, setNewTaskTitle] = useState('');
  const [newTaskDue, setNewTaskDue] = useState('');
  const [newTaskPriority, setNewTaskPriority] = useState('medium');
  const [newTaskAssignee, setNewTaskAssignee] = useState('');
  const [pendingTasks, setPendingTasks] = useState<Record<string, boolean>>({});

  // AI State
  const [showEmailComposer, setShowEmailComposer] = useState(false);
  const [showMeetingSummary, setShowMeetingSummary] = useState(false);
  const [scoreJobId, setScoreJobId] = useState<string | null>(null);
  const [scoreStatus, setScoreStatus] = useState<'idle' | 'processing' | 'done' | 'error'>('idle');
  const [dealScore, setDealScore] = useState<any>(null);

  // SSE for Deal Score Job
  useEffect(() => {
    if (!scoreJobId || scoreStatus !== 'processing') return;

    const token = getAccessToken();
    if (!token) return;

    const API_BASE = (import.meta as any).env?.VITE_API_URL ?? ((import.meta as any).env?.DEV ? 'http://localhost:8080' : '');
    const abort = new AbortController();

    const pullEvents = async () => {
      try {
        const response = await fetch(`${API_BASE}/api/events`, {
          headers: { 'Authorization': `Bearer ${token}`, 'Accept': 'text/event-stream' },
          credentials: 'include',
          signal: abort.signal
        });

        if (!response.ok) throw new Error('SSE failed');
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
                if (data.type === 'job_complete' && data.job_id === scoreJobId) {
                  if (data.status === 'completed') {
                    setDealScore(data.result);
                    setScoreStatus('done');
                  } else {
                    setScoreStatus('error');
                  }
                  abort.abort();
                  return;
                }
              } catch (e) {}
            }
          }
        }
      } catch (e: any) {
        if (e.name !== 'AbortError') setScoreStatus('error');
      }
    };
    pullEvents();
    return () => abort.abort();
  }, [scoreJobId, scoreStatus]);

  const handleScoreDeal = async () => {
    if (!deal) return;
    setScoreStatus('processing');
    try {
      const res = await submitScoreDeal(deal.id) as any;
      if (res.status === 'completed') {
        setDealScore(res.result);
        setScoreStatus('done');
        return;
      }
      setScoreJobId(res.job_id);
    } catch {
      setScoreStatus('error');
    }
  };

  const { data: deal, isLoading } = useQuery<Deal>({
    queryKey: ['deal', id],
    queryFn: () => getDeal(id!),
    enabled: !!id,
  });

  // Tab title from the SAVED deal (U7.2) — the react-query record, never the
  // EditDealModal's bound `title` input, which would retitle the tab on every
  // keystroke. Undefined while loading ⇒ the bare app name, not "undefined".
  useDocumentTitle(deal?.title);

  const { data: activities = [] } = useQuery<Activity[]>({
    queryKey: ['activities', id],
    queryFn: () => getActivities({ deal_id: id }),
    enabled: !!id,
  });

  const { data: stages = [] } = useQuery<PipelineStage[]>({
    queryKey: ['stages'],
    queryFn: getStages,
  });

  const { data: tasks = [] } = useQuery<Task[]>({
    queryKey: ['tasks', id],
    queryFn: () => getTasks({ deal_id: id }),
    enabled: !!id,
  });

  const { data: users = [] } = useQuery<UserListItem[]>({
    queryKey: ['users'],
    queryFn: getUsers,
  });

  const createTaskMutation = useMutation({
    mutationFn: () => createTask({
      title: newTaskTitle,
      deal_id: id,
      due_at: newTaskDue ? new Date(newTaskDue).toISOString() : undefined,
      priority: newTaskPriority,
      assigned_to: newTaskAssignee || undefined,
    }),
    onMutate: async () => {
      await queryClient.cancelQueries({ queryKey: ['tasks', id] });
      const previousTasks = queryClient.getQueryData<Task[]>(['tasks', id]);

      const fakeTask: Task = {
        id: `temp-${Date.now()}`,
        org_id: 'temp',
        title: newTaskTitle,
        deal_id: id,
        assigned_to: newTaskAssignee || undefined,
        due_at: newTaskDue ? new Date(newTaskDue).toISOString() : undefined,
        priority: newTaskPriority,
        created_at: new Date().toISOString(),
        updated_at: new Date().toISOString(),
      };

      if (previousTasks) {
        queryClient.setQueryData<Task[]>(['tasks', id], [fakeTask, ...previousTasks]);
      }

      // Optimistically close form and reset state instantly
      setNewTaskTitle('');
      setNewTaskDue('');
      setNewTaskPriority('medium');
      setNewTaskAssignee('');
      setShowAddTask(false);

      return { previousTasks };
    },
    onError: (_err, _variables, context) => {
      if (context?.previousTasks) {
        queryClient.setQueryData(['tasks', id], context.previousTasks);
      }
    },
    onSettled: () => {
      queryClient.invalidateQueries({ queryKey: ['tasks', id] });
    },
  });

  const toggleTaskMutation = useMutation({
    mutationFn: ({ taskId, completed }: { taskId: string; completed: boolean }) =>
      updateTask(taskId, { completed }),
    onMutate: async ({ taskId, completed }) => {
      setPendingTasks(prev => ({ ...prev, [taskId]: true }));
      // Cancel any outgoing refetches so they don't overwrite optimistic update
      await queryClient.cancelQueries({ queryKey: ['tasks', id] });
      
      // Snapshot previous value
      const previousTasks = queryClient.getQueryData<Task[]>(['tasks', id]);
      
      // Optimistically update to new value
      if (previousTasks) {
        queryClient.setQueryData<Task[]>(['tasks', id], prev => 
          prev?.map(task => 
            task.id === taskId 
              ? { ...task, completed_at: completed ? new Date().toISOString() : undefined }
              : task
          )
        );
      }
      
      return { previousTasks };
    },
    onError: (_err, _vars, context) => {
      // Rolling back if it fails
      if (context?.previousTasks) {
        queryClient.setQueryData(['tasks', id], context.previousTasks);
      }
    },
    onSettled: (_data, _err, { taskId }) => {
      setPendingTasks(prev => ({ ...prev, [taskId]: false }));
      // Refresh to ensure server sync
      queryClient.invalidateQueries({ queryKey: ['tasks', id] });
    },
  });

  const stageChangeMutation = useMutation({
    mutationFn: ({ stageId }: { stageId: string }) => changeDealStage(id!, stageId),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deal', id] });
      queryClient.invalidateQueries({ queryKey: ['activities', id] });
      queryClient.invalidateQueries({ queryKey: ['deals'] });
    },
  });

  const deleteMutation = useMutation({
    mutationFn: () => deleteDeal(id!),
    onSuccess: () => navigate('/deals'),
  });

  const wonStage = stages.find(s => s.is_won);
  const lostStage = stages.find(s => s.is_lost);

  if (isLoading || !deal) {
    return (
      <div className="max-w-6xl mx-auto space-y-4">
        <div className="h-8 w-48 rounded-lg bg-muted/50 animate-pulse" />
        <div className="h-96 rounded-xl bg-muted/30 animate-pulse" />
      </div>
    );
  }

  return (
    <div className="max-w-6xl mx-auto">
      {/* Back button */}
      <button
        onClick={() => navigate('/deals')}
        className="flex items-center gap-1 text-sm text-muted-foreground hover:text-foreground transition-colors mb-4 rounded-md focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <ArrowLeft aria-hidden className="h-3.5 w-3.5" />
        Back to deals
      </button>

      <div className="grid grid-cols-1 lg:grid-cols-5 gap-6">
        {/* Left Panel: Deal Info (3 cols) */}
        <div className="lg:col-span-3 space-y-4">
          {/* Title + Status */}
          <div className="rounded-xl border bg-card p-6">
            <div className="flex items-start justify-between mb-4">
              <div>
                <div className="flex flex-wrap items-center gap-3">
                  <h1 className="text-2xl font-bold">{deal.title}</h1>
                  {canEditDeal && (
                    <Button
                      id="edit-deal-btn"
                      variant="outline"
                      size="sm"
                      onClick={() => setShowEdit(true)}
                      title="Edit deal"
                    >
                      <Pencil aria-hidden /> Edit
                    </Button>
                  )}
                  <Button
                    id="deal-share-btn"
                    variant="outline"
                    size="sm"
                    onClick={() => setShowShare(true)}
                    title="Share deal"
                  >
                    <Share2 aria-hidden /> Share
                  </Button>
                </div>
                {deal.contact && (
                  <p className="text-sm text-muted-foreground mt-1">
                    Contact: {deal.contact.first_name} {deal.contact.last_name}
                    {deal.contact.email && ` · ${deal.contact.email}`}
                  </p>
                )}
                {deal.company && (
                  <p className="text-sm text-muted-foreground">
                    Company: {deal.company.name}
                  </p>
                )}
                
                {/* AI Actions Row */}
                <div className="flex flex-wrap gap-2 mt-4">
                  <Button
                    variant="outline"
                    size="sm"
                    onClick={handleScoreDeal}
                    disabled={scoreStatus === 'processing'}
                  >
                    {scoreStatus === 'processing' ? (
                      <><Spinner size="sm" /> Scoring...</>
                    ) : (
                      <><Brain aria-hidden /> Score Deal</>
                    )}
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => setShowEmailComposer(true)}>
                    <Mail aria-hidden /> Draft Email
                  </Button>
                  <Button variant="outline" size="sm" onClick={() => setShowMeetingSummary(true)}>
                    <Mic aria-hidden /> Summarize Meeting
                  </Button>
                </div>
              </div>
              <div className="flex flex-col items-end gap-2">
                {deal.is_won && (
                  <Badge variant="success" className="font-bold">WON</Badge>
                )}
                {deal.is_lost && (
                  <Badge variant="destructive" className="font-bold">LOST</Badge>
                )}
                {!deal.is_won && !deal.is_lost && deal.stage && (
                  <span
                    className="px-3 py-1 rounded-full text-xs font-bold text-white"
                    style={{ backgroundColor: deal.stage.color }}
                  >
                    {deal.stage.name}
                  </span>
                )}
              </div>
            </div>

            {/* AI Deal Score Widget */}
            {scoreStatus === 'error' && (
              <div className="mb-6 p-4 rounded-xl bg-destructive/10 border border-destructive/20 text-destructive text-sm flex items-center gap-2">
                <AlertTriangle aria-hidden className="h-4 w-4 shrink-0" />
                Failed to calculate deal score. Please try again later.
              </div>
            )}
            {dealScore && scoreStatus === 'done' && (
              <div className="mb-6 p-5 rounded-xl border bg-primary/5 animate-in fade-in slide-in-from-top-4">
                <div className="flex items-start gap-5">
                  <div className="flex flex-col items-center justify-center h-20 w-20 rounded-full border-4 border-primary shrink-0 bg-background text-primary">
                    <span className="text-2xl font-bold">{dealScore.score}</span>
                    <span className="text-[10px] uppercase font-semibold">Score</span>
                  </div>
                  <div className="flex-1">
                    <h3 className="font-semibold text-foreground mb-2 border-b border-border pb-1">AI Recommendation</h3>
                    <p className="text-sm text-foreground mb-3">{dealScore.recommendation}</p>
                    <div className="grid grid-cols-2 gap-3 text-xs">
                      <div>
                        <Badge variant="success" className="mb-1">Positives</Badge>
                        <ul className="space-y-1">
                          {dealScore.factors.filter((f: string) => f.startsWith('+')).map((f: string, i: number) => (
                            <li key={i} className="flex gap-1.5 items-start text-muted-foreground"><Check aria-hidden className="h-3 w-3 mt-0.5 shrink-0" /> <span>{f.substring(1).trim()}</span></li>
                          ))}
                        </ul>
                      </div>
                      <div>
                        <Badge variant="destructive" className="mb-1">Risks</Badge>
                        <ul className="space-y-1">
                          {dealScore.factors.filter((f: string) => f.startsWith('-')).map((f: string, i: number) => (
                            <li key={i} className="flex gap-1.5 items-start text-muted-foreground"><X aria-hidden className="h-3 w-3 mt-0.5 shrink-0" /> <span>{f.substring(1).trim()}</span></li>
                          ))}
                        </ul>
                      </div>
                    </div>
                  </div>
                </div>
              </div>
            )}

            {/* Value + Probability */}
            <div className="grid grid-cols-3 gap-4 mb-6">
              <div className="rounded-lg bg-muted/30 p-3">
                <p className="text-xs text-muted-foreground mb-1">Value</p>
                <p className="text-xl font-bold">${deal.value.toLocaleString()}</p>
              </div>
              <div className="rounded-lg bg-muted/30 p-3">
                <p className="text-xs text-muted-foreground mb-1">Probability</p>
                <p className="text-xl font-bold">{deal.probability}%</p>
              </div>
              <div className="rounded-lg bg-muted/30 p-3">
                <p className="text-xs text-muted-foreground mb-1">Expected Revenue</p>
                <p className="text-xl font-bold">${Math.round(deal.value * deal.probability / 100).toLocaleString()}</p>
              </div>
            </div>

            {deal.expected_close_at && (
              <p className="text-sm text-muted-foreground mb-4">
                Expected close: {new Date(deal.expected_close_at).toLocaleDateString()}
              </p>
            )}

            {/* Stage Selector — a stage move IS a deal edit, so it's gated on the
                same OLS bit as Mark Won/Lost below. Read-only roles keep the
                information (current stage as a static pill), not the buttons. */}
            {!deal.is_won && !deal.is_lost && (
              <div className="mb-4">
                <label className="text-xs font-medium text-muted-foreground mb-2 block">
                  {canEditDeal ? 'Move to stage' : 'Stage'}
                </label>
                {canEditDeal ? (
                  <div className="flex flex-wrap gap-1.5">
                    {stages.filter(s => !s.is_won && !s.is_lost).map(s => (
                      <Button
                        key={s.id}
                        size="sm"
                        variant={deal.stage_id === s.id ? 'default' : 'secondary'}
                        onClick={() => stageChangeMutation.mutate({ stageId: s.id })}
                        disabled={deal.stage_id === s.id || stageChangeMutation.isPending}
                      >
                        {s.name}
                      </Button>
                    ))}
                  </div>
                ) : (
                  <span
                    className={`inline-block px-3 py-1.5 rounded-lg text-xs font-medium text-white ${deal.stage?.color ? '' : 'bg-muted-foreground'}`}
                    style={deal.stage?.color ? { backgroundColor: deal.stage.color } : undefined}
                  >
                    {deal.stage?.name || '—'}
                  </span>
                )}
              </div>
            )}

            {/* Won / Lost / Delete Actions */}
            {!deal.is_won && !deal.is_lost && (canEditDeal || canDeleteDeal) && (
              <div className="flex gap-2 pt-4 border-t">
                {wonStage && canEditDeal && (
                  <Button
                    onClick={() => stageChangeMutation.mutate({ stageId: wonStage.id })}
                    disabled={stageChangeMutation.isPending}
                  >
                    <Trophy aria-hidden /> Mark Won
                  </Button>
                )}
                {lostStage && canEditDeal && (
                  <Button
                    variant="destructive"
                    onClick={() => stageChangeMutation.mutate({ stageId: lostStage.id })}
                    disabled={stageChangeMutation.isPending}
                  >
                    <HeartCrack aria-hidden /> Mark Lost
                  </Button>
                )}
                <div className="flex-1" />
                {canDeleteDeal && (
                  <Button
                    variant="ghost"
                    onClick={() => setShowDelete(true)}
                    className="text-destructive hover:bg-destructive/10 hover:text-destructive"
                  >
                    Delete
                  </Button>
                )}
              </div>
            )}
          </div>

          {/* Tasks Section */}
          <div className="rounded-xl border bg-card p-6">
            <div className="flex items-center justify-between mb-4">
              <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground">Tasks</h2>
              <Button
                id="add-task-btn"
                variant="outline"
                size="sm"
                onClick={() => setShowAddTask(!showAddTask)}
              >
                {showAddTask ? (<><X aria-hidden /> Cancel</>) : (<><Plus aria-hidden /> Add Task</>)}
              </Button>
            </div>

            {/* Add Task Form */}
            {showAddTask && (
              <div className="mb-4 p-4 rounded-lg border bg-muted/20 space-y-3">
                <Input
                  id="new-task-title"
                  placeholder="Task title..."
                  value={newTaskTitle}
                  onChange={e => setNewTaskTitle(e.target.value)}
                />
                <div className="grid grid-cols-3 gap-2">
                  <div>
                    <Label htmlFor="new-task-due" className="mb-1 block text-[10px] text-muted-foreground">Due Date</Label>
                    <Input
                      id="new-task-due"
                      type="date"
                      value={newTaskDue}
                      onChange={e => setNewTaskDue(e.target.value)}
                    />
                  </div>
                  <div>
                    <Label htmlFor="new-task-priority" className="mb-1 block text-[10px] text-muted-foreground">Priority</Label>
                    <Select
                      id="new-task-priority"
                      value={newTaskPriority}
                      onChange={e => setNewTaskPriority(e.target.value)}
                    >
                      <option value="low">Low</option>
                      <option value="medium">Medium</option>
                      <option value="high">High</option>
                    </Select>
                  </div>
                  <div>
                    <Label htmlFor="new-task-assignee" className="mb-1 block text-[10px] text-muted-foreground">Assignee</Label>
                    <Select
                      id="new-task-assignee"
                      value={newTaskAssignee}
                      onChange={e => setNewTaskAssignee(e.target.value)}
                    >
                      <option value="">Unassigned</option>
                      {users.map(u => (
                        <option key={u.id} value={u.id}>{u.first_name} {u.last_name}</option>
                      ))}
                    </Select>
                  </div>
                </div>
                <Button
                  id="save-task-btn"
                  size="sm"
                  onClick={() => createTaskMutation.mutate()}
                  disabled={!newTaskTitle.trim() || createTaskMutation.isPending}
                >
                  {createTaskMutation.isPending ? 'Saving...' : 'Create Task'}
                </Button>
              </div>
            )}

            {/* Task List */}
            <div className="space-y-2">
              {tasks.length === 0 && !showAddTask && (
                <p className="text-sm text-muted-foreground text-center py-4">No tasks yet</p>
              )}
              {tasks.map(task => {
                const assignee = users.find(u => u.id === task.assigned_to);
                const isOverdue = task.due_at && !task.completed_at && new Date(task.due_at) < new Date();
                return (
                  <div
                    key={task.id}
                    className={`flex items-start justify-between gap-3 p-3 rounded-lg border transition-colors ${
                      task.completed_at ? 'bg-muted/20 opacity-60' : 'bg-card hover:bg-muted/30'
                    }`}
                  >
                    <div className="flex items-start gap-3 flex-1 min-w-0">
                      <input
                        type="checkbox"
                        checked={!!task.completed_at}
                        onChange={() => toggleTaskMutation.mutate({ taskId: task.id, completed: !task.completed_at })}
                        className="h-4 w-4 mt-0.5 rounded border-2 accent-primary shrink-0 cursor-pointer"
                      />
                      <div className="flex-1 min-w-0">
                        <p className={`text-sm font-medium ${task.completed_at ? 'line-through text-muted-foreground' : ''}`}>
                          {task.title}
                        </p>
                        <div className="flex items-center gap-2 mt-1 flex-wrap">
                          <Badge variant={PRIORITY_VARIANT[task.priority] ?? 'warning'} className="rounded px-1.5 py-0.5 text-[10px] capitalize">
                            {task.priority}
                          </Badge>
                          {task.due_at && (
                            <span className={`inline-flex items-center gap-1 text-[10px] ${isOverdue ? 'text-destructive font-bold' : 'text-muted-foreground'}`}>
                              {isOverdue ? <AlertTriangle aria-hidden className="h-3 w-3" /> : <Calendar aria-hidden className="h-3 w-3" />}
                              {new Date(task.due_at).toLocaleDateString()}
                            </span>
                          )}
                          {assignee && (
                            <span className="inline-flex items-center gap-1 text-[10px] text-muted-foreground">
                              <User aria-hidden className="h-3 w-3" /> {assignee.first_name} {assignee.last_name}
                            </span>
                          )}
                        </div>
                      </div>
                    </div>
                    {pendingTasks[task.id] && (
                      <div className="shrink-0 flex items-center justify-center p-1">
                        <Spinner size="sm" />
                      </div>
                    )}
                  </div>
                );
              })}
            </div>
          </div>
        </div>

        {/* Right Panel: Activity Timeline (2 cols) */}
        <div className="lg:col-span-2 space-y-4">
          <h2 className="text-sm font-semibold uppercase tracking-wider text-muted-foreground">Activity Timeline</h2>

          <ActivityForm dealId={id} />

          <div className="space-y-3">
            {activities.length === 0 && (
              <p className="text-sm text-muted-foreground text-center py-8">No activities yet</p>
            )}
            {activities.map(a => {
              const ActivityIcon = ACTIVITY_ICONS[a.type] || ClipboardList;
              return (
              <div key={a.id} className="flex gap-3 items-start">
                <div className="shrink-0 h-8 w-8 rounded-full bg-muted/50 flex items-center justify-center text-muted-foreground">
                  <ActivityIcon aria-hidden className="h-4 w-4" />
                </div>
                <div className="flex-1 min-w-0">
                  <p className="text-sm font-medium">{a.title || a.type}</p>
                  {a.body && (
                    <p className="text-xs text-muted-foreground mt-0.5 line-clamp-2">{a.body}</p>
                  )}
                  <div className="flex items-center gap-2 mt-1">
                    <span className="text-[10px] text-muted-foreground">
                      {new Date(a.occurred_at).toLocaleDateString()} {new Date(a.occurred_at).toLocaleTimeString([], { hour: '2-digit', minute: '2-digit' })}
                    </span>
                    {a.duration_minutes && (
                      <span className="text-[10px] text-muted-foreground">· {a.duration_minutes}min</span>
                    )}
                    <span className="text-[10px] px-1.5 py-0.5 rounded bg-muted/50 text-muted-foreground capitalize">{a.type}</span>
                    {a.sentiment && (
                      <Badge
                        variant={SENTIMENT_VARIANT[a.sentiment] ?? 'outline'}
                        title={`Sentiment: ${a.sentiment}`}
                        className="h-4 w-4 justify-center p-0"
                      >
                        <Circle aria-hidden className="h-2 w-2 fill-current" />
                      </Badge>
                    )}
                  </div>
                </div>
              </div>
              );
            })}
          </div>

          {/* Voice Notes section */}
          <div className="rounded-xl border bg-card p-5">
            <h2 className="flex items-center gap-1.5 text-sm font-semibold uppercase tracking-wider text-muted-foreground mb-4"><Mic aria-hidden className="h-3.5 w-3.5" /> Voice Notes</h2>

            {/* Record / Upload mini-tabs */}
            <DealVoiceTabs dealId={id!} />

          </div>
        </div>
      </div>

      {/* Edit modal */}
      {showEdit && deal && <EditDealModal deal={deal} onClose={() => setShowEdit(false)} />}

      {/* Share modal (U6) — users, roles and groups at view/edit */}
      {showShare && deal && (
        <ShareRecordModal
          slug="deal"
          recordId={deal.id}
          recordName={deal.title}
          onClose={() => setShowShare(false)}
        />
      )}

      {/* Delete confirmation modal — hideClose like ConfirmDialog: Cancel and
          Delete are the only exits, so a third one would just be noise. */}
      {showDelete && (
        <Modal
          open
          onClose={() => setShowDelete(false)}
          title="Delete Deal"
          size="md"
          padded={false}
          hideClose
          dismissable={!deleteMutation.isPending}
        >
          <div className="px-6 pb-6">
            <p className="text-muted-foreground text-sm">
              Are you sure you want to delete "{deal.title}"? This cannot be undone.
            </p>
          </div>
          <div className="px-6 py-4 bg-muted/30 flex justify-end gap-3 border-t">
            <Button variant="ghost" onClick={() => setShowDelete(false)}>
              Cancel
            </Button>
            <Button
              variant="destructive"
              onClick={() => deleteMutation.mutate()}
              disabled={deleteMutation.isPending}
            >
              Delete
            </Button>
          </div>
        </Modal>
      )}

      {/* AI Modals */}
      {showEmailComposer && deal && (
        <EmailComposer
          dealId={deal.id}
          contactId={deal.contact_id}
          contactName={deal.contact ? `${deal.contact.first_name} ${deal.contact.last_name}` : undefined}
          onClose={() => setShowEmailComposer(false)}
        />
      )}

      {showMeetingSummary && deal && (
        <MeetingSummary
          dealId={deal.id}
          contactId={deal.contact_id}
          onClose={() => setShowMeetingSummary(false)}
          onTasksCreated={() => queryClient.invalidateQueries({ queryKey: ['tasks', id] })}
        />
      )}
    </div>
  );
}

/* ── DealVoiceTabs — self-contained mini input switcher ── */
function DealVoiceTabs({ dealId }: { dealId: string }) {
  return (
    <div className="space-y-4">
      <div className="mb-2">
        <VoiceUploader initialDealId={dealId} />
      </div>
      <VoiceLibrary dealId={dealId} />
    </div>
  );
}
