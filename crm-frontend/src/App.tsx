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
import SettingsLayout, { SettingsIndexRedirect } from './pages/settings/SettingsLayout';
import MembersSection from './pages/settings/MembersSection';
import GroupsSection from './pages/settings/GroupsSection';
import SecuritySessions from './components/settings/SecuritySessions';
import PipelineStagesManager from './components/settings/PipelineStagesManager';
import ObjectsManager from './components/settings/ObjectsManager';
import KnowledgeBase from './components/settings/KnowledgeBase';
import RolesManager from './components/settings/RolesManager';
import PermissionsManager from './components/settings/PermissionsManager';
import FieldSecurityManager from './components/settings/FieldSecurityManager';
import AuditLogViewer from './components/settings/AuditLogViewer';
import CustomObjectPage from './pages/CustomObjectPage';
import ObjectRecordPage from './pages/ObjectRecordPage';
import AcceptInvitePage from './pages/AcceptInvitePage';
import ChooseWorkspacePage from './pages/ChooseWorkspacePage';
import NoWorkspacePage from './pages/NoWorkspacePage';
import ForgotPasswordPage from './pages/ForgotPasswordPage';
import ResetPasswordPage from './pages/ResetPasswordPage';
import VerifyEmailPage from './pages/VerifyEmailPage';
import ConversationLogPage from './pages/ConversationLogPage';
import VoicePage from './pages/VoicePage';
import { WorkflowList } from './features/workflows/WorkflowList';
import { NextBuilder } from './features/workflows/builder/NextBuilder';
import { BuilderDemo } from './features/workflows/builder/__demo__/BuilderDemo';
import { RunHistory } from './features/workflows/RunHistory';
import { EmailTemplatesPage } from './features/workflows/EmailTemplatesPage';
import { EmailTemplateEditor } from './features/workflows/EmailTemplateEditor';
import AIPage from './pages/AIPage';
import ReportsListPage from './features/reports/ReportsListPage';
import ReportBuilderPage from './features/reports/ReportBuilderPage';
import DashboardPage from './features/reports/DashboardPage';

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

function ProtectedRoute({ children, requireWorkspace = true }: { children: React.ReactNode; requireWorkspace?: boolean }) {
  const { isAuthenticated, isLoading, needsChooser, hasActiveWorkspace } = useAuth();

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

  // R2 workspace gate (P4): a multi-org user with no resolved default chooses one;
  // a user with no active workspace lands on the dead-end. The chooser / dead-end
  // pages themselves pass requireWorkspace={false} to avoid a redirect loop.
  if (requireWorkspace) {
    if (needsChooser) {
      return <Navigate to="/choose-workspace" replace />;
    }
    if (!hasActiveWorkspace) {
      return <Navigate to="/no-workspace" replace />;
    }
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
            {/* TEMP A3 visual-verification harness — remove after verifying. */}
            <Route path="/builder-demo" element={<BuilderDemo />} />
            {/* Public routes */}
            <Route path="/login" element={<PublicRoute><LoginPage /></PublicRoute>} />
            <Route path="/register" element={<PublicRoute><RegisterPage /></PublicRoute>} />
            <Route path="/forgot-password" element={<PublicRoute><ForgotPasswordPage /></PublicRoute>} />
            {/* reset/verify are token-bearing links — left UNWRAPPED so a logged-in
                user clicking an emailed link still lands on the flow instead of
                being bounced to the dashboard by PublicRoute. */}
            <Route path="/reset-password" element={<ResetPasswordPage />} />
            <Route path="/verify-email" element={<VerifyEmailPage />} />
            <Route path="/auth/callback" element={<AuthCallbackPage />} />
            <Route path="/accept-invite" element={<AcceptInvitePage />} />

            {/* Authenticated but pre-workspace: the R2 chooser + zero-membership
                dead-end. requireWorkspace={false} so they don't redirect to themselves. */}
            <Route path="/choose-workspace" element={
              <ProtectedRoute requireWorkspace={false}><ChooseWorkspacePage /></ProtectedRoute>
            } />
            <Route path="/no-workspace" element={
              <ProtectedRoute requireWorkspace={false}><NoWorkspacePage /></ProtectedRoute>
            } />

            {/* Protected routes */}
            <Route path="/" element={
              <ProtectedRoute>
                <AppLayout><DashboardPage /></AppLayout>
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
            {/* Unified settings shell (U1): one routed area, grouped nav,
                every section deep-linkable. Old destinations redirect:
                /settings → default section, /settings/workspace → members. */}
            <Route path="/settings" element={
              <ProtectedRoute>
                <AppLayout><SettingsLayout /></AppLayout>
              </ProtectedRoute>
            }>
              <Route index element={<SettingsIndexRedirect />} />
              <Route path="security" element={<SecuritySessions />} />
              <Route path="members" element={<MembersSection />} />
              <Route path="groups" element={<GroupsSection />} />
              <Route path="roles" element={<RolesManager />} />
              <Route path="object-access" element={<PermissionsManager />} />
              <Route path="field-access" element={<FieldSecurityManager />} />
              <Route path="objects" element={<ObjectsManager />} />
              <Route path="pipeline" element={<PipelineStagesManager />} />
              <Route path="knowledge" element={<KnowledgeBase />} />
              <Route path="audit" element={<AuditLogViewer />} />
              <Route path="ai-logs" element={<ConversationLogPage />} />
              {/* Old /settings/workspace links: SettingsLayout's guard redirects
                  any unknown segment to the member's default section, which for
                  anyone who could use the old page IS Members. */}
            </Route>
            <Route path="/objects/:slug" element={
              <ProtectedRoute>
                <AppLayout><CustomObjectPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/objects/:slug/records/:id" element={
              <ProtectedRoute>
                <AppLayout><ObjectRecordPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/voice" element={
              <ProtectedRoute>
                <AppLayout><VoicePage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/ai" element={
              <ProtectedRoute>
                <AppLayout><AIPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows" element={
              <ProtectedRoute>
                <AppLayout><WorkflowList /></AppLayout>
              </ProtectedRoute>
            } />
            {/* A5: email templates library. Static segments outrank "/workflows/:id"
                in React Router v6 ranking, so these resolve before the builder. */}
            <Route path="/workflows/email-templates" element={
              <ProtectedRoute>
                <AppLayout><EmailTemplatesPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows/email-templates/new" element={
              <ProtectedRoute>
                <AppLayout><EmailTemplateEditor /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows/email-templates/:id" element={
              <ProtectedRoute>
                <AppLayout><EmailTemplateEditor /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows/:id/history" element={
              <ProtectedRoute>
                <AppLayout><RunHistory /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows/:id" element={
              <ProtectedRoute>
                <AppLayout><NextBuilder /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/reports" element={
              <ProtectedRoute>
                <AppLayout><ReportsListPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/reports/new" element={
              <ProtectedRoute>
                <AppLayout><ReportBuilderPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/reports/:id" element={
              <ProtectedRoute>
                <AppLayout><ReportBuilderPage /></AppLayout>
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
