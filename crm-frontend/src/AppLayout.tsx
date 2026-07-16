import React, { useEffect, useState } from "react";
import { NavLink, Link, useLocation, useNavigate } from "react-router-dom";
import { useQuery } from "@tanstack/react-query";
import {
  Menu, X, UserRound, Shield, LogOut, ListChecks, BookOpen,
  LayoutDashboard, Users, Handshake, Mic, Sparkles, Zap, BarChart3, Share2, Settings,
} from "lucide-react";
import { useAuth } from "./lib/auth";
import { getThemePreference, setThemePreference, type ThemePreference } from "./lib/theme";
import GlobalSearch from "./components/common/GlobalSearch";
import AIUsageWidget from "./components/settings/AIUsageWidget";
import WorkspaceSwitcher from "./components/common/WorkspaceSwitcher";
import Modal from "./components/common/Modal";
import NotificationBell from "./features/notifications/NotificationBell";
import { useNotificationStream } from "./features/notifications/useNotificationStream";
import { openSetupChecklist } from "./features/onboarding/checklistState";
import { getObjectDefs, resendVerification, type CustomObjectDef } from "./lib/api";
import { DOCS_ENABLED, DOCS_IS_EXTERNAL, DOCS_URL } from "./lib/docs";
import { DocumentTitle } from "./lib/useDocumentTitle";

interface AppLayoutProps {
  children?: React.ReactNode;
  /**
   * Tab title for this route (U7.2) — set from App.tsx for the STATIC pages.
   * Omitted on purpose for pages that own a DYNAMIC title (a deal, a record, a
   * workflow, a report): they call useDocumentTitle themselves with their loaded
   * name. Hence <DocumentTitle> below is rendered only when a title is actually
   * passed — a hook here would run unconditionally and, because a parent's effect
   * fires after its children's, would overwrite the name the page just set.
   */
  title?: string;
}

