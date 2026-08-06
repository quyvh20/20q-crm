import { Suspense } from 'react';
import { BrowserRouter, Routes, Route, Navigate } from 'react-router-dom';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import * as Sentry from '@sentry/react';
import { AuthProvider, useAuth } from './lib/auth';
import { Button, SpinnerBlock, Toaster } from '@/components/ui';
import { lazyRoute, lazyRouteNamed } from './lib/lazyRoute';
import AppLayout from './AppLayout';

// ── ROUTE CHUNKS ────────────────────────────────────────────────────────────
// Every page below is a separate lazily-loaded chunk. Before this the whole app
// was ONE 3.75 MB script, so a visitor sitting on /login downloaded the React
// Flow automation canvas, recharts, TipTap, the markdown editor and every Prism
// grammar before the password field was interactive.
//
// `lazyRoute` is React.lazy plus a retry-then-reload policy — see
// src/lib/lazyRoute.ts for why a split app needs one and a single-bundle app
// does not.
//
// AppLayout is deliberately NOT lazy. It is the parent of every authenticated
// page, so a lazy AppLayout would suspend BEFORE its lazy child ever rendered
// and serialise two round trips on every signed-in page load. Keeping it in the
// entry chunk costs unauthenticated visitors its weight once; making it lazy
// would cost every signed-in visitor an extra RTT on every cold page.

// Public / pre-workspace.
const LoginPage = lazyRoute(() => import('./pages/LoginPage'));
const TwoFactorChallengePage = lazyRoute(() => import('./pages/TwoFactorChallengePage'));
const RegisterPage = lazyRoute(() => import('./pages/RegisterPage'));
const ForgotPasswordPage = lazyRoute(() => import('./pages/ForgotPasswordPage'));
const ResetPasswordPage = lazyRoute(() => import('./pages/ResetPasswordPage'));
const VerifyEmailPage = lazyRoute(() => import('./pages/VerifyEmailPage'));
const AuthCallbackPage = lazyRoute(() => import('./pages/AuthCallbackPage'));
const AcceptInvitePage = lazyRoute(() => import('./pages/AcceptInvitePage'));
const PreferenceCenterPage = lazyRoute(() => import('./features/marketing/PreferenceCenterPage'));
const ConfirmSubscriptionPage = lazyRoute(() => import('./features/marketing/ConfirmSubscriptionPage'));
const TermsPage = lazyRoute(() => import('./pages/legal/TermsPage'));
const PrivacyPage = lazyRoute(() => import('./pages/legal/PrivacyPage'));
const ChooseWorkspacePage = lazyRoute(() => import('./pages/ChooseWorkspacePage'));
const NoWorkspacePage = lazyRoute(() => import('./pages/NoWorkspacePage'));
const EnrollTwoFactorPage = lazyRoute(() => import('./pages/EnrollTwoFactorPage'));

// Core records.
const DashboardPage = lazyRoute(() => import('./features/reports/DashboardPage'));
const ContactsPage = lazyRoute(() => import('./pages/ContactsPage'));
const DealsPage = lazyRoute(() => import('./pages/DealsPage'));
const DealDetailPage = lazyRoute(() => import('./pages/DealDetailPage'));
const CustomObjectPage = lazyRoute(() => import('./pages/CustomObjectPage'));
const ObjectRecordPage = lazyRoute(() => import('./pages/ObjectRecordPage'));
const VoicePage = lazyRoute(() => import('./pages/VoicePage'));
const AIPage = lazyRoute(() => import('./pages/AIPage'));
const SharedWithMePage = lazyRoute(() => import('./pages/SharedWithMePage'));
const TasksPage = lazyRoute(() => import('./pages/TasksPage'));

