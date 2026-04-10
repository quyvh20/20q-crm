import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { createActivity } from '../../lib/api';

interface ActivityFormProps {
  dealId?: string;
  contactId?: string;
}

const ACTIVITY_TYPES = [
  { value: 'call', label: 'Call', icon: '📞' },
  { value: 'email', label: 'Email', icon: '✉️' },
  { value: 'meeting', label: 'Meeting', icon: '🤝' },
  { value: 'note', label: 'Note', icon: '📝' },
];

export default function ActivityForm({ dealId, contactId }: ActivityFormProps) {
  const queryClient = useQueryClient();
  const [type, setType] = useState('note');
  const [title, setTitle] = useState('');
  const [body, setBody] = useState('');
  const [duration, setDuration] = useState('');
  const [isOpen, setIsOpen] = useState(false);

  const mutation = useMutation({
    mutationFn: createActivity,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['activities'] });
      setTitle('');
      setBody('');
      setDuration('');
      setIsOpen(false);
    },
  });

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    mutation.mutate({
      type,
      deal_id: dealId,
      contact_id: contactId,
      title,
      body: body || undefined,
      duration_minutes: duration ? parseInt(duration) : undefined,
    });
  };

  if (!isOpen) {
    return (
      <button
        onClick={() => setIsOpen(true)}
        className="w-full py-2.5 rounded-xl border-2 border-dashed border-muted-foreground/20 text-sm text-muted-foreground hover:border-blue-500/50 hover:text-blue-500 transition-colors"
      >
        + Log Activity
      </button>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="rounded-xl border bg-card p-4 space-y-3">
      {/* Type selector */}
      <div className="flex gap-1.5">
        {ACTIVITY_TYPES.map(t => (
          <button
            key={t.value}
            type="button"
            onClick={() => setType(t.value)}
            className={`flex items-center gap-1 px-2.5 py-1.5 rounded-lg text-xs font-medium transition-colors ${
              type === t.value
                ? 'bg-blue-600 text-white'
                : 'bg-muted/50 text-muted-foreground hover:bg-muted'
            }`}
          >
            <span>{t.icon}</span> {t.label}
          </button>
        ))}
      </div>

      <input
        value={title}
        onChange={e => setTitle(e.target.value)}
        placeholder="Title"
        required
        className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
      />

      <textarea
        value={body}
        onChange={e => setBody(e.target.value)}
        placeholder="Notes (optional)"
        rows={2}
        className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500 resize-none"
      />

      <div className="flex items-center gap-3">
        <input
          type="number"
          value={duration}
          onChange={e => setDuration(e.target.value)}
          placeholder="Duration (min)"
          min="0"
          className="w-32 px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
        />
        <div className="flex-1" />
        <button
          type="button"
          onClick={() => setIsOpen(false)}
          className="px-3 py-2 text-sm rounded-lg hover:bg-muted transition-colors"
        >
          Cancel
        </button>
        <button
          type="submit"
          disabled={!title || mutation.isPending}
          className="px-3 py-2 text-sm font-medium rounded-lg bg-blue-600 text-white hover:bg-blue-700 transition-colors disabled:opacity-50"
        >
          Save
        </button>
      </div>
    </form>
  );
}
