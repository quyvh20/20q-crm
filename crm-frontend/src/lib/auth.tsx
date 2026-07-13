import React, { createContext, useContext, useState, useEffect, useCallback } from 'react';
import { switchWorkspace as apiSwitchWorkspace, setAccessToken as setApiToken, readCsrfToken, getMyPermissions, parseJsonSafe, type Workspace, type MyPermissions, type DataScope, type ObjectAccessBits } from './api';

const API_URL = import.meta.env.VITE_API_URL ?? (import.meta.env.DEV ? 'http://localhost:8080' : '');

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
  // Personal preferences (U2 My Account).
  timezone?: string;
  locale?: string;
  onboarding_completed?: boolean;
}

interface AuthContextType {
  user: User | null;
  accessToken: string | null;
  activeWorkspace: Workspace | null;
  workspaces: Workspace[];
  currentRole: string;
  isLoading: boolean;
  isAuthenticated: boolean;
  // R2 multi-org state (P4). needsChooser drives /choose-workspace; defaultOrgId
  // marks the user's home workspace (the switcher star); hasActiveWorkspace is
  // false for the zero-membership dead-end.
  needsChooser: boolean;
  defaultOrgId: string | null;
  hasActiveWorkspace: boolean;
  // Effective system capabilities of the current user in the active workspace
  // (P3). Owner gets all. Use hasCapability (or usePermissions().can) for
  // permission-aware UI.
  capabilities: string[];
  hasCapability: (code: string) => boolean;
  // True once the capability fetch has settled (success OR failure). Until
  // then hasCapability returns false for everything, so capability-driven
  // navigation (the settings shell guard) must wait instead of redirecting a
  // deep-linked admin off a page they're actually allowed on (U1).
  permsLoaded: boolean;
  // Role identity + row scope for the active workspace (P6). dataScope 'own' means
  // the caller only sees records they own/were shared; roleId is the authoritative
  // role identity; isOwner marks god-mode.
  dataScope: DataScope;
  roleId: string;
  isOwner: boolean;
  // Caller's per-object OLS bits (U3.7): slug → {read,create,edit,delete}.
  // null means UNKNOWN (perms not loaded yet, or an older server that doesn't
  // send the map) — canAccessObject fails OPEN on unknown so buttons don't
  // flash hidden for allowed users; the server still enforces every action.
  objectAccess: Record<string, ObjectAccessBits> | null;
  canAccessObject: (slug: string, action: 'read' | 'create' | 'edit' | 'delete') => boolean;
  login: (email: string, password: string) => Promise<void>;
  register: (data: RegisterData) => Promise<void>;
  logout: () => Promise<void>;
  refreshAuth: () => Promise<void>;
  switchWorkspace: (orgId: string, setDefault?: boolean) => Promise<void>;
  // Merge a profile edit into the in-memory user so the header/sidebar update
  // immediately after a PATCH /me, without a full token refresh (U2).
  setUserProfile: (patch: Partial<User>) => void;
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
    // R2 org-selection contract (P3/P4). active_org_id is the org the token is
    // bound to (empty ⇒ zero-membership dead-end); needs_chooser ⇒ show the chooser.
    active_org_id?: string;
    default_org_id?: string;
    needs_chooser?: boolean;
  };
  error: string | null;
}

const AuthContext = createContext<AuthContextType | undefined>(undefined);

