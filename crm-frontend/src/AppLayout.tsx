import React, { useEffect, useState } from "react";
import { useAuth } from "./lib/auth";
import GlobalSearch from "./components/common/GlobalSearch";
import AIUsageWidget from "./components/settings/AIUsageWidget";
import WelcomeModal from "./components/onboarding/WelcomeModal";
import WorkspaceSwitcher from "./components/common/WorkspaceSwitcher";
import { getObjectDefs, getFieldDefs, resendVerification, type CustomObjectDef } from "./lib/api";

interface AppLayoutProps {
  children?: React.ReactNode;
}

export default function AppLayout({ children }: AppLayoutProps) {
  const { user, logout, activeWorkspace } = useAuth();
  const [customObjects, setCustomObjects] = useState<CustomObjectDef[]>([]);
  const [showWelcome, setShowWelcome] = useState(false);
  const [isLoading, setIsLoading] = useState(true);

  // Soft-gate email verification (P1): show a persistent banner with a resend
  // action until the user confirms their email.
  const [verifyState, setVerifyState] = useState<'idle' | 'sending' | 'sent' | 'error'>('idle');
  const [verifyMsg, setVerifyMsg] = useState('');
  const needsVerify = !!user && !user.email_verified_at;

  const handleResend = async () => {
    setVerifyState('sending');
    try {
      await resendVerification();
      setVerifyState('sent');
      setVerifyMsg('Verification email sent — check your inbox.');
    } catch (err) {
      setVerifyState('error');
      setVerifyMsg(err instanceof Error ? err.message : 'Could not send verification email.');
    }
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
            <a href="/voice" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">🎙 Voice Notes</a>
            <a href="/ai" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">✦ AI Assistant</a>
            <a href="/workflows" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">⚡ Automations</a>
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
            {/* P6: the global cross-object palette replaces the contact-only bar
                when objects.search is enabled; otherwise the legacy bar stays. */}
            {/* Cross-object command-palette search (P7 — replaced the contact-only SearchBar). */}
            <GlobalSearch />
            <div className="flex-1" />
            {activeWorkspace && (
              <span className="text-sm text-muted-foreground hidden sm:block">
                {activeWorkspace.org_name}
              </span>
            )}
            {user?.avatar_url ? (
              <img src={user.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" />
            ) : (
              <div className="h-8 w-8 rounded-full bg-primary/10"></div>
            )}
          </div>
        </header>
        {needsVerify && (
          <div className="border-b border-amber-500/30 bg-amber-500/10 px-6 py-2.5 flex flex-wrap items-center gap-x-3 gap-y-1 text-sm">
            <span className="text-amber-700 dark:text-amber-300">
              Please verify your email{user?.email ? ` (${user.email})` : ''} to keep full access.
            </span>
            {verifyState === 'sent' || verifyState === 'error' ? (
              <span className={verifyState === 'sent' ? 'text-green-600 dark:text-green-400' : 'text-red-600 dark:text-red-400'}>
                {verifyMsg}
              </span>
            ) : (
              <button
                onClick={handleResend}
                disabled={verifyState === 'sending'}
                className="font-medium text-amber-700 dark:text-amber-300 underline hover:no-underline disabled:opacity-60"
              >
                {verifyState === 'sending' ? 'Sending…' : 'Resend verification email'}
              </button>
            )}
          </div>
        )}
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
      {showWelcome && !isLoading && (
        <WelcomeModal onComplete={() => setShowWelcome(false)} />
      )}
    </div>
  );
}
