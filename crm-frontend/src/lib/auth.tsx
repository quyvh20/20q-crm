import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import { switchWorkspace as apiSwitchWorkspace, type Workspace } from './api';

const API_URL = import.meta.env.VITE_API_URL || 'http://localhost:8080';

interface User {
  id: string;
  email: string;
  first_name: string;
  last_name: string;
  full_name?: string;
  role?: string;
  avatar_url?: string;
}

interface AuthContextType {
  user: User | null;
  accessToken: string | null;
  activeWorkspace: Workspace | null;
  workspaces: Workspace[];
  currentRole: string;
  isLoading: boolean;
  isAuthenticated: boolean;
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

interface MeResponse {
  data: {
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
  const [accessToken, setAccessToken] = useState<string | null>(
    localStorage.getItem('access_token')
  );
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [activeWorkspace, setActiveWorkspace] = useState<Workspace | null>(null);
  const [isLoading, setIsLoading] = useState(true);

  const currentRole = activeWorkspace?.role || '';

  const clearAuth = useCallback(() => {
    setUser(null);
    setAccessToken(null);
    setWorkspaces([]);
    setActiveWorkspace(null);
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
    localStorage.removeItem('active_workspace_id');
  }, []);

  const saveAuth = useCallback((data: AuthResponse['data']) => {
    setAccessToken(data.access_token);
    setUser(data.user);
    localStorage.setItem('access_token', data.access_token);
    localStorage.setItem('refresh_token', data.refresh_token);
    if (data.workspaces) {
      setWorkspaces(data.workspaces);
      const active = findActiveWorkspace(data.workspaces);
      setActiveWorkspace(active);
      if (active) {
        localStorage.setItem('active_workspace_id', active.org_id);
      }
    }
  }, []);

  const refreshAuth = useCallback(async () => {
    const refreshToken = localStorage.getItem('refresh_token');
    if (!refreshToken) {
      clearAuth();
      return;
    }

    try {
      const res = await fetch(`${API_URL}/api/auth/refresh`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ refresh_token: refreshToken }),
      });

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
      const token = localStorage.getItem('access_token');
      if (!token) {
        setIsLoading(false);
        return;
      }

      try {
        const res = await fetch(`${API_URL}/api/auth/me`, {
          headers: { Authorization: `Bearer ${token}` },
        });

        if (res.ok) {
          const json: MeResponse = await res.json();
          if (json.data) {
            setUser(json.data.user);
            setAccessToken(token);
            if (json.data.workspaces) {
              setWorkspaces(json.data.workspaces);
              const active = findActiveWorkspace(json.data.workspaces);
              setActiveWorkspace(active);
            }
          }
        } else if (res.status === 401) {
          await refreshAuth();
        } else {
          clearAuth();
        }
      } catch {
        clearAuth();
      } finally {
        setIsLoading(false);
      }
    };

    loadUser();
  }, [clearAuth, refreshAuth]);

  const login = async (email: string, password: string) => {
    const res = await fetch(`${API_URL}/api/auth/login`, {
      method: 'POST',
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
    const refreshToken = localStorage.getItem('refresh_token');
    if (refreshToken) {
      try {
        await fetch(`${API_URL}/api/auth/logout`, {
          method: 'POST',
          headers: { 'Content-Type': 'application/json' },
          body: JSON.stringify({ refresh_token: refreshToken }),
        });
      } catch {
        // pass
      }
    }
    clearAuth();
  };

  const doSwitchWorkspace = async (orgId: string) => {
    const result = await apiSwitchWorkspace(orgId);
    localStorage.setItem('access_token', result.access_token);
    localStorage.setItem('refresh_token', result.refresh_token);
    localStorage.setItem('active_workspace_id', orgId);
    setAccessToken(result.access_token);
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
