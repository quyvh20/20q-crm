# User System Improvement Plan (Permissions, Settings, Accounts)

**Goal:** make the user system — permissions, settings, member management, and personal accounts — feel like a polished modern SaaS (HubSpot/Attio/Linear class): simple for end users, understandable for admins, and honest everywhere (the UI never promises something the backend doesn't enforce).

**Where this sits:** the auth/RBAC overhaul (auth_system_improvement_plan.md, P0–P9) built a strong *engine* — capability catalog, OLS, FLS, own-scope, sessions, audit, invitations, multi-org. This plan is about the *product* on top of that engine. A 7-agent audit (2026-07-12) found the engine is largely sound but the surface is fractured, several UI promises are unbacked by the server, and an entire audience (the non-admin end user) has no home at all.

---

## 1. Verified headline defects (drive the priority order)

These were adversarially re-verified in code, not just reported:

1. **Legacy read routes bypass OLS/FLS.** `GET /api/contacts`, `/companies`, `/deals` (list + by-id) carry no `olsOn(..., ActionRead)` middleware — only the write routes do (crm-backend/internal/delivery/http/router.go:186-187, 197-198, 215-216) — and their usecases never apply FieldMask. The frontend's main list pages still call these paths. Concretely: revoke "read" on Contacts for a role, or mark a field Hidden in Field Security — that role still sees every contact and the hidden value. **The permission grids promise enforcement the API only delivers on the /registry path.**
2. **Member removal fakes data reassignment.** The "Reassign & Delete" modal collects a new owner, but `RemoveMember` only validates the field is present and then deletes the membership (crm-backend/internal/usecase/workspace_usecase.go:621-627, comment: "We'll trust the input for now for the mock"). Records silently stay owned by the ex-member; user-targeted report_shares/record_shares/group memberships are never revoked (so re-invite silently restores access).
3. **Phantom capabilities.** `org.settings` and `billing.manage` appear in the roles grid as sensitive toggles but gate zero routes (grep: only domain definitions + a test). Admins can "revoke billing access" and change nothing.

Supporting defects from the audit (spot-checked by the auditors, high confidence): `record_shares.permission_level` is decorative (a "read" share is fully editable by an own-scoped grantee, scopes.go:55 + share_usecase.go:47-50); invite emails name the workspace by raw UUID (workspace_usecase.go:220); an admin can remove `roles.manage` from their own role and lock everyone but the owner out of permissions (no guard in role_usecase.go SetCapabilities); `GET /api/pipeline/forecast` has no `analytics.view` gate while the AI tool does (router.go:231); the AI Logs tab is shown under `roles.manage` but the page requires `members.manage` (SettingsPage.tsx:37-42 vs ConversationLogPage.tsx:31); a user with no workspace is stranded in a redirect loop (NoWorkspacePage → /register → PublicRoute bounce → /no-workspace) with no endpoint to create a workspace for an existing account.

## 2. The three structural problems

1. **No personal account surface exists.** No profile page, no change-name/avatar/email, no in-app change-password (sign out and use "forgot password" is the only path), no theme toggle (a full `.dark` palette ships as dead CSS), no timezone/locale, no notification preferences. On mobile the sidebar is hidden with no drawer — you cannot even sign out. The header avatar is a decorative blank circle.
2. **Settings is two disconnected pages with no IA.** `/settings` (emoji tabs, component-state tab selection — refresh resets, no deep links) and `/settings/workspace` ("Members" in the sidebar, "Workspace Settings" as the title). Personal Security sits between org-wide Templates and Audit Log. Admin tabs render fully editable UIs to viewers who only learn they can't save from a server error. Ten native `window.confirm()`/`alert()` calls, three icon systems, two hardcoded-light-mode islands (ObjectsManager, KnowledgeBase), a permanent "coming soon" Templates tab, unconfirmed destructive deletes (custom fields, layouts, pipeline stages).
3. **The permission model is powerful but unexplainable.** Four layers (capabilities × OLS × FLS × data scope) across four grids on two pages with *opposite pivots* (OLS: pick role → objects×actions; FLS: pick object → fields×roles). No effective-access preview, no "view as", no cross-links between roles and the members who hold them, RBAC jargon in the copy ("default-deny", "God-mode", "Row scope"), an FLS grid that becomes 180 raw `<select>`s on a 30-field object, and a `usePermissions` hook with zero consumers.

## 3. Phases

Sequenced so trust comes first, the structural container second, then the two audiences (end user, admin), then lifecycle depth. U0–U2 are cheap relative to impact; U6 is the only backend-heavy expansion.

### U0 — Make the system honest (security + trust) — ✅ DONE (2026-07-12, uncommitted)

**Migration note:** U0.1 makes the OLS grid authoritative on the legacy read routes. System roles and template-lineage custom roles are seeded read access (unaffected). A *lineage-less* custom role (created pre-P6, zero OLS rows) previously read contacts/deals/companies via the ungated legacy path and will now be denied until an admin grants read in the grid — intended, but worth telling affected admins. Also: any role holding the now-retired `billing.manage` row renders/saves fine (the code filters retired codes on read; the stale row clears on that role's next capability save).

1. Add `olsOn(slug, ActionRead)` to legacy GET routes for contacts/companies/deals (list + by-id) and apply FieldMask to their responses — or cut the FE lists over to the /registry path. Closes defect #1.
2. Implement real member-removal reassignment: execute transfer/unassign over contacts + deals, revoke user-targeted report_shares/record_shares/group memberships, return owned-record counts in the 409, key the FE modal off an error code instead of the `'reassign_to_user_id'` substring (MembersList.tsx:103), and surface the backend's already-accepted "unassign" strategy as a second option.
3. Phantom capabilities: keep `org.settings` and wire it to the new Workspace General page (U4); **delete** `billing.manage` from the catalog until billing exists. Fix `data.export` copy to match what it gates.
4. Enforce `record_shares.permission_level` (validate read|edit; write path requires edit) — or relabel shares "view access" honestly.
5. Self-lockout guard: reject SetCapabilities that strips `roles.manage` from the caller's own role (mirror guardRoleAssignment), plus an FE confirm naming the consequence.
6. Gate `GET /api/pipeline/forecast` with `analytics.view` (parity with the AI tool).
7. Invite emails: pass real org name + inviter name (workspace_usecase.go:220).
8. Fix the AI Logs capability mismatch (`members.manage` on both sides).
9. OLS grid: auto-imply Read when Create/Edit/Delete is checked (FE rule + one-line explanation).
10. Log/count any HTTP-originated callerless request reaching Authorize (turns the designed fail-open into an observable invariant).

### U1 — One settings shell
1. Single routed settings area `/settings/*` with a grouped left nav: **Personal** (Profile, Security, Notifications) / **Workspace** (General, Members & Groups, Roles & Permissions, Objects & Fields, Pipeline, Email Templates, Knowledge Base, Audit Log, AI Logs). Every section URL-addressable; refresh/Back/deep-links work; breadcrumbs in nested editors (ObjectsManager sub-editors become routes).
2. Capability-gated *visibility*: sections a member can't use don't render (kills the viewer-edits-then-403 pattern); sidebar entries collapse to one "Settings" link.
3. Consistency pass: one dialog component (replace all ten `window.confirm`/`alert`), one icon set (lucide), Tailwind tokens in ObjectsManager/KnowledgeBase/MembersList (fixes their dark-mode breakage), one skeleton/error pattern, confirmations on every destructive action (field delete, layout delete, stage delete — say what happens to affected data), errors shown instead of swallowed (MembersList/ObjectsManager/KnowledgeBase fetches).
4. Templates tab: link to the existing A5 email-template library instead of "coming soon", or drop it.
5. Mobile: settings reachable and usable below md (nav collapses; wide grids scroll in overflow-x containers).
6. Settings destinations in GlobalSearch/command palette.

### U2 — My Account (the missing audience)
1. `PATCH /api/auth/me`: first/last name, avatar_url; Profile section with avatar upload + initials fallback. (New user columns → **main.go boot guard**, not a numbered migration — prod constraint.)
2. In-app change-password (current-password verified, reuses bcrypt + TokenVersion machinery) beside the existing sessions list; email-change with re-verification.
3. Connected accounts: show Google-linked status, set-a-password for OAuth-only accounts, unlink.
4. Theme: wire the shipped `.dark` palette to a light/dark/system selector (localStorage + class on `<html>`), persisted per user later via /me.
5. Timezone + locale on the user (and org default); automation date-field triggers inherit them instead of forcing per-trigger IANA picks.
6. **Header avatar becomes a user menu** (name/email, Settings, theme, Sign out) — also fixes the mobile sign-out dead-end in one stroke.
7. Session-expiry UX: "session expired" notice + return-to URL instead of a silent hard redirect; `autocomplete` attributes on all auth forms.
8. Move `onboarding_completed` from localStorage to the user row; show the welcome wizard only to users who can actually create objects.

### U3 — Permissions people can understand
1. **Role detail page**: one role's capabilities, object access, field restrictions, data scope, layouts, and members in a single view with a single pivot. The roles list becomes cards → detail, not a checkbox wall.
2. **Effective-access endpoint + "What can this role see?" panel**: merged capabilities + OLS + FLS + scope (data already sits in one cache entry, orgAccessEntry) — the poor-man's "view as", answering "what can Jane actually see?" without a test account.
3. Cross-links: role member-count → members filtered to that role; role dropdowns in MembersList/InviteMemberModal show a description + "what does this grant?" link.
4. FLS at scale: field search, "restricted only" filter, bulk apply (set column), restriction-count badges on object pills.
5. Language pass: kill "default-deny", "God-mode", "Row scope", "(Development only)" from user-facing copy; capability descriptions visible (not title-attribute tooltips); "Sensitive" chip instead of ⚠; prettyRole everywhere.
6. Zero-access banner becomes precise and actionable (click → jump to the offending role/object; dismissable for deliberately restricted roles); duplicate the nudge into ObjectsManager at object-creation time.
7. Adopt `usePermissions` app-wide: gate record edit/delete/export buttons and remaining settings surfaces so users stop discovering permissions via errors; friendly denied states ("You need *Manage roles* — ask an admin").

### U4 — Members & workspace lifecycle
1. Invites: `GET /auth/invitations/:token` metadata → accept page shows "Join **Acme** as **Sales Rep**"; auto-login after accept; dedupe (resend instead of double-insert); expired invites shown with a Resend badge instead of vanishing; multi-email invite; copyable invite link (prod-safe).
2. Members table: search, role/status filter, Joined / Last active / Verified columns, pagination; labeled actions.
3. Member detail drawer: role, groups, owned-record counts, sessions with admin force-sign-out (repo methods exist), audit trail link.
4. Workspace General page (behind `org.settings`, now real): rename, logo, org defaults (currency/locale/timezone); guarded delete-workspace.
5. `POST /workspaces` for existing users + fix the NoWorkspacePage redirect loop + "create workspace" entry on the chooser; self-serve **leave workspace** (last-owner-guarded).
6. Google-first invitee fix: if a pending invitation exists for the email, don't auto-create a junk personal org — route into the invite accept flow.
7. Groups: wire the existing rename API, show group membership on members, description field — groundwork for U6 teams.
8. Transfer ownership: real modal with consequences + type-to-confirm; auth-context refresh instead of `window.location.reload()`.

### U5 — Notifications people control
1. `notification_preferences` table + preference center in Personal settings: per-event-type × channel (in-app / email), mute-all, digest frequency.
2. Email as a second channel (reuse the Resend mailer + retry/idempotency work from 2026-07-12) with a daily digest option.
3. Expose the already-built unread-only filter as a bell toggle; settings link from the bell.

### U6 — Deeper access model (backend-heavy, each item independently shippable)
1. **Team scope**: groups grow into teams; `data_scope` gains `team` (own + my teams' records) — the missing middle between "mine" and "everything".
2. **Record sharing parity**: share records to user/role/group at view/edit like reports already do; enforced levels; a "Shared with me" view; one sharing framework across records, reports, and (later) workflows.
3. **Custom-object ownership**: owner field on custom records → assignment, own-scope, and sharing work uniformly (today a "private" custom object is impossible).
4. **2FA**: TOTP + backup codes, admin "require 2FA" org policy, member 2FA-status column. (Passkeys later.)
5. Personal API tokens (scoped, revocable, audited).

### U7 — Fit & finish sweep
1. Accessibility: Radix dialogs (already a dependency) for hand-rolled modals, focus traps, aria labels on icon-only buttons, keyboard nav through grids.
2. `document.title` per page (the tab currently says "crm-frontend" everywhere).
3. Settings screens onto react-query with refetch (two admins editing the same grid currently last-write-wins invisibly).
4. Correlation/request IDs surfaced in error toasts so "I can't save" is debuggable.
5. Help affordances: contextual explainer for the permission model, docs/support links; a returnable setup checklist (invite team → roles → pipeline → import) replacing the one-shot WelcomeModal.
6. Terms/Privacy links at signup.

**Explicitly out of scope for now:** SSO/SAML, SCIM/directory sync, billing UI, IP allowlisting, GDPR self-serve export/delete (tracked as future work; U2's email-change and U4's leave-workspace reduce the sharpest edges).

## 4. Status

| Phase | Scope | Status |
|-------|-------|--------|
| U0 | Honesty/security fixes | **DONE** (uncommitted; BE build/vet/tests + FE tsc + 713 FE tests green) |
| U1 | Unified settings shell | NOT STARTED |
| U2 | My Account | NOT STARTED |
| U3 | Understandable permissions | NOT STARTED |
| U4 | Members & lifecycle | NOT STARTED |
| U5 | Notification preferences | NOT STARTED |
| U6 | Team scope / sharing / 2FA | NOT STARTED |
| U7 | Fit & finish | NOT STARTED |

**Constraints to respect throughout** (from prior overhauls): new tables/columns need main.go IF-NOT-EXISTS boot guards (golang-migrate is dead on prod); backend isn't gofmt-clean (build + vet are the gates); FE tests via `npx vitest run` (not rtk), types via `rtk npx tsc -b`; never share-to-self (enforced both layers); adding capabilities/actions means updating both the TS union and the zod enum where applicable.
