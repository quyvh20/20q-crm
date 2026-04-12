import { useState } from 'react';
import { useQuery, useMutation, useQueryClient } from '@tanstack/react-query';
import {
  getFieldDefs,
  createFieldDef,
  updateFieldDef,
  deleteFieldDef,
  type CustomFieldDef,
} from '../../lib/api';

const ENTITY_TYPES = [
  { value: 'contact', label: 'Contacts' },
  { value: 'company', label: 'Companies' },
  { value: 'deal', label: 'Deals' },
] as const;

const FIELD_TYPES = [
  { value: 'text', label: 'Text', icon: 'Aa' },
  { value: 'number', label: 'Number', icon: '#' },
  { value: 'date', label: 'Date', icon: '📅' },
  { value: 'select', label: 'Select', icon: '▼' },
  { value: 'boolean', label: 'Yes/No', icon: '✓' },
  { value: 'url', label: 'URL', icon: '🔗' },
] as const;

interface FieldForm {
  key: string;
  label: string;
  type: string;
  entity_type: string;
  options: string[];
  required: boolean;
}

const emptyForm: FieldForm = {
  key: '',
  label: '',
  type: 'text',
  entity_type: 'contact',
  options: [],
  required: false,
};

export default function CustomFieldManager() {
  const queryClient = useQueryClient();
  const [activeEntity, setActiveEntity] = useState<string>('contact');
  const [showForm, setShowForm] = useState(false);
  const [editingKey, setEditingKey] = useState<string | null>(null);
  const [form, setForm] = useState<FieldForm>({ ...emptyForm });
  const [optionInput, setOptionInput] = useState('');
  const [deleteConfirm, setDeleteConfirm] = useState<string | null>(null);

  const { data: fieldDefs = [], isLoading } = useQuery({
    queryKey: ['field-defs', activeEntity],
    queryFn: () => getFieldDefs(activeEntity),
  });

  const createMutation = useMutation({
    mutationFn: createFieldDef,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['field-defs'] });
      resetForm();
    },
  });

  const updateMutation = useMutation({
    mutationFn: ({ key, data }: { key: string; data: Parameters<typeof updateFieldDef>[1] }) =>
      updateFieldDef(key, data),
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['field-defs'] });
      resetForm();
    },
  });

  const deleteMutation = useMutation({
    mutationFn: deleteFieldDef,
    onSuccess: () => {
      queryClient.invalidateQueries({ queryKey: ['field-defs'] });
      setDeleteConfirm(null);
    },
  });

  function resetForm() {
    setForm({ ...emptyForm, entity_type: activeEntity });
    setEditingKey(null);
    setShowForm(false);
    setOptionInput('');
  }

  function openCreateForm() {
    setForm({ ...emptyForm, entity_type: activeEntity });
    setEditingKey(null);
    setShowForm(true);
  }

  function openEditForm(def: CustomFieldDef) {
    setForm({
      key: def.key,
      label: def.label,
      type: def.type,
      entity_type: def.entity_type,
      options: def.options || [],
      required: def.required,
    });
    setEditingKey(def.key);
    setShowForm(true);
  }

  function handleSubmit(e: React.FormEvent) {
    e.preventDefault();
    if (editingKey) {
      updateMutation.mutate({
        key: editingKey,
        data: {
          label: form.label,
          type: form.type,
          options: form.type === 'select' ? form.options : undefined,
          required: form.required,
        },
      });
    } else {
      createMutation.mutate({
        key: form.key,
        label: form.label,
        type: form.type,
        entity_type: form.entity_type,
        options: form.type === 'select' ? form.options : undefined,
        required: form.required,
      });
    }
  }

  function addOption() {
    const trimmed = optionInput.trim();
    if (trimmed && !form.options.includes(trimmed)) {
      setForm({ ...form, options: [...form.options, trimmed] });
      setOptionInput('');
    }
  }

  function removeOption(opt: string) {
    setForm({ ...form, options: form.options.filter((o) => o !== opt) });
  }

  // Auto-generate key from label
  function handleLabelChange(label: string) {
    const key = label
      .toLowerCase()
      .replace(/[^a-z0-9\s]/g, '')
      .replace(/\s+/g, '_')
      .slice(0, 64);
    setForm({ ...form, label, ...(editingKey ? {} : { key }) });
  }

  const error = createMutation.error || updateMutation.error || deleteMutation.error;

  return (
    <div className="space-y-6">
      {/* Entity type tabs */}
      <div className="flex gap-1 bg-accent/50 rounded-lg p-1">
        {ENTITY_TYPES.map((et) => (
          <button
            key={et.value}
            onClick={() => { setActiveEntity(et.value); resetForm(); }}
            className={`flex-1 px-4 py-2 rounded-md text-sm font-medium transition-all ${
              activeEntity === et.value
                ? 'bg-card text-foreground shadow-sm'
                : 'text-muted-foreground hover:text-foreground'
            }`}
          >
            {et.label}
          </button>
        ))}
      </div>

      {/* Error banner */}
      {error && (
        <div className="rounded-lg bg-red-500/10 border border-red-500/20 px-4 py-3 text-sm text-red-400">
          {(error as Error).message}
        </div>
      )}

      {/* Fields list */}
      <div className="space-y-2">
        {isLoading ? (
          <div className="flex items-center gap-2 py-8 justify-center text-muted-foreground">
            <span className="animate-spin h-4 w-4 border-2 border-blue-500 border-t-transparent rounded-full" />
            Loading…
          </div>
        ) : fieldDefs.length === 0 ? (
          <div className="text-center py-12 text-muted-foreground">
            <div className="text-4xl mb-3">📋</div>
            <p className="text-sm">No custom fields defined for {activeEntity}s yet.</p>
            <p className="text-xs mt-1">Click "Add Field" to create your first custom field.</p>
          </div>
        ) : (
          <div className="border rounded-lg overflow-hidden">
            <table className="w-full text-sm">
              <thead>
                <tr className="bg-accent/30">
                  <th className="text-left px-4 py-2.5 font-medium text-muted-foreground">Label</th>
                  <th className="text-left px-4 py-2.5 font-medium text-muted-foreground">Key</th>
                  <th className="text-left px-4 py-2.5 font-medium text-muted-foreground">Type</th>
                  <th className="text-left px-4 py-2.5 font-medium text-muted-foreground">Required</th>
                  <th className="text-right px-4 py-2.5 font-medium text-muted-foreground">Actions</th>
                </tr>
              </thead>
              <tbody>
                {fieldDefs.map((def) => (
                  <tr key={def.key} className="border-t hover:bg-accent/10 transition-colors">
                    <td className="px-4 py-3 font-medium">{def.label}</td>
                    <td className="px-4 py-3">
                      <code className="text-xs bg-accent px-1.5 py-0.5 rounded">{def.key}</code>
                    </td>
                    <td className="px-4 py-3">
                      <span className="inline-flex items-center gap-1.5 text-xs bg-blue-500/10 text-blue-400 px-2 py-0.5 rounded-full">
                        {FIELD_TYPES.find((t) => t.value === def.type)?.icon}{' '}
                        {FIELD_TYPES.find((t) => t.value === def.type)?.label || def.type}
                      </span>
                      {def.type === 'select' && def.options && (
                        <span className="ml-2 text-xs text-muted-foreground">
                          ({def.options.length} options)
                        </span>
                      )}
                    </td>
                    <td className="px-4 py-3">
                      {def.required ? (
                        <span className="text-xs text-amber-400">Required</span>
                      ) : (
                        <span className="text-xs text-muted-foreground">Optional</span>
                      )}
                    </td>
                    <td className="px-4 py-3 text-right">
                      <div className="flex items-center justify-end gap-1">
                        <button
                          onClick={() => openEditForm(def)}
                          className="p-1.5 rounded hover:bg-accent text-muted-foreground hover:text-foreground transition-colors"
                          title="Edit"
                        >
                          <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M17 3a2.85 2.83 0 1 1 4 4L7.5 20.5 2 22l1.5-5.5Z"/></svg>
                        </button>
                        {deleteConfirm === def.key ? (
                          <div className="flex items-center gap-1">
                            <button
                              onClick={() => deleteMutation.mutate(def.key)}
                              className="px-2 py-1 text-xs bg-red-600 text-white rounded hover:bg-red-700 transition-colors"
                            >
                              Confirm
                            </button>
                            <button
                              onClick={() => setDeleteConfirm(null)}
                              className="px-2 py-1 text-xs border rounded hover:bg-accent transition-colors"
                            >
                              Cancel
                            </button>
                          </div>
                        ) : (
                          <button
                            onClick={() => setDeleteConfirm(def.key)}
                            className="p-1.5 rounded hover:bg-red-500/10 text-muted-foreground hover:text-red-400 transition-colors"
                            title="Delete"
                          >
                            <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M3 6h18"/><path d="M19 6v14c0 1-1 2-2 2H7c-1 0-2-1-2-2V6"/><path d="M8 6V4c0-1 1-2 2-2h4c1 0 2 1 2 2v2"/></svg>
                          </button>
                        )}
                      </div>
                    </td>
                  </tr>
                ))}
              </tbody>
            </table>
          </div>
        )}
      </div>

      {/* Add button */}
      {!showForm && (
        <button
          onClick={openCreateForm}
          className="inline-flex items-center gap-2 px-4 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors"
        >
          <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M12 5v14"/><path d="M5 12h14"/></svg>
          Add Field
        </button>
      )}

      {/* Create/Edit form */}
      {showForm && (
        <form onSubmit={handleSubmit} className="border rounded-lg p-5 space-y-4 bg-card">
          <h3 className="text-sm font-semibold">
            {editingKey ? 'Edit Field' : 'New Custom Field'}
          </h3>

          <div className="grid grid-cols-2 gap-4">
            {/* Label */}
            <div className="space-y-1.5">
              <label className="text-sm font-medium">Label <span className="text-red-400">*</span></label>
              <input
                type="text"
                value={form.label}
                onChange={(e) => handleLabelChange(e.target.value)}
                className="w-full rounded-lg border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all"
                placeholder="e.g. Budget"
                required
              />
            </div>

            {/* Key (auto-generated, read-only when editing) */}
            <div className="space-y-1.5">
              <label className="text-sm font-medium">Key</label>
              <input
                type="text"
                value={form.key}
                onChange={(e) => !editingKey && setForm({ ...form, key: e.target.value })}
                readOnly={!!editingKey}
                className="w-full rounded-lg border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all read-only:opacity-60"
                placeholder="auto_generated"
              />
              <p className="text-xs text-muted-foreground">Snake_case identifier</p>
            </div>
          </div>

          <div className="grid grid-cols-2 gap-4">
            {/* Type */}
            <div className="space-y-1.5">
              <label className="text-sm font-medium">Type <span className="text-red-400">*</span></label>
              <select
                value={form.type}
                onChange={(e) => setForm({ ...form, type: e.target.value })}
                className="w-full rounded-lg border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all"
              >
                {FIELD_TYPES.map((ft) => (
                  <option key={ft.value} value={ft.value}>
                    {ft.icon} {ft.label}
                  </option>
                ))}
              </select>
            </div>

            {/* Required */}
            <div className="space-y-1.5">
              <label className="text-sm font-medium">Required</label>
              <label className="flex items-center gap-2 mt-1.5 cursor-pointer">
                <input
                  type="checkbox"
                  checked={form.required}
                  onChange={(e) => setForm({ ...form, required: e.target.checked })}
                  className="h-4 w-4 rounded border-gray-500 text-blue-600 focus:ring-blue-500/40"
                />
                <span className="text-sm text-muted-foreground">
                  This field must be filled
                </span>
              </label>
            </div>
          </div>

          {/* Options (for select type) */}
          {form.type === 'select' && (
            <div className="space-y-2">
              <label className="text-sm font-medium">Options <span className="text-red-400">*</span></label>
              <div className="flex gap-2">
                <input
                  type="text"
                  value={optionInput}
                  onChange={(e) => setOptionInput(e.target.value)}
                  onKeyDown={(e) => {
                    if (e.key === 'Enter') { e.preventDefault(); addOption(); }
                  }}
                  className="flex-1 rounded-lg border bg-background px-3 py-2 text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all"
                  placeholder="Type an option and press Enter"
                />
                <button
                  type="button"
                  onClick={addOption}
                  className="px-3 py-2 rounded-lg border text-sm hover:bg-accent transition-colors"
                >
                  Add
                </button>
              </div>
              <div className="flex flex-wrap gap-2">
                {form.options.map((opt) => (
                  <span
                    key={opt}
                    className="inline-flex items-center gap-1 bg-accent px-2.5 py-1 rounded-full text-xs"
                  >
                    {opt}
                    <button
                      type="button"
                      onClick={() => removeOption(opt)}
                      className="hover:text-red-400 transition-colors"
                    >
                      ×
                    </button>
                  </span>
                ))}
              </div>
            </div>
          )}

          {/* Actions */}
          <div className="flex gap-2 pt-2">
            <button
              type="submit"
              disabled={createMutation.isPending || updateMutation.isPending}
              className="px-4 py-2.5 bg-blue-600 hover:bg-blue-700 text-white rounded-lg text-sm font-medium transition-colors disabled:opacity-50"
            >
              {(createMutation.isPending || updateMutation.isPending) ? (
                <span className="flex items-center gap-2">
                  <span className="animate-spin h-3.5 w-3.5 border-2 border-white border-t-transparent rounded-full" />
                  Saving…
                </span>
              ) : editingKey ? 'Update Field' : 'Create Field'}
            </button>
            <button
              type="button"
              onClick={resetForm}
              className="px-4 py-2.5 border rounded-lg text-sm font-medium hover:bg-accent transition-colors"
            >
              Cancel
            </button>
          </div>
        </form>
      )}
    </div>
  );
}