// Settings shell + sections. The shell and its index redirect live in the same
// module, so they resolve to the same chunk.
const SettingsLayout = lazyRoute(() => import('./pages/settings/SettingsLayout'));
const SettingsIndexRedirect = lazyRouteNamed(
  () => import('./pages/settings/SettingsLayout'),
  'SettingsIndexRedirect',
);
const ProfileSection = lazyRoute(() => import('./pages/settings/ProfileSection'));
const SecuritySection = lazyRoute(() => import('./pages/settings/SecuritySection'));
const ApiTokensSection = lazyRoute(() => import('./pages/settings/ApiTokensSection'));
const NotificationPreferencesSection = lazyRoute(() => import('./pages/settings/NotificationPreferencesSection'));
const WorkspaceGeneralSection = lazyRoute(() => import('./pages/settings/WorkspaceGeneralSection'));
const MembersSection = lazyRoute(() => import('./pages/settings/MembersSection'));
const GroupsSection = lazyRoute(() => import('./pages/settings/GroupsSection'));
const RolesManager = lazyRoute(() => import('./components/settings/RolesManager'));
const RoleDetailSection = lazyRoute(() => import('./pages/settings/RoleDetailSection'));
const PermissionsManager = lazyRoute(() => import('./components/settings/PermissionsManager'));
const FieldSecurityManager = lazyRoute(() => import('./components/settings/FieldSecurityManager'));
const StarterTemplateSection = lazyRoute(() => import('./pages/settings/StarterTemplateSection'));
const DuplicatesSection = lazyRoute(() => import('./pages/settings/DuplicatesSection'));
const LeadScoringSection = lazyRoute(() => import('./pages/settings/LeadScoringSection'));
const ObjectsManager = lazyRoute(() => import('./components/settings/ObjectsManager'));
const PipelineStagesManager = lazyRoute(() => import('./components/settings/PipelineStagesManager'));
const IntegrationsSection = lazyRoute(() => import('./pages/settings/IntegrationsSection'));
const DeliveryLogSection = lazyRoute(() => import('./pages/settings/DeliveryLogSection'));
const IntegrationSourceDetailSection = lazyRoute(() => import('./pages/settings/IntegrationSourceDetailSection'));
// The single heaviest route in the app: @uiw/react-md-editor drags in
// rehype-prism-plus, which imports refractor/all — every Prism grammar there is.
// ~630 kB gzip that used to be in the first byte every visitor downloaded, for
// one admin settings screen.
const KnowledgeBase = lazyRoute(() => import('./components/settings/KnowledgeBase'));
const AuditLogViewer = lazyRoute(() => import('./components/settings/AuditLogViewer'));
const ConversationLogPage = lazyRoute(() => import('./pages/ConversationLogPage'));

// Automations (@xyflow/react + dagre live here and nowhere else).
const WorkflowList = lazyRoute(() => import('./features/workflows/WorkflowList'));
const EmailTemplatesPage = lazyRoute(() => import('./features/workflows/EmailTemplatesPage'));
const EmailTemplateEditor = lazyRoute(() => import('./features/workflows/EmailTemplateEditor'));
const RunHistory = lazyRoute(() => import('./features/workflows/RunHistory'));
const NextBuilder = lazyRouteNamed(() => import('./features/workflows/builder/NextBuilder'), 'NextBuilder');

// Reports (recharts + d3).
const ReportsListPage = lazyRoute(() => import('./features/reports/ReportsListPage'));
const ReportBuilderPage = lazyRoute(() => import('./features/reports/ReportBuilderPage'));

// Marketing (TipTap + prosemirror + dnd-kit live here).
const MarketingOverviewPage = lazyRoute(() => import('./features/marketing/MarketingOverviewPage'));
const CampaignsListPage = lazyRoute(() => import('./features/marketing/CampaignsListPage'));
const CampaignComposerPage = lazyRoute(() => import('./features/marketing/CampaignComposerPage'));
const SequencesListPage = lazyRoute(() => import('./features/marketing/SequencesListPage'));
const SequenceDetailPage = lazyRoute(() => import('./features/marketing/SequenceDetailPage'));
const SegmentsPage = lazyRoute(() => import('./features/marketing/SegmentsPage'));
const SegmentEditorPage = lazyRoute(() => import('./features/marketing/SegmentEditorPage'));
const CampaignContentListPage = lazyRoute(() => import('./features/marketing/CampaignContentListPage'));
const CampaignContentEditor = lazyRoute(() => import('./features/marketing/CampaignContentEditor'));
const MediaLibraryPage = lazyRoute(() => import('./features/marketing/MediaLibraryPage'));
const SavedBlocksPage = lazyRoute(() => import('./features/marketing/SavedBlocksPage'));
const SenderProfilePage = lazyRoute(() => import('./features/marketing/SenderProfilePage'));
const SendingDomainsPage = lazyRoute(() => import('./features/marketing/SendingDomainsPage'));
const SuppressionListPage = lazyRoute(() => import('./features/marketing/SuppressionListPage'));
const MarketingConsentPage = lazyRoute(() => import('./features/marketing/MarketingConsentPage'));

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

