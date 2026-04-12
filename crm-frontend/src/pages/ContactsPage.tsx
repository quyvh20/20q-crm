import { useState, useEffect } from 'react';
import { QueryClient, QueryClientProvider, useQueryClient, useQuery } from '@tanstack/react-query';
import ContactList from '../components/contacts/ContactList';
import ContactForm from '../components/contacts/ContactForm';
import ImportModal from '../components/contacts/ImportModal';
import { getCompanies, getTags, type Contact, type ContactFilter } from '../lib/api';

const queryClient = new QueryClient({
  defaultOptions: {
    queries: { staleTime: 30_000, retry: 1 },
  },
});

function ContactsPageInner() {
  const qc = useQueryClient();
  const [searchQuery, setSearchQuery] = useState('');
  const [debouncedQuery, setDebouncedQuery] = useState('');

  const [selectedCompanyId, setSelectedCompanyId] = useState<string>('');
  // Multi-select tags: Set<id>
  const [selectedTagIds, setSelectedTagIds] = useState<Set<string>>(new Set());
  const [showFilters, setShowFilters] = useState(true);

  const [editingContact, setEditingContact] = useState<Contact | null>(null);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [showImportModal, setShowImportModal] = useState(false);
  const [semanticMode, setSemanticMode] = useState(false);

  // Fetch filter metadata
  const { data: companies } = useQuery({ queryKey: ['companies'], queryFn: getCompanies });
  const { data: tags } = useQuery({ queryKey: ['tags'], queryFn: getTags });

  // Debounce search (300ms)
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(searchQuery), 300);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  const toggleTag = (id: string) => {
    setSelectedTagIds(prev => {
      const s = new Set(prev);
      s.has(id) ? s.delete(id) : s.add(id);
      return s;
    });
  };

  const clearAllFilters = () => {
    setSearchQuery('');
    setSelectedCompanyId('');
    setSelectedTagIds(new Set());
  };

  const hasActiveFilters = !!(searchQuery || selectedCompanyId || selectedTagIds.size > 0);

  const autoSemantic = debouncedQuery.trim().split(/\s+/).filter(Boolean).length >= 3;
  const useSemantic = semanticMode || autoSemantic;

  const filters: ContactFilter = {
    q: debouncedQuery || undefined,
    company_id: selectedCompanyId || undefined,
    tag_ids: selectedTagIds.size > 0 ? Array.from(selectedTagIds) : undefined,
    semantic: useSemantic || undefined,
  };

  // Active filter chips data
  const activeChips: { label: string; onRemove: () => void }[] = [];
  if (searchQuery) {
    const label = useSemantic
      ? `✦ AI Search: "${searchQuery}"`
      : `Search: "${searchQuery}"`;
    activeChips.push({ label, onRemove: () => setSearchQuery('') });
  }
  if (selectedCompanyId) {
    const co = companies?.find(c => c.id === selectedCompanyId);
    activeChips.push({ label: `Company: ${co?.name ?? '…'}`, onRemove: () => setSelectedCompanyId('') });
  }
  selectedTagIds.forEach(id => {
    const tag = tags?.find(t => t.id === id);
    activeChips.push({ label: `Tag: ${tag?.name ?? '…'}`, onRemove: () => toggleTag(id) });
  });

  return (
    <div className="space-y-6 max-w-[1600px] mx-auto">
      {/* Header */}
      <div className="flex items-center justify-between">
        <div>
          <h1 className="text-2xl font-bold tracking-tight">Contacts</h1>
          <p className="text-sm text-muted-foreground mt-1">Manage your contacts and leads</p>
        </div>
        <div className="flex items-center gap-2">
          <button
            onClick={() => setShowImportModal(true)}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg border text-sm font-medium hover:bg-accent transition-colors"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M21 15v4a2 2 0 0 1-2 2H5a2 2 0 0 1-2-2v-4"/><polyline points="17 8 12 3 7 8"/><line x1="12" x2="12" y1="3" y2="15"/></svg>
            Import
          </button>
          <button
            onClick={() => setShowCreateForm(true)}
            className="inline-flex items-center gap-2 px-4 py-2 rounded-lg bg-blue-600 hover:bg-blue-700 text-white text-sm font-medium transition-colors"
          >
            <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><path d="M5 12h14"/><path d="M12 5v14"/></svg>
            Add Contact
          </button>
        </div>
      </div>

      <div className="flex flex-col md:flex-row gap-6 items-start">
        {/* Sidebar Filters */}
        {showFilters && (
          <div className="w-full md:w-64 shrink-0 space-y-6 p-5 bg-card border rounded-xl">
            <div>
              <h3 className="font-semibold mb-3 text-sm">Filters</h3>

              {/* Search */}
              <div className="space-y-2 mb-5">
                <label className="text-xs font-medium text-muted-foreground">Search</label>
                <div className="relative">
                  <svg xmlns="http://www.w3.org/2000/svg" width="14" height="14" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round" className="absolute left-3 top-1/2 -translate-y-1/2 text-muted-foreground"><circle cx="11" cy="11" r="8"/><path d="m21 21-4.3-4.3"/></svg>
                  <input
                    id="filter-search"
                    type="text"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder={semanticMode ? 'Describe who you\'re looking for…' : 'Name or email...'}
                    className="w-full pl-9 pr-3 py-2 rounded-lg border bg-background text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all"
                  />
                </div>
                <button
                  id="contacts-semantic-toggle"
                  onClick={() => setSemanticMode(v => !v)}
                  title={semanticMode ? 'Disable AI Semantic Search' : 'Enable AI Semantic Search'}
                  className="mt-1 w-full flex items-center justify-center gap-2 py-1.5 rounded-lg border text-xs font-medium transition-all"
                  style={semanticMode
                    ? { background: 'linear-gradient(135deg,#eef2ff,#faf5ff)', borderColor: '#6366f1', color: '#6366f1' }
                    : { borderColor: 'var(--border)', color: 'var(--muted-foreground)' }
                  }
                >
                  <svg width="12" height="12" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2"><path d="M12 2a8 8 0 0 1 8 8c0 5.25-8 12-8 12S4 15.25 4 10a8 8 0 0 1 8-8z"/><circle cx="12" cy="10" r="3"/></svg>
                  {semanticMode ? '🔮 AI Search ON' : 'AI Search'}
                </button>
                {autoSemantic && searchQuery && !semanticMode && (
                  <p className="text-xs" style={{ color: '#059669' }}>⚡ Auto AI Search active</p>
                )}
              </div>

              {/* Company Filter */}
              <div className="space-y-2 mb-5">
                <label className="text-xs font-medium text-muted-foreground">Company</label>
                <select
                  id="filter-company"
                  value={selectedCompanyId}
                  onChange={(e) => setSelectedCompanyId(e.target.value)}
                  className="w-full px-3 py-2 rounded-lg border bg-background text-sm outline-none focus:ring-2 focus:ring-blue-500/40 transition-all"
                >
                  <option value="">All Companies</option>
                  {companies?.map(c => (
                    <option key={c.id} value={c.id}>{c.name}</option>
                  ))}
                </select>
              </div>

              {/* Tag Multi-Filter */}
              <div className="space-y-2 mb-5">
                <label className="text-xs font-medium text-muted-foreground">
                  Tags
                  {selectedTagIds.size > 0 && (
                    <span className="ml-1.5 inline-flex items-center justify-center w-4 h-4 rounded-full bg-blue-500 text-white text-[9px] font-bold">
                      {selectedTagIds.size}
                    </span>
                  )}
                </label>
                <div className="flex flex-wrap gap-2">
                  {tags?.map(t => {
                    const isActive = selectedTagIds.has(t.id);
                    return (
                      <button
                        key={t.id}
                        id={`filter-tag-${t.id}`}
                        onClick={() => toggleTag(t.id)}
                        style={isActive
                          ? { backgroundColor: t.color + '20', borderColor: t.color, color: t.color }
                          : { borderColor: 'var(--border)' }
                        }
                        className={`px-3 py-1.5 rounded-full text-xs font-medium border transition-all ${
                          !isActive && 'bg-background hover:bg-muted text-muted-foreground'
                        } ${isActive && 'ring-1'}`}
                      >
                        {t.name}
                        {isActive && (
                          <span className="ml-1 opacity-70">✓</span>
                        )}
                      </button>
                    );
                  })}
                </div>
                {selectedTagIds.size > 0 && (
                  <p className="text-xs text-muted-foreground mt-1">
                    Matching contacts with <em>any</em> selected tag
                  </p>
                )}
              </div>

              {/* Clear */}
              {hasActiveFilters && (
                <button
                  id="filter-clear-all"
                  onClick={clearAllFilters}
                  className="w-full py-2 text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                >
                  Clear All Filters
                </button>
              )}
            </div>
          </div>
        )}

        {/* Contact List */}
        <div className="flex-1 w-full min-w-0">
          <div className="mb-4 flex items-center justify-between flex-wrap gap-3">
            <button
              onClick={() => setShowFilters(!showFilters)}
              className="inline-flex items-center gap-2 text-sm font-medium text-muted-foreground hover:text-foreground transition-colors"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/></svg>
              {showFilters ? 'Hide Filters' : 'Show Filters'}
            </button>

            {/* AI Search badge — shown when semantic results are active */}
            {useSemantic && debouncedQuery && (
              <span
                id="ai-search-badge"
                style={{
                  display: 'inline-flex', alignItems: 'center', gap: '5px',
                  padding: '3px 10px',
                  background: 'linear-gradient(135deg,#6366f1,#8b5cf6)',
                  color: 'white', borderRadius: '99px',
                  fontSize: '11px', fontWeight: 700,
                  boxShadow: '0 1px 8px rgba(99,102,241,0.35)',
                }}
              >
                ✦ AI Search Active
              </span>
            )}

            {/* Active filter chips */}
            {activeChips.length > 0 && (
              <div id="active-filter-chips" className="flex flex-wrap gap-1.5 items-center">
                {activeChips.map((chip, i) => (
                  <span
                    key={i}
                    className="inline-flex items-center gap-1 px-2.5 py-1 rounded-full bg-blue-500/10 border border-blue-500/30 text-xs font-medium text-blue-600"
                  >
                    {chip.label}
                    <button
                      onClick={chip.onRemove}
                      className="ml-0.5 hover:text-blue-800 transition-colors"
                      aria-label={`Remove ${chip.label} filter`}
                    >
                      <svg xmlns="http://www.w3.org/2000/svg" width="10" height="10" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="3" strokeLinecap="round" strokeLinejoin="round"><path d="M18 6 6 18"/><path d="m6 6 12 12"/></svg>
                    </button>
                  </span>
                ))}
                {activeChips.length > 1 && (
                  <button
                    onClick={clearAllFilters}
                    className="text-xs text-muted-foreground hover:text-foreground underline underline-offset-2"
                  >
                    Clear all
                  </button>
                )}
              </div>
            )}
          </div>

          <ContactList
            filters={filters}
            onEdit={(contact) => setEditingContact(contact)}
            onImport={() => setShowImportModal(true)}
          />
        </div>
      </div>

      {/* Create / Edit form */}
      {(showCreateForm || editingContact) && (
        <ContactForm
          contact={editingContact}
          onClose={() => {
            setShowCreateForm(false);
            setEditingContact(null);
          }}
        />
      )}

      {/* Import modal */}
      {showImportModal && (
        <ImportModal
          onClose={() => setShowImportModal(false)}
          onSuccess={() => {
            qc.invalidateQueries({ queryKey: ['contacts'] });
            qc.invalidateQueries({ queryKey: ['companies'] });
            qc.invalidateQueries({ queryKey: ['tags'] });
          }}
        />
      )}
    </div>
  );
}

export default function ContactsPage() {
  return (
    <QueryClientProvider client={queryClient}>
      <ContactsPageInner />
    </QueryClientProvider>
  );
}
