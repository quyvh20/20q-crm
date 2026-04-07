import React from "react";

interface AppLayoutProps {
  children?: React.ReactNode;
}

export default function AppLayout({ children }: AppLayoutProps) {
  return (
    <div className="flex h-screen w-full bg-background overflow-hidden">
      {/* Sidebar */}
      <aside className="w-64 border-r bg-card text-card-foreground hidden md:flex flex-col">
        <div className="h-16 border-b flex items-center px-6">
          <h1 className="text-xl font-bold tracking-tight">Guerrilla CRM</h1>
        </div>
        <div className="flex-1 p-4">
          <nav className="space-y-2">
            {/* Nav items placeholder */}
            <a href="#" className="block px-3 py-2 rounded-md bg-accent text-accent-foreground font-medium">Dashboard</a>
            <a href="#" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Customers</a>
            <a href="#" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Deals</a>
            <a href="#" className="block px-3 py-2 rounded-md hover:bg-accent hover:text-accent-foreground font-medium text-muted-foreground transition-colors">Settings</a>
          </nav>
        </div>
      </aside>

      {/* Main content */}
      <div className="flex-1 flex flex-col min-w-0">
        <header className="h-16 border-b bg-card flex items-center justify-between px-6">
          <div className="md:hidden">
            <h1 className="text-xl font-bold tracking-tight">Guerrilla CRM</h1>
          </div>
          <div className="flex flex-1 justify-end items-center gap-4">
               {/* Topbar placeholder */}
               <div className="h-8 w-8 rounded-full bg-primary/10"></div>
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
    </div>
  );
}
