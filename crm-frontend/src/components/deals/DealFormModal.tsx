import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createDeal, getContacts, getCompanies, type PipelineStage, type Contact, type Company } from '../../lib/api';
import DynamicCustomFields from '../common/DynamicCustomFields';

interface DealFormModalProps {
  isOpen: boolean;
  onClose: () => void;
  stages: PipelineStage[];
  defaultStageId?: string;
}

export default function DealFormModal({ isOpen, onClose, stages, defaultStageId }: DealFormModalProps) {
  const queryClient = useQueryClient();
  const [title, setTitle] = useState('');
  const [value, setValue] = useState('');
  const [probability, setProbability] = useState(50);
  const [stageId, setStageId] = useState(defaultStageId || '');
  const [contactId, setContactId] = useState('');
  const [companyId, setCompanyId] = useState('');
  const [expectedCloseAt, setExpectedCloseAt] = useState('');
  const [customFields, setCustomFields] = useState<Record<string, unknown>>({});

  const { data: contacts = [] } = useQuery<Contact[]>({
    queryKey: ['contacts-dropdown'],
    queryFn: async () => {
      const res = await getContacts({ limit: 100 });
      return res.contacts;
    },
    enabled: isOpen,
  });

  const { data: companies = [] } = useQuery<Company[]>({
    queryKey: ['companies'],
    queryFn: getCompanies,
    enabled: isOpen,
  });

  const mutation = useMutation({
    mutationFn: createDeal,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['deals'] });
      resetForm();
      onClose();
    },
  });

  const resetForm = () => {
    setTitle('');
    setValue('');
    setProbability(50);
    setStageId(defaultStageId || '');
    setContactId('');
    setCompanyId('');
    setExpectedCloseAt('');
    setCustomFields({});
  };

  const handleSubmit = (e: React.FormEvent) => {
    e.preventDefault();
    mutation.mutate({
      title,
      value: parseFloat(value) || 0,
      probability,
      stage_id: stageId || undefined,
      contact_id: contactId || undefined,
      company_id: companyId || undefined,
      expected_close_at: expectedCloseAt ? new Date(expectedCloseAt).toISOString() : undefined,
    });
  };

  if (!isOpen) return null;

  return (
    <div className="fixed inset-0 z-50 flex items-center justify-center bg-black/50 backdrop-blur-sm animate-in fade-in duration-200">
      <div className="bg-card w-full max-w-lg rounded-2xl shadow-xl overflow-hidden animate-in zoom-in-95 duration-200">
        <form onSubmit={handleSubmit}>
          <div className="p-6">
            <h3 className="text-lg font-semibold mb-4">New Deal</h3>

            <div className="space-y-4">
              {/* Title */}
              <div>
                <label className="text-sm font-medium text-muted-foreground mb-1 block">Deal Title *</label>
                <input
                  value={title}
                  onChange={e => setTitle(e.target.value)}
                  required
                  placeholder="e.g. Website redesign project"
                  className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>

              {/* Value + Probability */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-sm font-medium text-muted-foreground mb-1 block">Value ($)</label>
                  <input
                    type="number"
                    value={value}
                    onChange={e => setValue(e.target.value)}
                    placeholder="0"
                    min="0"
                    className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  />
                </div>
                <div>
                  <label className="text-sm font-medium text-muted-foreground mb-1 block">Probability: {probability}%</label>
                  <input
                    type="range"
                    min="0"
                    max="100"
                    value={probability}
                    onChange={e => setProbability(Number(e.target.value))}
                    className="w-full mt-2 accent-blue-500"
                  />
                </div>
              </div>

              {/* Stage */}
              <div>
                <label className="text-sm font-medium text-muted-foreground mb-1 block">Stage</label>
                <select
                  value={stageId}
                  onChange={e => setStageId(e.target.value)}
                  className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                >
                  <option value="">Select stage</option>
                  {stages.filter(s => !s.is_won && !s.is_lost).map(s => (
                    <option key={s.id} value={s.id}>{s.name}</option>
                  ))}
                </select>
              </div>

              {/* Contact + Company */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <label className="text-sm font-medium text-muted-foreground mb-1 block">Contact</label>
                  <select
                    value={contactId}
                    onChange={e => setContactId(e.target.value)}
                    className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  >
                    <option value="">None</option>
                    {contacts.map(c => (
                      <option key={c.id} value={c.id}>{c.first_name} {c.last_name}</option>
                    ))}
                  </select>
                </div>
                <div>
                  <label className="text-sm font-medium text-muted-foreground mb-1 block">Company</label>
                  <select
                    value={companyId}
                    onChange={e => setCompanyId(e.target.value)}
                    className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                  >
                    <option value="">None</option>
                    {companies.map(c => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                  </select>
                </div>
              </div>

              {/* Expected Close */}
              <div>
                <label className="text-sm font-medium text-muted-foreground mb-1 block">Expected Close Date</label>
                <input
                  type="date"
                  value={expectedCloseAt}
                  onChange={e => setExpectedCloseAt(e.target.value)}
                  className="w-full px-3 py-2 rounded-lg border bg-background text-sm focus:outline-none focus:ring-2 focus:ring-blue-500"
                />
              </div>

              {/* Custom Fields */}
              <DynamicCustomFields
                entityType="deal"
                values={customFields}
                onChange={setCustomFields}
                disabled={mutation.isPending}
              />
            </div>
          </div>

          <div className="px-6 py-4 bg-muted/30 flex justify-end gap-3 border-t">
            <button
              type="button"
              onClick={() => { resetForm(); onClose(); }}
              className="px-4 py-2 text-sm font-medium rounded-lg hover:bg-muted transition-colors"
            >
              Cancel
            </button>
            <button
              type="submit"
              disabled={!title || mutation.isPending}
              className="px-4 py-2 text-sm font-medium rounded-lg bg-blue-600 text-white hover:bg-blue-700 transition-colors disabled:opacity-50 flex items-center gap-2"
            >
              {mutation.isPending && <div className="h-4 w-4 rounded-full border-2 border-white/30 border-t-white animate-spin" />}
              Create Deal
            </button>
          </div>
        </form>
      </div>
    </div>
  );
}
