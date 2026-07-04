import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import {
  addReportComment, deleteReportComment, listReportComments,
  type ReportCommentView,
} from '../../lib/api';

// shortDate renders a comment timestamp compactly (e.g. "Jul 4, 2:15 PM").
function shortDate(iso: string): string {
  const d = new Date(iso);
  if (Number.isNaN(d.getTime())) return '';
  return d.toLocaleString(undefined, { month: 'short', day: 'numeric', hour: 'numeric', minute: '2-digit' });
}

// ReportComments is a report's discussion thread: oldest-first messages with a
// composer for anyone with comment access. The delete affordance shows only on
// rows the server marked can_delete (the author, or a report manager). Read
// access is implied — the parent renders this only for a report the caller can
// already see; canComment gates posting.
export default function ReportComments({ reportId, canComment }: { reportId: string; canComment: boolean }) {
  const queryClient = useQueryClient();
  const [body, setBody] = useState('');

  const { data: comments = [], isLoading, isError, error } = useQuery<ReportCommentView[]>({
    queryKey: ['report-comments', reportId],
    queryFn: () => listReportComments(reportId),
  });

  const invalidate = () => queryClient.invalidateQueries({ queryKey: ['report-comments', reportId] });

  const addMutation = useMutation({
    mutationFn: (text: string) => addReportComment(reportId, text),
    onSuccess: () => { setBody(''); invalidate(); },
  });

  const deleteMutation = useMutation({
    mutationFn: (commentId: string) => deleteReportComment(reportId, commentId),
    onSuccess: invalidate,
  });

  const submit = () => {
    const text = body.trim();
    if (!text) return;
    addMutation.mutate(text);
  };

  return (
    <div className="rounded-xl border bg-card p-4">
      <div className="mb-3 flex items-center justify-between">
        <h3 className="text-sm font-semibold">Comments</h3>
        {comments.length > 0 && <span className="text-xs text-muted-foreground">{comments.length}</span>}
      </div>

      {isError ? (
        <div className="text-sm text-red-600">{(error as Error).message}</div>
      ) : isLoading ? (
        <div className="h-16 animate-pulse rounded-lg bg-muted/50" />
      ) : comments.length === 0 ? (
        <div className="rounded-lg border border-dashed p-4 text-center text-sm text-muted-foreground">No comments yet.</div>
      ) : (
        <ul className="space-y-3">
          {comments.map((c) => (
            <li key={c.id} className="flex gap-2 text-sm">
              <div className="min-w-0 flex-1">
                <div className="flex items-center gap-2">
                  <span className="font-medium">{c.author_name}</span>
                  <span className="text-xs text-muted-foreground">{shortDate(c.created_at)}</span>
                </div>
                <div className="whitespace-pre-wrap break-words text-foreground">{c.body}</div>
              </div>
              {c.can_delete && (
                <button
                  onClick={() => deleteMutation.mutate(c.id)}
                  disabled={deleteMutation.isPending}
                  className="rounded px-1.5 py-1 text-muted-foreground hover:bg-accent hover:text-foreground"
                  aria-label={`Delete comment by ${c.author_name}`}
                >
                  ✕
                </button>
              )}
            </li>
          ))}
        </ul>
      )}

      {canComment ? (
        <div className="mt-3 space-y-2">
          <textarea
            aria-label="Add a comment"
            value={body}
            onChange={(e) => setBody(e.target.value)}
            placeholder="Add a comment…"
            rows={2}
            className="w-full resize-y rounded-md border bg-background px-3 py-2 text-sm"
          />
          {addMutation.isError && <div className="text-sm text-red-600">{(addMutation.error as Error).message}</div>}
          <div className="flex justify-end">
            <button
              onClick={submit}
              disabled={addMutation.isPending || !body.trim()}
              className="rounded-md bg-primary px-3 py-2 text-sm font-medium text-primary-foreground hover:opacity-90 disabled:opacity-50"
            >
              {addMutation.isPending ? 'Posting…' : 'Comment'}
            </button>
          </div>
        </div>
      ) : (
        <div className="mt-3 text-xs text-muted-foreground">You need comment access to post.</div>
      )}
    </div>
  );
}
