package integrations

import (
	"context"
	"log/slog"
	"time"

	"github.com/google/uuid"
)

// Workspace teardown (L6.4).
//
// Deleting a workspace touched no integrations table at all. Inbound stopped —
// every token lookup joins organizations on deleted_at IS NULL — but three things
// survived indefinitely:
//
//   - the sealed provider credentials, encrypted at rest with nothing left that could
//     ever reach a UI to remove them (deletion evicts every member, so nobody can
//     authenticate to the management API of the workspace they deleted);
//   - the page CLAIM. The claim index is partial on live, connected rows, so a deleted
//     workspace went on holding a customer's Facebook page hostage against every other
//     workspace — a page they could never release, because releasing it means signing
//     in to the workspace that is gone;
//   - the ASYNC backlog. Receipt-time joins stop new deliveries, but ClaimPendingEvents
//     is the global worker queue with no org join.
//
// Best-effort and never fails the deletion: a customer asking to delete must not be
// told "no" because Meta was slow. Every step is idempotent, and the retention sweep
// re-runs the teardown for any workspace left half-purged — which is what makes
// "best-effort" defensible rather than a permanent silent failure.

// purgeUnsubscribeTimeout bounds the detached provider-teardown pass.
const purgeUnsubscribeTimeout = 2 * time.Minute

// PurgeService tears down an org's integrations when its workspace is deleted.
type PurgeService struct {
	repo   *Repository
	conn   *ConnectionService
	logger *slog.Logger
	// cancelBackfills stops in-flight historical imports for an org. Nil-tolerant.
	cancelBackfills func(orgID uuid.UUID) int
}

// NewPurgeService builds the teardown. A nil connection service is tolerated: the
// database half — the half that protects the customer — must still run where no
// provider is configured.
func NewPurgeService(repo *Repository, conn *ConnectionService, logger *slog.Logger) *PurgeService {
	return &PurgeService{repo: repo, conn: conn, logger: logger}
}

// WithBackfillCanceller wires the in-flight import canceller (the source Handler owns
// the registry). Set after both exist, so a setter rather than a constructor arg.
func (s *PurgeService) WithBackfillCanceller(fn func(orgID uuid.UUID) int) *PurgeService {
	if s != nil {
		s.cancelBackfills = fn
	}
	return s
}

// PurgeWorkspace removes an org's integration footprint, reporting what it did.
func (s *PurgeService) PurgeWorkspace(ctx context.Context, orgID uuid.UUID) (connections, sources int64, err error) {
	if s == nil || s.repo == nil {
		return 0, 0, nil
	}

	// FIRST, before any statement: stop work already in flight. A backfill runs
	// detached with its own 15-minute budget and a *LeadSource captured at request
	// time; it re-checks nothing, so blanking the credentials in the database does not
	// stop it — it is holding the opened token in memory. Left running, it keeps
	// importing real people's names, emails and phones into a workspace that no longer
	// exists, and with enrolment on it can mail them.
	if s.cancelBackfills != nil {
		if n := s.cancelBackfills(orgID); n > 0 {
			s.info("integrations: cancelled in-flight imports for a deleted workspace",
				"org_id", orgID.String(), "imports", n)
		}
	}

	// Custody rows next, so no half-finished OAuth handshake can complete behind the
	// teardown and insert a brand-new sealed token into a workspace that is gone.
	if err := s.repo.PurgeOAuthArtifactsForOrg(ctx, orgID); err != nil {
		s.warn("integrations: could not purge OAuth artifacts on workspace teardown", "org_id", orgID.String(), "error", err)
	}

	// Snapshot BEFORE the purge blanks the ciphertext the unsubscribe needs — the
	// goroutine below works entirely from these in-memory rows and never re-reads.
	// Only the rows that still HOLD a claim: see unsubscribeDetached.
	claimed, lerr := s.repo.ListClaimedConnections(ctx, orgID)
	if lerr != nil {
		s.warn("integrations: could not list connections for workspace teardown", "org_id", orgID.String(), "error", lerr)
		claimed = nil
	}

	var firstErr error

	// Connections BEFORE sources, and the order is load-bearing. GetConnection is
	// soft-delete-scoped, so once these rows are gone the async worker takes its
	// `conn == nil` branch and fails the delivery WITHOUT fetching. Disabling sources
	// first would leave the connection live, so the worker would fetch the lead from
	// the provider — pulling a real person's details — and only then quarantine it,
	// writing fresh personal data into the workspace the customer just deleted.
	n, cerr := s.repo.PurgeConnectionSecrets(ctx, orgID)
	if cerr != nil {
		firstErr = cerr
		// The loudest line here. A credential we failed to destroy is the one thing a
		// customer would consider a breach of the deletion.
		s.warn("integrations: COULD NOT DESTROY provider credentials on workspace teardown — they remain at rest",
			"org_id", orgID.String(), "error", cerr)
	}
	connections = n

	// Belt-and-braces for the connection-less kinds (capture API, google_ads,
	// form_embed), which are already refused at receipt by the organizations join.
	sn, serr := s.repo.DisableSourcesForOrg(ctx, orgID)
	if serr != nil {
		if firstErr == nil {
			firstErr = serr
		}
		s.warn("integrations: could not disable lead sources on workspace teardown", "org_id", orgID.String(), "error", serr)
	}
	sources = sn

	s.unsubscribeDetached(orgID, claimed)
	return connections, sources, firstErr
}