export function AuthProvider({ children }: { children: React.ReactNode }) {
  const [user, setUser] = useState<User | null>(null);
  // Access token is mirrored here for context consumers, but the source of truth
  // is the in-memory token in api.ts (setApiToken). It is never persisted (P2).
  const [accessToken, setAccessToken] = useState<string | null>(null);
  const [workspaces, setWorkspaces] = useState<Workspace[]>([]);
  const [activeWorkspace, setActiveWorkspace] = useState<Workspace | null>(null);
  const [needsChooser, setNeedsChooser] = useState(false);
  const [defaultOrgId, setDefaultOrgId] = useState<string | null>(null);
  const [perms, setPerms] = useState<MyPermissions | null>(null);
  const [isLoading, setIsLoading] = useState(true);
  const [permsLoaded, setPermsLoaded] = useState(false);

  const currentRole = activeWorkspace?.role || '';
  const capabilities = perms?.capabilities ?? [];
  const dataScope: DataScope = perms?.data_scope ?? 'all';
  const roleId = perms?.role_id ?? '';
  const isOwner = perms?.is_owner ?? false;
  const hasCapability = useCallback((code: string) => (perms?.capabilities ?? []).includes(code), [perms]);
  const objectAccess = perms?.objects ?? null;
  // Unknown (null map) → visible: capability gates fail closed, but record
  // buttons fail open until the OLS map arrives so allowed users never see
  // their Edit button flash away. Known map + missing slug → denied.
  const canAccessObject = useCallback(
    (slug: string, action: 'read' | 'create' | 'edit' | 'delete') => {
      if (perms?.is_owner) return true;
      const objects = perms?.objects;
      if (!objects) return true;
      return !!objects[slug]?.[action];
    },
    [perms],
  );

  // Fetch the caller's effective permissions (capabilities + role identity + row
  // scope) for the active org after auth changes. Fire-and-forget: on failure
  // perms stay null (UI fails closed — no capabilities, 'all' scope is inert
  // without them).
  const loadCapabilities = useCallback(() => {
    getMyPermissions()
      .then(setPerms)
      .catch(() => setPerms(null))
      .finally(() => setPermsLoaded(true));
  }, []);

  // clearAuth splits (P4): a hard logout (full=true) forgets everything, but a
  // failed refresh (full=false) KEEPS active_workspace_id so the next refresh
  // re-scopes to the same org — the durable home is the server-side default_org_id.
  const clearAuth = useCallback((full = true) => {
    setUser(null);
    setAccessToken(null);
    setApiToken(null);
    setWorkspaces([]);
    setActiveWorkspace(null);
    setNeedsChooser(false);
    setDefaultOrgId(null);
    setPerms(null);
    setPermsLoaded(false);
    if (full) localStorage.removeItem('active_workspace_id');
    // One-release shim: purge any tokens the pre-P2 build left in localStorage.
    localStorage.removeItem('access_token');
    localStorage.removeItem('refresh_token');
  }, []);

  const saveAuth = useCallback((data: AuthResponse['data']) => {
    setAccessToken(data.access_token);
    setApiToken(data.access_token); // in-memory source of truth for apiFetch
    setUser(data.user);
    setNeedsChooser(!!data.needs_chooser);
    setDefaultOrgId(data.default_org_id ?? null);
    const list = data.workspaces ?? [];
    setWorkspaces(list);
    // Trust the server's active_org_id rather than inferring from localStorage /
    // array order (P4) — this is what kills the "UI shows org A, token is org B"
    // class of bug. active_org_id empty ⇒ zero-membership dead-end.
    const active = data.active_org_id ? list.find(w => w.org_id === data.active_org_id) ?? null : null;
    setActiveWorkspace(active);
    if (active) {
      localStorage.setItem('active_workspace_id', active.org_id);
      // Remember the name too, so if this org later becomes unavailable (a 409 on
      // refresh) the chooser / dead-end can say WHICH workspace was lost (P4 nicety).
      localStorage.setItem('active_workspace_name', active.org_name);
    } else {
      localStorage.removeItem('active_workspace_id');
      localStorage.removeItem('active_workspace_name');
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
    const post = (orgId: string | null) =>
      fetch(`${API_URL}/api/auth/refresh`, {
        method: 'POST',
        credentials: 'include',
        headers: { 'Content-Type': 'application/json', 'X-CSRF-Token': readCsrfToken() },
        body: JSON.stringify({
          ...(legacyRefresh ? { refresh_token: legacyRefresh } : {}),
          ...(orgId ? { org_id: orgId } : {}),
        }),
      });
    try {
      let res = await post(activeOrgId);
      // The shim token is single-use — drop the legacy copies regardless of result.
      localStorage.removeItem('refresh_token');
      localStorage.removeItem('access_token');
      // 409 ORG_UNAVAILABLE: the saved active org is gone. Remember its name so the
      // chooser / dead-end can explain WHICH workspace was lost, then retry a plain
      // refresh into the default/first org; saveAuth then flags needs_chooser so the
      // AuthProvider routes to the chooser (P4).
      if (res.status === 409) {
        const lostName = localStorage.getItem('active_workspace_name');
        if (lostName) sessionStorage.setItem('lost_workspace_name', lostName);
        localStorage.removeItem('active_workspace_id');
        localStorage.removeItem('active_workspace_name');
        res = await post(null);
      }
      if (!res.ok) {
        clearAuth(false); // keep active_workspace_id across a failed refresh
        return;
      }
      const json = (await parseJsonSafe(res)) as AuthResponse;
      if (json.data) {
        saveAuth(json.data);
      } else {
        clearAuth(false);
      }
    } catch {
      clearAuth(false);
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

  const doSwitchWorkspace = async (orgId: string, setDefault = false) => {
    const result = await apiSwitchWorkspace(orgId, setDefault);
    setAccessToken(result.access_token);
    setApiToken(result.access_token);
    localStorage.setItem('active_workspace_id', result.active_org_id || orgId);
    setNeedsChooser(false);
    if (result.default_org_id) setDefaultOrgId(result.default_org_id);
    // Hard reload so the whole app (queries, capabilities) re-establishes against
    // the new org. On reload, refreshAuth re-scopes to active_workspace_id.
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
        needsChooser,
        defaultOrgId,
        hasActiveWorkspace: !!activeWorkspace,
        capabilities,
        hasCapability,
        permsLoaded,
        dataScope,
        roleId,
        isOwner,
        objectAccess,
        canAccessObject,
        login,
        register,
        logout,
        refreshAuth,
        switchWorkspace: doSwitchWorkspace,
        setUserProfile: (patch) => setUser((cur) => (cur ? { ...cur, ...patch } : cur)),
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

// usePermissions is the single hook the permission-aware UI reads (P6): the
// caller's capabilities + role identity + row scope, plus can(code) for gating.
// It's a thin projection over the auth context so gates read
// `usePermissions().can('members.manage')` instead of hardcoded role-name checks.
// U3.7 adds: `loaded` (capability fetch settled — gate skeletons on it, the
// SettingsLayout trap), `canAccess(slug, action)` for record-level buttons
// (fails open while unknown; the server always enforces), and the raw
// `objects` OLS map for callers that need to enumerate.
export function usePermissions() {
  const { capabilities, dataScope, roleId, currentRole, isOwner, hasCapability, permsLoaded, objectAccess, canAccessObject } = useAuth();
  return {
    capabilities,
    dataScope,
    roleId,
    roleName: currentRole,
    isOwner,
    can: hasCapability,
    loaded: permsLoaded,
    objects: objectAccess,
    canAccess: canAccessObject,
  };
}
