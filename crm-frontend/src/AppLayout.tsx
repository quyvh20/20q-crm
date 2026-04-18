import React, { useEffect, useState } from "react";
import { useAuth } from "./lib/auth";
import ChatPanel from "./components/ai/ChatPanel";
import SearchBar from "./components/common/SearchBar";
import AIUsageWidget from "./components/settings/AIUsageWidget";
import WelcomeModal from "./components/onboarding/WelcomeModal";
import WorkspaceSwitcher from "./components/common/WorkspaceSwitcher";
import { getObjectDefs, getFieldDefs, type CustomObjectDef } from "./lib/api";

interface AppLayoutProps {
  children?: React.ReactNode;
}

export default function AppLayout({ children }: AppLayoutProps) {
  const { user, logout, activeWorkspace } = useAuth();
  const [customObjects, setCustomObjects] = useState<CustomObjectDef[]>([]);
  const [showWelcome, setShowWelcome] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [chatOpen, setChatOpen] = useState(() => localStorage.getItem('chat_panel_open') === 'true');

  const toggleChat = () => {
    const next = !chatOpen;
    setChatOpen(next);
    localStorage.setItem('chat_panel_open', String(next));
  };

  useEffect(() => {
    Promise.all([
      getObjectDefs().catch(() => []),
      getFieldDefs().catch(() => [])
    ])
      .then(([objects, fields]) => {
        setCustomObjects(objects);
        
        const hasCompletedOnboarding = localStorage.getItem('onboarding_completed') === 'true';
        if (objects.length === 0 && fields.length === 0 && !hasCompletedOnboarding) {
          setShowWelcome(true);
        }
      })
      .finally(() => setIsLoading(false));
  }, []);

  return (
    <div className="flex h-screen w-full bg-background overflow-hidden">
      <aside className="w-64 border-r bg-card text-card-foreground hidden md:flex flex-col">
        <div className="h-16 border-b flex items-center px-4">
          <WorkspaceSwitcher />
        </div>
        <div className="flex-1 p-4">
          <nav className="space-y-2">
            <a href="/" className="block px-3 py-2 rounded-md bg-accent text-accent-foreground font-medium">Dashboard</a>
            <a href="/contacts" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Contacts</a>
            <a href="/deals" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Deals</a>
            {customObjects.map(obj => (
              <a key={obj.slug} href={`/objects/${obj.slug}`}
                className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">
                <span style={{ marginRight: 6 }}>{obj.icon}</span>{obj.label_plural}
              </a>
            ))}
            <a href="/settings" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Settings</a>
            <a href="/settings/workspace" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Members</a>
          </nav>
          <div className="mt-4">
            <AIUsageWidget />
          </div>
        </div>

        {user && (
          <div className="p-4 border-t">
            <div className="flex items-center gap-3 mb-3">
              {user.avatar_url ? (
                <img src={user.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" />
              ) : (
                <div className="h-8 w-8 rounded-full bg-primary/10 flex items-center justify-center text-sm font-medium">
                  {user.first_name?.[0]}{user.last_name?.[0]}
                </div>
              )}
              <div className="flex-1 min-w-0">
                <p className="text-sm font-medium truncate">{user.first_name} {user.last_name}</p>
                <p className="text-xs text-muted-foreground truncate">{user.email}</p>
              </div>
            </div>
            <button
              onClick={logout}
              className="w-full px-3 py-2 text-sm rounded-md border border-border hover:bg-accent transition-colors text-muted-foreground hover:text-foreground"
            >
              Sign out
            </button>
          </div>
        )}
      </aside>

      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-16 border-b bg-card flex items-center justify-between px-6">
          <div className="md:hidden">
            <h1 className="text-xl font-bold tracking-tight">Guerrilla CRM</h1>
          </div>
          <div className="flex flex-1 items-center gap-4">
            <SearchBar />
            <div className="flex-1" />
            {activeWorkspace && (
              <span className="text-sm text-muted-foreground hidden sm:block">
                {activeWorkspace.org_name}
              </span>
            )}
            {/* AI Chat toggle */}
            <button
              onClick={toggleChat}
              title="AI Assistant"
              style={{
                width: 36, height: 36, borderRadius: 10,
                background: chatOpen
                  ? 'linear-gradient(135deg, #f59e0b, #ef4444)'
                  : 'transparent',
                border: '1px solid var(--border)',
                cursor: 'pointer',
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                fontSize: 16,
                color: chatOpen ? '#fff' : 'var(--muted-foreground)',
                transition: 'all 0.15s',
              }}
            >
              ✦
            </button>
            {user?.avatar_url ? (
              <img src={user.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" />
            ) : (
              <div className="h-8 w-8 rounded-full bg-primary/10"></div>
            )}
          </div>
        </header>
        <main className="flex-1 overflow-auto p-6">
          {children || (
            <div className="flex h-full items-center justify-center border-2 border-dashed border-muted rounded-xl">
              <div className="text-center">
                <h3 className="text-2xl font-semibold tracking-tight">Welcome to Guerrilla CRM</h3>
                <p className="text-muted-foreground mt-2">Get started by selecting an option from the sidebar.</p>
              </div>
            </div>
          )}
        </main>
      </div>
      <ChatPanel open={chatOpen} onClose={() => { setChatOpen(false); localStorage.setItem('chat_panel_open', 'false'); }} />

      {showWelcome && !isLoading && (
        <WelcomeModal onComplete={() => setShowWelcome(false)} />
      )}
    </div>
  );
}
