import React from "react";
import { useAuth } from "./lib/auth";
import AIAssistant from "./components/ai/AIAssistant";
import SearchBar from "./components/common/SearchBar";
import AIUsageWidget from "./components/settings/AIUsageWidget";

interface AppLayoutProps {
  children?: React.ReactNode;
}

export default function AppLayout({ children }: AppLayoutProps) {
  const { user, logout } = useAuth();

  return (
    <div className="flex h-screen w-full bg-background overflow-hidden">
      {/* Sidebar */}
      <aside className="w-64 border-r bg-card text-card-foreground hidden md:flex flex-col">
        <div className="h-16 border-b flex items-center px-6">
          <h1 className="text-xl font-bold tracking-tight">Guerrilla CRM</h1>
        </div>
        <div className="flex-1 p-4">
          <nav className="space-y-2">
            <a href="/" className="block px-3 py-2 rounded-md bg-accent text-accent-foreground font-medium">Dashboard</a>
            <a href="/contacts" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Contacts</a>
            <a href="/deals" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Deals</a>
            <a href="/settings" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Settings</a>
          </nav>
          <div className="mt-4">
            <AIUsageWidget />
          </div>
        </div>

        {/* User info + logout */}
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

      {/* Main content */}
      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-16 border-b bg-card flex items-center justify-between px-6">
          <div className="md:hidden">
            <h1 className="text-xl font-bold tracking-tight">Guerrilla CRM</h1>
          </div>
          <div className="flex flex-1 items-center gap-4">
            <SearchBar />
            <div className="flex-1" />
            {user && (
              <span className="text-sm text-muted-foreground hidden sm:block">
                {user.organization?.name}
              </span>
            )}
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
      {/* Global AI Assistant */}
      <AIAssistant />
    </div>
  );
}
