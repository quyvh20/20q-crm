import { useState } from 'react';
import { useMutation, useQuery, useQueryClient } from '@tanstack/react-query';
import { createDeal, getContacts, getCompanies, type PipelineStage, type Contact, type Company } from '../../lib/api';
import DynamicCustomFields from '../common/DynamicCustomFields';
import Modal from '../common/Modal';
import { Button, Input, Label, Select, Spinner } from '@/components/ui';

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

  const close = () => { resetForm(); onClose(); };

  return (
    // Shared Radix modal (U7): Escape, focus trap + restore and aria come with it.
    // Dismissal is blocked mid-create so a stray Escape can't orphan the request.
    <Modal
      open={isOpen}
      onClose={close}
      title="New Deal"
      size="lg"
      padded={false}
      dismissable={!mutation.isPending}
    >
        <form onSubmit={handleSubmit}>
          <div className="px-6 pb-6">
            <div className="space-y-4">
              {/* Title */}
              <div>
                <Label className="mb-1 text-muted-foreground">Deal Title *</Label>
                <Input
                  value={title}
                  onChange={e => setTitle(e.target.value)}
                  required
                  placeholder="e.g. Website redesign project"
                />
              </div>

              {/* Value + Probability */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label className="mb-1 text-muted-foreground">Value ($)</Label>
                  <Input
                    type="number"
                    value={value}
                    onChange={e => setValue(e.target.value)}
                    placeholder="0"
                    min="0"
                  />
                </div>
                <div>
                  <Label className="mb-1 text-muted-foreground">Probability: {probability}%</Label>
                  <input
                    type="range"
                    min="0"
                    max="100"
                    value={probability}
                    onChange={e => setProbability(Number(e.target.value))}
                    className="w-full mt-2 accent-primary"
                  />
                </div>
              </div>

              {/* Stage */}
              <div>
                <Label className="mb-1 text-muted-foreground">Stage</Label>
                <Select
                  value={stageId}
                  onChange={e => setStageId(e.target.value)}
                >
                  <option value="">Select stage</option>
                  {stages.filter(s => !s.is_won && !s.is_lost).map(s => (
                    <option key={s.id} value={s.id}>{s.name}</option>
                  ))}
                </Select>
              </div>

              {/* Contact + Company */}
              <div className="grid grid-cols-2 gap-3">
                <div>
                  <Label className="mb-1 text-muted-foreground">Contact</Label>
                  <Select
                    value={contactId}
                    onChange={e => setContactId(e.target.value)}
                  >
                    <option value="">None</option>
                    {contacts.map(c => (
                      <option key={c.id} value={c.id}>{c.first_name} {c.last_name}</option>
                    ))}
                  </Select>
                </div>
                <div>
                  <Label className="mb-1 text-muted-foreground">Company</Label>
                  <Select
                    value={companyId}
                    onChange={e => setCompanyId(e.target.value)}
                  >
                    <option value="">None</option>
                    {companies.map(c => (
                      <option key={c.id} value={c.id}>{c.name}</option>
                    ))}
                  </Select>
                </div>
              </div>

              {/* Expected Close */}
              <div>
                <Label className="mb-1 text-muted-foreground">Expected Close Date</Label>
                <Input
                  type="date"
                  value={expectedCloseAt}
                  onChange={e => setExpectedCloseAt(e.target.value)}
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
            <Button type="button" variant="ghost" onClick={close}>
              Cancel
            </Button>
            <Button
              type="submit"
              disabled={!title || mutation.isPending}
            >
              {mutation.isPending && <Spinner size="sm" />}
              Create Deal
            </Button>
          </div>
        </form>
    </Modal>
  );
}