export default function AppLayout({ children, title }: AppLayoutProps) {
  const { user, logout, activeWorkspace, hasCapability, canAccessObject } = useAuth();
  // App-global SSE listener for the header bell — keeps the unread badge + inbox
  // live while signed in (A6.2). No-op until a user is present.
  useNotificationStream(!!user);
  const [mobileNavOpen, setMobileNavOpen] = useState(false);
  const [userMenuOpen, setUserMenuOpen] = useState(false);
  const [theme, setTheme] = useState<ThemePreference>(() => getThemePreference());
  const location = useLocation();
  const navigate = useNavigate();

  // Close the mobile drawer + user menu on navigation.
  useEffect(() => { setMobileNavOpen(false); setUserMenuOpen(false); }, [location.pathname]);

  // The drawer only exists below md. If the viewport grows past the breakpoint
  // while it's open, close it: Radix ties the scroll lock and the aria-hidden on
  // the rest of the app to `open`, not to CSS, so a drawer merely hidden by a
  // md:hidden class would still freeze the page behind it.
  useEffect(() => {
    const mq = window.matchMedia?.('(min-width: 768px)');
    if (!mq) return;
    const sync = () => { if (mq.matches) setMobileNavOpen(false); };
    sync();
    mq.addEventListener('change', sync);
    return () => mq.removeEventListener('change', sync);
  }, []);

  const changeTheme = (t: ThemePreference) => {
    setTheme(t);
    setThemePreference(t);
  };

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

  // The sidebar's custom-object list. React Query (not a one-shot useEffect) so
  // deploying a starter template can invalidate ['object-defs'] and have the new
  // object appear in the nav — the retired welcome wizard did that with a full
  // window.location.reload() (U7.5). A failure resolves to [] rather than erroring:
  // a custom-object fetch must never take the whole app shell down.
  const { data: customObjects = [] } = useQuery<CustomObjectDef[]>({
    queryKey: ['object-defs'],
    queryFn: () => getObjectDefs().catch(() => []),
  });

  // "Setup guide" in the account menu is what makes the checklist RETURNABLE — the
  // wizard it replaced could never be reopened. Shown only to someone who has at
  // least one step they're allowed to do, so it's never a dead entry.
  const hasSetupSteps =
    hasCapability('members.invite') ||
    hasCapability('roles.manage') ||
    hasCapability('pipeline.manage') ||
    canAccessObject('contact', 'create');

  const openSetupGuide = () => {
    setUserMenuOpen(false);
    openSetupChecklist();
    navigate('/');
  };

  // Client-side NavLinks with real active state (the old sidebar used raw <a>
  // anchors — full page reload per click, 'Dashboard' hardcoded active). One
  // 'Settings' entry replaces the Settings/Members pair (U1 unified shell).
  const navItemClass = ({ isActive }: { isActive: boolean }) =>
    `flex items-center gap-2.5 px-3 py-2 rounded-lg text-sm font-medium transition-colors ${
      isActive ? 'bg-primary/10 text-primary' : 'text-muted-foreground hover:bg-accent hover:text-accent-foreground'
    }`;
  const navIconClass = "h-4 w-4 shrink-0";
  const navSectionClass = "px-3 pt-5 pb-1.5 text-[11px] font-semibold uppercase tracking-wider text-muted-foreground/70";

  const sidebarContent = (
    <>
      <div className="h-16 border-b flex items-center px-4">
        <WorkspaceSwitcher />
      </div>
      <div className="flex-1 p-3 overflow-y-auto">
        <nav className="space-y-0.5">
          <NavLink to="/" end className={navItemClass}><LayoutDashboard aria-hidden className={navIconClass} />Dashboard</NavLink>

          <p className={navSectionClass}>Records</p>
          <NavLink to="/contacts" className={navItemClass}><Users aria-hidden className={navIconClass} />Contacts</NavLink>
          <NavLink to="/deals" className={navItemClass}><Handshake aria-hidden className={navIconClass} />Deals</NavLink>
          {customObjects.map(obj => (
            <NavLink key={obj.slug} to={`/objects/${obj.slug}`} className={navItemClass}>
              {/* Custom-object icons are user-chosen emoji (data, not chrome) —
                  boxed to the same footprint as the lucide icons so rows align. */}
              <span aria-hidden className="flex h-4 w-4 shrink-0 items-center justify-center text-[13px] leading-none">{obj.icon}</span>
              {obj.label_plural}
            </NavLink>
          ))}

          <p className={navSectionClass}>Tools</p>
          <NavLink to="/voice" className={navItemClass}><Mic aria-hidden className={navIconClass} />Voice Notes</NavLink>
          <NavLink to="/ai" className={navItemClass}><Sparkles aria-hidden className={navIconClass} />AI Assistant</NavLink>
          <NavLink to="/workflows" className={navItemClass}><Zap aria-hidden className={navIconClass} />Automations</NavLink>
          <NavLink to="/reports" className={navItemClass}><BarChart3 aria-hidden className={navIconClass} />Reports</NavLink>
          <NavLink to="/shared-with-me" className={navItemClass}><Share2 aria-hidden className={navIconClass} />Shared with me</NavLink>

          <p className={navSectionClass}>Workspace</p>
          <NavLink to="/settings" className={navItemClass}><Settings aria-hidden className={navIconClass} />Settings</NavLink>
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
      {title && <DocumentTitle title={title} />}
      <aside className="w-64 border-r bg-card text-card-foreground hidden md:flex flex-col">
        {sidebarContent}
      </aside>

      {/* Mobile drawer (U1.5): below md the sidebar was simply gone — no way to
          reach Settings or even sign out on a phone. U7: now the shared Radix
          modal, so it traps focus and closes on Escape like every other dialog.
          hideHeader because the sidebar carries its own brand block and close X;
          the title below exists only to name the dialog for screen readers. */}
      <Modal
        open={mobileNavOpen}
        onClose={() => setMobileNavOpen(false)}
        title="Navigation"
        variant="drawer"
        side="left"
        widthClass="w-72 max-w-[85vw]"
        hideHeader
        padded={false}
      >
        <button
          onClick={() => setMobileNavOpen(false)}
          aria-label="Close menu"
          className="absolute top-4 right-3 p-1.5 rounded-md text-muted-foreground hover:bg-accent"
        >
          <X className="w-5 h-5" />
        </button>
        {sidebarContent}
      </Modal>

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
            <h1 className="text-lg font-semibold tracking-tight">Guerrilla <span className="text-primary">CRM</span></h1>
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
            {/* User menu (U2): the avatar was a decorative blank circle — now
                it's the account entry point (profile, security, theme, sign out). */}
            {user && (
              <div className="relative">
                <button
                  onClick={() => {
                    // Re-read the stored preference on open so the pills reflect
                    // a theme changed elsewhere (e.g. the Profile page) — the two
                    // controls hold independent state (U2 review).
                    if (!userMenuOpen) setTheme(getThemePreference());
                    setUserMenuOpen((v) => !v);
                  }}
                  aria-label="Account menu"
                  aria-expanded={userMenuOpen}
                  className="block rounded-full focus:outline-none focus:ring-2 focus:ring-primary"
                >
                  {user.avatar_url ? (
                    <img src={user.avatar_url} alt="" className="h-8 w-8 rounded-full object-cover" />
                  ) : (
                    <div className="h-8 w-8 rounded-full bg-primary/10 flex items-center justify-center text-xs font-semibold text-primary">
                      {user.first_name?.[0]}{user.last_name?.[0]}
                    </div>
                  )}
                </button>
                {userMenuOpen && (
                  <>
                    <div className="fixed inset-0 z-40" onClick={() => setUserMenuOpen(false)} />
                    <div className="absolute right-0 top-10 z-50 w-64 rounded-xl border border-border bg-card shadow-xl overflow-hidden">
                      <div className="px-4 py-3 border-b border-border">
                        <p className="text-sm font-medium text-foreground truncate">{user.first_name} {user.last_name}</p>
                        <p className="text-xs text-muted-foreground truncate">{user.email}</p>
                      </div>
                      <div className="py-1">
                        <Link to="/settings/profile" onClick={() => setUserMenuOpen(false)} className="flex items-center gap-2 px-4 py-2 text-sm text-foreground hover:bg-accent">
                          <UserRound className="w-4 h-4 text-muted-foreground" /> My profile
                        </Link>
                        <Link to="/settings/security" onClick={() => setUserMenuOpen(false)} className="flex items-center gap-2 px-4 py-2 text-sm text-foreground hover:bg-accent">
                          <Shield className="w-4 h-4 text-muted-foreground" /> Security
                        </Link>
                        {hasSetupSteps && (
                          <button
                            onClick={openSetupGuide}
                            className="w-full flex items-center gap-2 px-4 py-2 text-sm text-foreground hover:bg-accent text-left"
                          >
                            <ListChecks className="w-4 h-4 text-muted-foreground" /> Setup guide
                          </button>
                        )}
                        {/* Help & docs (U7.5): the product had no route to
                            documentation or support at all. The destination is
                            operator-configured (VITE_DOCS_URL) and the entry is
                            omitted entirely when it isn't set — an entry that
                            opens a 404 is worse than no entry. */}
                        {DOCS_ENABLED && (
                          <a
                            href={DOCS_URL}
                            onClick={() => setUserMenuOpen(false)}
                            {...(DOCS_IS_EXTERNAL ? { target: '_blank', rel: 'noopener noreferrer' } : {})}
                            className="flex items-center gap-2 px-4 py-2 text-sm text-foreground hover:bg-accent"
                          >
                            <BookOpen className="w-4 h-4 text-muted-foreground" /> Help &amp; docs
                          </a>
                        )}
                      </div>
                      <div className="px-4 py-2.5 border-t border-border">
                        <p className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground mb-1.5">Theme</p>
                        <div className="inline-flex rounded-lg border border-border overflow-hidden">
                          {(['light', 'system', 'dark'] as const).map((t) => (
                            <button
                              key={t}
                              onClick={() => changeTheme(t)}
                              className={`px-3 py-1 text-xs capitalize transition-colors ${theme === t ? 'bg-accent text-accent-foreground font-medium' : 'text-muted-foreground hover:bg-accent/50'}`}
                            >
                              {t}
                            </button>
                          ))}
                        </div>
                      </div>
                      <div className="border-t border-border py-1">
                        <button
                          onClick={logout}
                          className="w-full flex items-center gap-2 px-4 py-2 text-sm text-foreground hover:bg-accent text-left"
                        >
                          <LogOut className="w-4 h-4 text-muted-foreground" /> Sign out
                        </button>
                      </div>
                    </div>
                  </>
                )}
              </div>
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
    </div>
  );
}
