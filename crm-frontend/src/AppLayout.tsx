import React, { useEffect, useState } from "react";
import { NavLink, useLocation } from "react-router-dom";
import { Menu, X } from "lucide-react";
import { useAuth } from "./lib/auth";
import GlobalSearch from "./components/common/GlobalSearch";
import AIUsageWidget from "./components/settings/AIUsageWidget";
import WelcomeModal from "./components/onboarding/WelcomeModal";
import WorkspaceSwitcher from "./components/common/WorkspaceSwitcher";
import NotificationBell from "./features/notifications/NotificationBell";
import { useNotificationStream } from "./features/notifications/useNotificationStream";
import { getObjectDefs, getFieldDefs, resendVerification, type CustomObjectDef } from "./lib/api";

interface AppLayoutProps {
  children?: React.ReactNode;
}

export default function AppLayout({ children }: AppLayoutProps) {
  const { user, logout, activeWorkspace } = useAuth();
  // App-global SSE listener for the header bell — keeps the unread badge + inbox
  // live while signed in (A6.2). No-op until a user is present.
  useNotificationStream(!!user);
  const [customObjects, setCustomObjects] = useState<CustomObjectDef[]>([]);
  const [showWelcome, setShowWelcome] = useState(false);
  const [isLoading, setIsLoading] = useState(true);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const location = useLocation();

  // Close the mobile drawer on navigation (NavLinks no longer full-reload).
  useEffect(() => { setMobileNavOpen(false); }, [location.pathname]);

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

  // Client-side NavLinks with real active state (the old sidebar used raw <a>
  // anchors — full page reload per click, 'Dashboard' hardcoded active). One
  // 'Settings' entry replaces the Settings/Members pair (U1 unified shell).
  const navItemClass = ({ isActive }: { isActive: boolean }) =>
    `block px-3 py-2 rounded-md font-medium transition-colors ${
      isActive ? 'bg-accent text-accent-foreground' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
    }`;

  const sidebarContent = (
    <>
      <div className="h-16 border-b flex items-center px-4">
        <WorkspaceSwitcher />
      </div>
      <div className="flex-1 p-4 overflow-y-auto">
        <nav className="space-y-2">
          <NavLink to="/" end className={navItemClass}>Dashboard</NavLink>
          <NavLink to="/contacts" className={navItemClass}>Contacts</NavLink>
          <NavLink to="/deals" className={navItemClass}>Deals</NavLink>
          <NavLink to="/voice" className={navItemClass}>🎙 Voice Notes</NavLink>
          <NavLink to="/ai" className={navItemClass}>✦ AI Assistant</NavLink>
          <NavLink to="/workflows" className={navItemClass}>⚡ Automations</NavLink>
          <NavLink to="/reports" className={navItemClass}>📊 Reports</NavLink>
          {customObjects.map(obj => (
            <NavLink key={obj.slug} to={`/objects/${obj.slug}`} className={navItemClass}>
              <span style={{ marginRight: 6 }}>{obj.icon}</span>{obj.label_plural}
            </NavLink>
          ))}
          <NavLink to="/settings" className={navItemClass}>Settings</NavLink>
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
    </>
  );

  return (
    <div className="flex h-screen w-full bg-background overflow-hidden">
      <aside className="w-64 border-r bg-card text-card-foreground hidden md:flex flex-col">
        {sidebarContent}
      </aside>

      {/* Mobile drawer (U1.5): below md the sidebar was simply gone — no way to
          reach Settings or even sign out on a phone. */}
      {mobileNavOpen && (
        <div className="fixed inset-0 z-50 md:hidden">
          <div className="absolute inset-0 bg-black/60" onClick={() => setMobileNavOpen(false)} />
          <aside className="absolute inset-y-0 left-0 w-72 max-w-[85vw] bg-card text-card-foreground border-r flex flex-col shadow-xl">
            <button
              onClick={() => setMobileNavOpen(false)}
              aria-label="Close menu"
              className="absolute top-4 right-3 p-1.5 rounded-md text-muted-foreground hover:bg-accent"
            >
              <X className="w-5 h-5" />
            </button>
            {sidebarContent}
          </aside>
        </div>
      )}

      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-16 border-b bg-card flex items-center justify-between px-4 sm:px-6">
          <div className="md:hidden flex items-center gap-3">
            <button
              onClick={() => setMobileNavOpen(true)}
              aria-label="Open menu"
              className="p-2 -ml-1 rounded-md text-muted-foreground hover:bg-accent"
            >
              <Menu className="w-5 h-5" />
            </button>
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
            <NotificationBell />
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
