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
  const [selectedTagId, setSelectedTagId] = useState<string>('');
  const [showFilters, setShowFilters] = useState(true);

  const [editingContact, setEditingContact] = useState<Contact | null>(null);
  const [showCreateForm, setShowCreateForm] = useState(false);
  const [showImportModal, setShowImportModal] = useState(false);

  // Fetch filter metadata
  const { data: companies } = useQuery({ queryKey: ['companies'], queryFn: getCompanies });
  const { data: tags } = useQuery({ queryKey: ['tags'], queryFn: getTags });

  // Debounce search (300ms)
  useEffect(() => {
    const timer = setTimeout(() => setDebouncedQuery(searchQuery), 300);
    return () => clearTimeout(timer);
  }, [searchQuery]);

  const filters: ContactFilter = {
    q: debouncedQuery || undefined,
    company_id: selectedCompanyId || undefined,
    tag_ids: selectedTagId ? [selectedTagId] : undefined,
  };

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
        {/* Sidebar Fillters */}
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
                    type="text"
                    value={searchQuery}
                    onChange={(e) => setSearchQuery(e.target.value)}
                    placeholder="Name or email..."
                    className="w-full pl-9 pr-3 py-2 rounded-lg border bg-background text-sm outline-none focus:ring-2 focus:ring-blue-500/40 focus:border-blue-500 transition-all"
                  />
                </div>
              </div>

              {/* Company Filter */}
              <div className="space-y-2 mb-5">
                <label className="text-xs font-medium text-muted-foreground">Company</label>
                <select
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

              {/* Tag Filter */}
              <div className="space-y-2 mb-5">
                <label className="text-xs font-medium text-muted-foreground">Tag</label>
                <div className="flex flex-wrap gap-2">
                  <button
                    onClick={() => setSelectedTagId('')}
                    className={`px-3 py-1.5 rounded-full text-xs font-medium border transition-colors ${!selectedTagId ? 'bg-blue-50 border-blue-200 text-blue-700' : 'bg-background hover:bg-muted text-muted-foreground'}`}
                  >
                    All Tags
                  </button>
                  {tags?.map(t => (
                    <button
                      key={t.id}
                      onClick={() => setSelectedTagId(t.id)}
                      style={
                        selectedTagId === t.id 
                          ? { backgroundColor: t.color + '20', borderColor: t.color, color: t.color }
                          : { borderColor: 'var(--border)' }
                      }
                      className={`px-3 py-1.5 rounded-full text-xs font-medium border transition-colors ${selectedTagId !== t.id && 'bg-background hover:bg-muted text-muted-foreground'}`}
                    >
                      {t.name}
                    </button>
                  ))}
                </div>
              </div>

              {/* Reset */}
              {(searchQuery || selectedCompanyId || selectedTagId) && (
                <button
                  onClick={() => {
                    setSearchQuery('');
                    setSelectedCompanyId('');
                    setSelectedTagId('');
                  }}
                  className="w-full py-2 text-sm font-medium text-muted-foreground hover:text-foreground hover:bg-muted rounded-lg transition-colors"
                >
                  Clear Filters
                </button>
              )}
            </div>
          </div>
        )}

        {/* Contact List */}
        <div className="flex-1 w-full min-w-0">
          <div className="mb-4 flex items-center justify-between">
            <button
              onClick={() => setShowFilters(!showFilters)}
              className="inline-flex items-center gap-2 text-sm font-medium text-muted-foreground hover:text-foreground transition-colors"
            >
              <svg xmlns="http://www.w3.org/2000/svg" width="16" height="16" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2" strokeLinecap="round" strokeLinejoin="round"><polygon points="22 3 2 3 10 12.46 10 19 14 21 14 12.46 22 3"/></svg>
              {showFilters ? 'Hide Filters' : 'Show Filters'}
            </button>
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
