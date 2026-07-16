import { useState } from 'react';
import { useMutation, useQueryClient } from '@tanstack/react-query';
import { Plus, Phone, Mail, Handshake, FileText, type LucideIcon } from 'lucide-react';
import { createActivity } from '../../lib/api';
import { Button, Input, Textarea } from '@/components/ui';

interface ActivityFormProps {
  dealId?: string;
  contactId?: string;
}

const ACTIVITY_TYPES: { value: string; label: string; icon: LucideIcon }[] = [
  { value: 'call', label: 'Call', icon: Phone },
  { value: 'email', label: 'Email', icon: Mail },
  { value: 'meeting', label: 'Meeting', icon: Handshake },
  { value: 'note', label: 'Note', icon: FileText },
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
        className="w-full py-2.5 rounded-xl border-2 border-dashed border-muted-foreground/20 text-sm text-muted-foreground hover:border-primary/50 hover:text-primary transition-colors focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring"
      >
        <Plus aria-hidden className="inline h-4 w-4 -mt-0.5 mr-1" /> Log Activity
      </button>
    );
  }

  return (
    <form onSubmit={handleSubmit} className="rounded-xl border bg-card p-4 space-y-3">
      {/* Type selector */}
      <div className="flex gap-1.5">
        {ACTIVITY_TYPES.map(t => {
          const Icon = t.icon;
          return (
            <Button
              key={t.value}
              type="button"
              size="sm"
              variant={type === t.value ? 'default' : 'secondary'}
              onClick={() => setType(t.value)}
            >
              <Icon aria-hidden /> {t.label}
            </Button>
          );
        })}
      </div>

      <Input
        value={title}
        onChange={e => setTitle(e.target.value)}
        placeholder="Title"
        required
      />

      <Textarea
        value={body}
        onChange={e => setBody(e.target.value)}
        placeholder="Notes (optional)"
        rows={2}
        className="resize-none"
      />

      <div className="flex items-center gap-3">
        <Input
          type="number"
          value={duration}
          onChange={e => setDuration(e.target.value)}
          placeholder="Duration (min)"
          min="0"
          className="w-32"
        />
        <div className="flex-1" />
        <Button type="button" variant="ghost" onClick={() => setIsOpen(false)}>
          Cancel
        </Button>
        <Button type="submit" disabled={!title || mutation.isPending}>
          Save
        </Button>
      </div>
    </form>
  );
}
