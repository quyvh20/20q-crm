import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import { switchWorkspace as apiSwitchWorkspace, setAccessToken as setApiToken, readCsrfToken, getMyCapabilities, type Workspace } from './api';

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

interface User {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  full_name?: string;
  role?: string;
  avatar_url?: string;
  // null/absent until the user verifies their email (P1). Drives the verify banner.
  email_verified_at?: string | null;
}

interface AuthContextType {
  user: User | null;
  accessToken: string | null;
  activeWorkspace: Workspace | null;
  workspaces: Workspace[];
  currentRole: string;
  isLoading: boolean;
  isAuthenticated: boolean;
  // Effective system capabilities of the current user in the active workspace
  // (P3). Owner gets all. Use hasCapability for permission-aware UI.
  capabilities: string[];
  hasCapability: (code: string) => boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (data: RegisterData) => Promise<void>;
  logout: () => Promise<void>;
  refreshAuth: () => Promise<void>;
  switchWorkspace: (orgId: string) => Promise<void>;
}

interface RegisterData {
  org_name: string;
  org_type?: string;
  email: string;
  password: string;
  first_name: string;
  last_name?: string;
}

interface AuthResponse {
  data: {
    access_token: string;
    refresh_token: string;
    user: User;
    workspaces?: Workspace[];
  };
  error: string | null;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

function findActiveWorkspace(workspaces: Workspace[]): Workspace | null {
  const savedId = localStorage.getItem('active_workspace_id');
  if (savedId) {
    const found = workspaces.find(w => w.org_id === savedId);
    if (found) return found;
  }
  return workspaces.length > 0 ? workspaces[0] : null;
}

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  // Access token is mirrored here for context consumers, but the source of truth
  // is the in-memory token in api.ts (setApiToken). It is never persisted (P2).
  const [accessToken, setAccessToken] = useState<string | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [activeWorkspace, setActiveWorkspace] = useState<Workspace | null>(null);
  const [capabilities, setCapabilities] = useState<string[]>([]);
  const [isLoading, setIsLoading] = useState(true);

  const currentRole = activeWorkspace?.role || '';
  const hasCapability = useCallback((code: string) => capabilities.includes(code), [capabilities]);

  // Fetch the caller's effective capabilities for the active org after auth
  // changes. Fire-and-forget: on failure capabilities stay empty (UI fails closed).
  const loadCapabilities = useCallback(() => {
    getMyCapabilities().then(setCapabilities).catch(() => setCapabilities([]));
  }, []);

  const clearAuth = useCallback(() => {
    setUser(null);
    setAccessToken(null);
    setApiToken(null);
    setWorkspaces([]);
    setActiveWorkspace(null);
    setCapabilities([]);
    localStorage.removeItem('active_workspace_id');
    // One-release shim: purge any tokens the pre-P2 build left in localStorage.
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
  }, []);

  const saveAuth = useCallback((data: AuthResponse['data']) => {
    setAccessToken(data.access_token);
    setApiToken(data.access_token); // in-memory source of truth for apiFetch
    setUser(data.user);
    if (data.workspaces) {
      setWorkspaces(data.workspaces);
      const active = findActiveWorkspace(data.workspaces);
      setActiveWorkspace(active);
      if (active) {
        localStorage.setItem('active_workspace_id', active.org_id);
      }
    }
    loadCapabilities();
  }, [loadCapabilities]);

  const refreshAuth = useCallback(async () => {
    // The refresh token normally rides in the httpOnly cookie. One-release shim:
    // an existing session may still have a refresh token in localStorage from the
    // pre-cookie build — send it once in the body to bootstrap the cookie, then
    // discard it so it's never replayed (a replay trips reuse detection).
    const legacyRefresh = localStorage.getItem('refresh_token');
    const activeOrgId = localStorage.getItem('active_workspace_id');
    try {
      const res = await fetch(`${API_URL}/api/auth/refresh`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': readCsrfToken() },
        body: JSON.stringify({
          ...(legacyRefresh ? { refresh_token: legacyRefresh } : {}),
          ...(activeOrgId ? { org_id: activeOrgId } : {}),
        }),
      });
      // The shim token is single-use — drop the legacy copies regardless of result.
      localStorage.removeItem('refresh_token');
      localStorage.removeItem('access_token');
      if (!res.ok) {
        clearAuth();
        return;
      }
      const json: AuthResponse = await res.json();
      if (json.data) {
        saveAuth(json.data);
      } else {
        clearAuth();
      }
    } catch {
      clearAuth();
    }
  }, [clearAuth, saveAuth]);

  useEffect(() => {
    const loadUser = async () => {
      // Skip on /auth/callback — that page bootstraps its own session (the OAuth
      // redirect already set the refresh cookie; the page reads the short-lived
      // access token from the URL).
      if (window.location.pathname === '/auth/callback') {
        setIsLoading(false);
        return;
      }
      // The access token lives only in memory, so on every fresh load/reload we
      // re-establish the session from the refresh cookie. refreshAuth scopes the
      // new token to the saved active workspace and populates user + workspaces.
      await refreshAuth();
      setIsLoading(false);
    };

    loadUser();
  }, [refreshAuth]);

  const login = async (email: string, password: string) => {
    const res = await fetch(`${API_URL}/api/auth/login`, {
      method: 'POST',
      credentials: 'include', // receive the httpOnly refresh + csrf cookies
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ email, password }),
    });

    const json: AuthResponse = await res.json();
    if (!res.ok || json.error) {
      throw new Error(json.error || 'Login failed');
    }

    saveAuth(json.data);
  };

  const register = async (data: RegisterData) => {
    const res = await fetch(`${API_URL}/api/auth/register`, {
      method: 'POST',
      credentials: 'include',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify(data),
    });

    const json: AuthResponse = await res.json();
    if (!res.ok || json.error) {
      throw new Error(json.error || 'Registration failed');
    }

    saveAuth(json.data);
  };

  const logout = async () => {
    try {
      await fetch(`${API_URL}/api/auth/logout`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': readCsrfToken() },
      });
    } catch {
      // Ignore network errors — local state is cleared regardless.
    }
    clearAuth();
  };

  const doSwitchWorkspace = async (orgId: string) => {
    const result = await apiSwitchWorkspace(orgId);
    setAccessToken(result.access_token);
    setApiToken(result.access_token);
    localStorage.setItem('active_workspace_id', orgId);
    if (result.workspaces) {
      setWorkspaces(result.workspaces);
      const active = result.workspaces.find(w => w.org_id === orgId) || null;
      setActiveWorkspace(active);
    }
    window.location.reload();
  };

  return (
    <AuthContext.Provider
      value={{
        user,
        accessToken,
        activeWorkspace,
        workspaces,
        currentRole,
        isLoading,
        isAuthenticated: !!user,
        capabilities,
        hasCapability,
        login,
        register,
        logout,
        refreshAuth,
        switchWorkspace: doSwitchWorkspace,
      }}
    >
      {children}
    </AuthContext.Provider>
  );
}

export function useAuth() {
  const context = useContext(AuthContext);
  if (context === undefined) {
    throw new Error('useAuth must be used within an AuthProvider');
  }
  return context;
}