// unsubscribeDetached asks each provider to stop sending, off the request path.
//
// SCOPED TO PAGES THIS ORG STILL HELD, and that restriction is a cross-tenant safety
// control rather than an optimisation. `Provider.Disconnect` is
// `DELETE /{page}/subscribed_apps` — it unsubscribes OUR APP FROM THE PAGE, and the
// call knows nothing about connections, orgs or claims. So sweeping every connection
// the org ever had would reach pages it disconnected long ago and that ANOTHER
// workspace has since connected: that workspace's card would still read "connected,
// receiving leads" while Meta silently stopped delivering, and nothing would alert,
// because our health signal counts fetch failures and there would be no fetches.
//
// Each page is re-checked against the live claim immediately before the call, because
// releasing the claim above means another workspace can take it within seconds.
func (s *PurgeService) unsubscribeDetached(orgID uuid.UUID, conns []IntegrationConnection) {
	if s.conn == nil || len(conns) == 0 {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), purgeUnsubscribeTimeout)
		defer cancel()
		for i := range conns {
			c := conns[i]
			prov, ok := s.conn.registry.Get(c.Provider)
			if !ok || !s.conn.codec.Configured() {
				continue
			}
			// Has someone else taken this page since we released it?
			if holder, herr := s.repo.FindActiveClaim(ctx, c.Provider, c.ExternalAccountID); herr == nil && holder != nil && holder.OrgID != orgID {
				s.info("integrations: skipping provider unsubscribe — the account is now connected elsewhere",
					"provider", c.Provider, "connection_id", c.ID.String())
				continue
			}
			creds, oerr := s.conn.openCredentials(&c)
			if oerr != nil {
				continue // nothing to speak to the provider with; the local row is already gone
			}
			if derr := prov.Disconnect(ctx, &c, creds); derr != nil {
				// Best-effort: a page we could not unsubscribe keeps sending, and every
				// delivery is dropped at receipt because the org is gone.
				s.warn("integrations: provider unsubscribe failed during workspace teardown",
					"provider", c.Provider, "connection_id", c.ID.String(), "error", derr)
			}
		}
	}()
}

func (s *PurgeService) warn(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Error(msg, args...)
	}
}

func (s *PurgeService) info(msg string, args ...any) {
	if s.logger != nil {
		s.logger.Info(msg, args...)
	}
}