/**
 * Fallback for a route chunk that is still in flight on a COLD load — a hard
 * navigation straight to a URL, where there is no previous screen to keep on
 * display. Public routes have no app chrome, so this is a full-viewport
 * spinner; authenticated routes get the finer-grained boundary inside
 * AppLayout, which keeps the sidebar and header up and spins only the content
 * area.
 *
 * In-app navigation does NOT reach either fallback: BrowserRouter wraps its
 * location updates in React.startTransition (react-router 7 default), so React
 * holds the CURRENT page on screen until the next chunk resolves instead of
 * tearing it down for a spinner.
 */
function RouteFallback() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-background">
      <SpinnerBlock />
    </div>
  );
}

/**
 * Last-resort screen. It has always caught render errors; splitting the bundle
 * gives it a new and much more likely customer — a chunk request that 404s
 * because the user is on a bundle we redeployed out from under them, and that
 * lazyRoute's reload guard has already tried once to fix.
 *
 * So it now offers the recovery instead of stating the problem: a reload is the
 * fix for the stale-deploy case, and this component is mounted OUTSIDE
 * BrowserRouter, so a reload is also the only navigation available to it.
 */
function AppErrorFallback() {
  return (
    <div className="min-h-screen flex items-center justify-center bg-background p-8">
      <div className="max-w-md text-center">
        <h1 className="text-xl font-semibold tracking-tight text-foreground">Something went wrong</h1>
        <p className="mt-2 text-sm text-muted-foreground">
          This page failed to load. If the app was updated while you had it open, reloading will pick
          up the new version.
        </p>
        <Button className="mt-6" onClick={() => window.location.reload()}>
          Reload the page
        </Button>
      </div>
    </div>
  );
}

function ProtectedRoute({ children, requireWorkspace = true }: { children: React.ReactNode; requireWorkspace?: boolean }) {
  const { isAuthenticated, isLoading, needsChooser, hasActiveWorkspace } = useAuth();

  if (isLoading) {
    return (
      <div className="min-h-screen flex items-center justify-center bg-background">
        <SpinnerBlock />
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
      <div className="min-h-screen flex items-center justify-center bg-background">
        <SpinnerBlock />
      </div>
    );
  }

  if (isAuthenticated) {
    // Honor a session-expiry return-to path (?next=/deals/123) so a re-login
    // lands back where the session died (U2). Same-origin paths only: must start
    // with a single '/' and contain no backslash — browsers normalize '\' to '/'
    // so '/\evil.com' would resolve cross-origin (open-redirect guard).
    const next = new URLSearchParams(window.location.search).get('next');
    const safeNext =
      next && next.startsWith('/') && !next.startsWith('//') && !next.includes('\\') ? next : '/';
    return <Navigate to={safeNext} replace />;
  }

  return <>{children}</>;
}

