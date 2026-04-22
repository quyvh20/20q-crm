import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as Sentry from '@sentry/react';
import { AuthProvider, useAuth } from './lib/auth';
import AppLayout from './AppLayout';
import LoginPage from './pages/LoginPage';
import RegisterPage from './pages/RegisterPage';
import AuthCallbackPage from './pages/AuthCallbackPage';
import ContactsPage from './pages/ContactsPage';
import DealsPage from './pages/DealsPage';
import DealDetailPage from './pages/DealDetailPage';
import SettingsPage from './pages/SettingsPage';
import WorkspaceSettingsPage from './pages/WorkspaceSettingsPage';
import CustomObjectPage from './pages/CustomObjectPage';
import AcceptInvitePage from './pages/AcceptInvitePage';
import ConversationLogPage from './pages/ConversationLogPage';
import VoicePage from './pages/VoicePage';
import { WorkflowList } from './features/workflows/WorkflowList';
import { WorkflowBuilder } from './features/workflows/WorkflowBuilder';
import { RunHistory } from './features/workflows/RunHistory';

// Initialize Sentry
const SENTRY_DSN = import.meta.env.VITE_SENTRY_DSN;
if (SENTRY_DSN) {
  Sentry.init({
    dsn: SENTRY_DSN,
    integrations: [
      Sentry.browserTracingIntegration(),
      Sentry.replayIntegration(),
    ],
    tracesSampleRate: 1.0,
    replaysSessionSampleRate: 0.1,
    replaysOnErrorSampleRate: 1.0,
    environment: import.meta.env.MODE,
  });
}

const queryClient = new QueryClient({
  defaultOptions: { queries: { staleTime: 30_000, retry: 1 } },
});

function ProtectedRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading } = useAuth();

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
        <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full"></div>
      </div>
    );
  }

  if (!isAuthenticated) {
    return <Navigate to="/login" replace />;
  }

  return <>{children}</>;
}

function PublicRoute({ children }: { children: React.ReactNode }) {
  const { isAuthenticated, isLoading } = useAuth();

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-gradient-to-br from-slate-900 via-slate-800 to-slate-900">
        <div className="animate-spin h-8 w-8 border-4 border-blue-500 border-t-transparent rounded-full"></div>
      </div>
    );
  }

  if (isAuthenticated) {
    return <Navigate to="/" replace />;
  }

  return <>{children}</>;
}

function App() {
  return (
    <QueryClientProvider client={queryClient}>
    <Sentry.ErrorBoundary fallback={<p className="p-8 text-red-400">Something went wrong.</p>}>
      <BrowserRouter>
        <AuthProvider>
          <Routes>
            {/* Public routes */}
            <Route path="/login" element={<PublicRoute><LoginPage /></PublicRoute>} />
            <Route path="/register" element={<PublicRoute><RegisterPage /></PublicRoute>} />
            <Route path="/auth/callback" element={<AuthCallbackPage />} />
            <Route path="/accept-invite" element={<AcceptInvitePage />} />

            {/* Protected routes */}
            <Route path="/" element={
              <ProtectedRoute>
                <AppLayout />
              </ProtectedRoute>
            } />
            <Route path="/contacts" element={
              <ProtectedRoute>
                <AppLayout><ContactsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/deals" element={
              <ProtectedRoute>
                <AppLayout><DealsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/deals/:id" element={
              <ProtectedRoute>
                <AppLayout><DealDetailPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/settings" element={
              <ProtectedRoute>
                <AppLayout><SettingsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/settings/workspace" element={
              <ProtectedRoute>
                <AppLayout><WorkspaceSettingsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/objects/:slug" element={
              <ProtectedRoute>
                <AppLayout><CustomObjectPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/settings/ai-logs" element={
              <ProtectedRoute>
                <AppLayout><ConversationLogPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/voice" element={
              <ProtectedRoute>
                <AppLayout><VoicePage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows" element={
              <ProtectedRoute>
                <AppLayout><WorkflowList /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows/:id" element={
              <ProtectedRoute>
                <AppLayout><WorkflowBuilder /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows/:id/history" element={
              <ProtectedRoute>
                <AppLayout><RunHistory /></AppLayout>
              </ProtectedRoute>
            } />
          </Routes>
        </AuthProvider>
      </BrowserRouter>
    </Sentry.ErrorBoundary>
    </QueryClientProvider>
  );
}

export default App;