function App() {
  return (
    <QueryClientProvider client={queryClient}>
    <Sentry.ErrorBoundary fallback={<AppErrorFallback />}>
      <BrowserRouter>
        <AuthProvider>
          {/* R7.4: the toast viewport lives OUTSIDE this <Suspense> on purpose.
              Every authenticated route is `<AppLayout><LazyPage/></AppLayout>`
              under this one boundary, so navigating to a not-yet-loaded route
              swaps the whole subtree — AppLayout included — for RouteFallback.
              A viewport mounted in AppLayout would therefore be torn down
              exactly when a "Saved" toast fired by the page you are leaving
              still needs to be read. Toast STATE is module-level (lib/useToast)
              and outlives this anyway; this keeps the DOM that renders it alive
              too. Inside the router so router-aware toast actions stay legal. */}
          <Toaster />
          <Suspense fallback={<RouteFallback />}>
          <Routes>
            {/* Public routes */}
            <Route path="/login" element={<PublicRoute><LoginPage /></PublicRoute>} />
            {/* The 2FA login challenge (U6.4). Public because the caller has no
                session yet — only a challenge. Reached from LoginPage (challenge in
                router state) or straight from the Google callback (challenge in an
                httpOnly cookie). PublicRoute bounces you out once verify succeeds. */}
            <Route path="/login/2fa" element={<PublicRoute><TwoFactorChallengePage /></PublicRoute>} />
            <Route path="/register" element={<PublicRoute><RegisterPage /></PublicRoute>} />
            <Route path="/forgot-password" element={<PublicRoute><ForgotPasswordPage /></PublicRoute>} />
            {/* reset/verify are token-bearing links — left UNWRAPPED so a logged-in
                user clicking an emailed link still lands on the flow instead of
                being bounced to the dashboard by PublicRoute. */}
            <Route path="/reset-password" element={<ResetPasswordPage />} />
            <Route path="/verify-email" element={<VerifyEmailPage />} />
            <Route path="/auth/callback" element={<AuthCallbackPage />} />
            <Route path="/accept-invite" element={<AcceptInvitePage />} />
            {/* PUBLIC one-click unsubscribe / preference center (M3). Token-bearing,
                so — like /reset-password — left UNWRAPPED: a logged-in admin clicking
                their own emailed unsubscribe link must still land here, not be bounced
                to the dashboard. It calls only the public token endpoint. */}
            <Route path="/u/:token" element={<PreferenceCenterPage />} />
            <Route path="/marketing/confirm/:token" element={<ConfirmSubscriptionPage />} />

            {/* Legal pages (U7.6): the fallback destinations for the signup
                consent line when the operator hasn't pointed VITE_TERMS_URL /
                VITE_PRIVACY_URL at their real ones. Deliberately NOT wrapped in
                PublicRoute — that redirects an authenticated user away, and
                someone who is already signed in must still be able to read the
                terms they agreed to. */}
            <Route path="/terms" element={<TermsPage />} />
            <Route path="/privacy" element={<PrivacyPage />} />

            {/* Authenticated but pre-workspace: the R2 chooser + zero-membership
                dead-end. requireWorkspace={false} so they don't redirect to themselves. */}
            <Route path="/choose-workspace" element={
              <ProtectedRoute requireWorkspace={false}><ChooseWorkspacePage /></ProtectedRoute>
            } />
            <Route path="/no-workspace" element={
              <ProtectedRoute requireWorkspace={false}><NoWorkspacePage /></ProtectedRoute>
            } />
            {/* Forced 2FA enrollment (U6.4): a real session that the workspace policy
                confines to enrolling — every other /api/* route 403s with
                `two_factor_required` and apiFetch parks them here.
                requireWorkspace={false} so the chooser/dead-end guards can't bounce
                them out of the one page they're allowed on. */}
            <Route path="/enroll-2fa" element={
              <ProtectedRoute requireWorkspace={false}><EnrollTwoFactorPage /></ProtectedRoute>
            } />

            {/* Protected routes.
                `title` on AppLayout drives the tab title (U7.2). It is set here
                for every STATIC page and deliberately OMITTED for the pages whose
                title is data — a deal, a record, a workflow, a report, a settings
                section — which call useDocumentTitle themselves once loaded. */}
            <Route path="/" element={
              <ProtectedRoute>
                <AppLayout title="Dashboard"><DashboardPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/contacts" element={
              <ProtectedRoute>
                <AppLayout title="Contacts"><ContactsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/deals" element={
              <ProtectedRoute>
                <AppLayout title="Deals"><DealsPage /></AppLayout>
              </ProtectedRoute>
            } />
            {/* No title: DealDetailPage titles the tab with the loaded deal name. */}
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
              <Route path="profile" element={<ProfileSection />} />
              <Route path="security" element={<SecuritySection />} />
              <Route path="api-tokens" element={<ApiTokensSection />} />
              <Route path="notifications" element={<NotificationPreferencesSection />} />
              <Route path="general" element={<WorkspaceGeneralSection />} />
              <Route path="members" element={<MembersSection />} />
              <Route path="groups" element={<GroupsSection />} />
              <Route path="roles" element={<RolesManager />} />
              {/* One role's whole story — capabilities, effective object/field
                  access, scope, members — on a single page (U3.1). */}
              <Route path="roles/:id" element={<RoleDetailSection />} />
              <Route path="object-access" element={<PermissionsManager />} />
              <Route path="field-access" element={<FieldSecurityManager />} />
              <Route path="starter-templates" element={<StarterTemplateSection />} />
              <Route path="duplicates" element={<DuplicatesSection />} />
              <Route path="lead-scoring" element={<LeadScoringSection />} />
              <Route path="objects" element={<ObjectsManager />} />
              <Route path="pipeline" element={<PipelineStagesManager />} />
              <Route path="integrations" element={<IntegrationsSection />} />
              {/* Flat sibling, matching roles/:id. The section entry above is what
                  lets the deep-link guard through; this route owns its own tab title. */}
              {/* Before the :id route: "deliveries" would otherwise be captured as a
                  source id and 404 against a uuid lookup. */}
              <Route path="integrations/deliveries" element={<DeliveryLogSection />} />
              <Route path="integrations/:id" element={<IntegrationSourceDetailSection />} />
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
                <AppLayout title="Voice Notes"><VoicePage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/ai" element={
              <ProtectedRoute>
                <AppLayout title="AI Assistant"><AIPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/workflows" element={
              <ProtectedRoute>
                <AppLayout title="Automations"><WorkflowList /></AppLayout>
              </ProtectedRoute>
            } />
            {/* A5: email templates library. Static segments outrank "/workflows/:id"
                in React Router v6 ranking, so these resolve before the builder. */}
            <Route path="/workflows/email-templates" element={
              <ProtectedRoute>
                <AppLayout title="Email Templates"><EmailTemplatesPage /></AppLayout>
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
            <Route path="/shared-with-me" element={
              <ProtectedRoute>
                <AppLayout title="Shared with me"><SharedWithMePage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/tasks" element={
              <ProtectedRoute>
                <AppLayout title="Tasks"><TasksPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/reports" element={
              <ProtectedRoute>
                <AppLayout title="Reports"><ReportsListPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/suppressions" element={
              <ProtectedRoute>
                <AppLayout title="Suppression list"><SuppressionListPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/consent" element={
              <ProtectedRoute>
                <AppLayout title="Marketing lawful basis"><MarketingConsentPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/domains" element={
              <ProtectedRoute>
                <AppLayout title="Sending domains"><SendingDomainsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/content" element={
              <ProtectedRoute>
                <AppLayout title="Email templates"><CampaignContentListPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/media" element={
              <ProtectedRoute>
                <AppLayout title="Media library"><MediaLibraryPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/saved-blocks" element={
              <ProtectedRoute>
                <AppLayout title="Saved blocks"><SavedBlocksPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/sender-profile" element={
              <ProtectedRoute>
                <AppLayout title="Sender profile"><SenderProfilePage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/audiences" element={
              <ProtectedRoute>
                <AppLayout title="Audiences"><SegmentsPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/audiences/:id" element={
              <ProtectedRoute>
                <AppLayout title="Audience"><SegmentEditorPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/overview" element={
              <ProtectedRoute>
                <AppLayout title="Marketing health"><MarketingOverviewPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/campaigns" element={
              <ProtectedRoute>
                <AppLayout title="Campaigns"><CampaignsListPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/campaigns/:id" element={
              <ProtectedRoute>
                <AppLayout title="Campaign"><CampaignComposerPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/sequences" element={
              <ProtectedRoute>
                <AppLayout title="Sequences"><SequencesListPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/sequences/:id" element={
              <ProtectedRoute>
                <AppLayout title="Sequence"><SequenceDetailPage /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/content/new" element={
              <ProtectedRoute>
                <AppLayout title="New email content"><CampaignContentEditor /></AppLayout>
              </ProtectedRoute>
            } />
            <Route path="/marketing/content/:id" element={
              <ProtectedRoute>
                <AppLayout title="Email content"><CampaignContentEditor /></AppLayout>
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
          </Suspense>
        </AuthProvider>
      </BrowserRouter>
    </Sentry.ErrorBoundary>
    </QueryClientProvider>
  );
}

export default App;
