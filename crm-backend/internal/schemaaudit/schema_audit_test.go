package schemaaudit

import (
	"context"
	"errors"
	"net/url"
	"os"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"crm-backend/internal/automation"
	"crm-backend/internal/domain"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
	"gorm.io/gorm/logger"
)

// ============================================================
// Schema audit — boot DDL must settle
// ============================================================
//
// Every boot runs gorm's AutoMigrate over two model sets: the platform list in
// cmd/server/main.go and internal/automation's own. AutoMigrate reconciles the
// database to the structs, and a reconciliation that SUCCEEDS is silent — the
// Postgres log only ever shows the ones it refuses. That asymmetry hid a real
// bug for months (see domain.User.Email), and it hides a second, quieter class
// outright: a tag gorm can never round-trip re-issues the SAME `ALTER TABLE` on
// every single boot, taking an ACCESS EXCLUSIVE lock each time, forever, with
// nothing in any log to say so. internal/automation's migrateSchema comment
// documents one that ran that way on two of its hottest tables.
//
// This test asks the question that catches both: after the server has already
// booted and migrated, does ANOTHER AutoMigrate still want to change anything?
//
//	on a settled schema  → zero DDL
//	drift gorm can fix   → DDL here, and it ran at boot too (once)
//	drift gorm CANNOT fix→ DDL here, and it runs at EVERY boot, forever
//
// so any statement this reports is a defect: either the tag disagrees with the
// migration that owns the column, or it disagrees in a way gorm cannot settle.
//
// It runs against a LIVE, already-booted database (the E2E stack in CI), which
// is what makes it faithful: that schema is migrations PLUS the main.go boot
// guards, and no fixture can drift away from it because there is no fixture.
//
// Nothing is written. Each model migrates inside its own transaction that is
// deliberately rolled back, under a bounded lock_timeout — the audit must never
// be the thing that mutates a database or stalls a reader.
func TestSchemaAudit_BootAutoMigrateEmitsNoDDL(t *testing.T) {
	dsn := os.Getenv("SCHEMA_AUDIT_DATABASE_URL")
	if dsn == "" {
		dsn = os.Getenv("DATABASE_URL")
	}
	if dsn == "" {
		t.Skip("SCHEMA_AUDIT_DATABASE_URL / DATABASE_URL not set — schema audit needs a live, migrated database")
	}
	requireLocalTarget(t, dsn)

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{Logger: logger.Discard})
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	// Prove the instrument before trusting its silence. This audit's whole
	// output is "no DDL was emitted", which is also exactly what a broken
	// recorder reports — a mis-wired session logger, a gorm version that stops
	// routing DDL through Trace, or a filter that matches nothing would all
	// yield a confident, permanent, meaningless pass. So migrate a model whose
	// table cannot exist and require that it be SEEN.
	canary, canaryErr := ddlFor(db, &schemaAuditCanary{})
	if canaryErr != nil {
		t.Fatalf("canary migrate failed, so the audit below proves nothing: %v", canaryErr)
	}
	if len(canary) == 0 {
		t.Fatal("canary emitted no DDL: creating a table that does not exist MUST produce a " +
			"CREATE TABLE, so the recorder is not observing gorm's statements — every 'clean' " +
			"result from this audit is meaningless until this passes")
	}

	var findings []string
	for _, m := range auditedModels() {
		stmts, migrateErr := ddlFor(db, m.model)
		for _, s := range stmts {
			findings = append(findings, m.name+": "+s)
		}
		// A blocked ALTER (a column a VIEW depends on, say) surfaces as an error
		// rather than as a statement gorm reports cleanly, and it is the WORSE
		// case: it can never settle, so it is retried on every boot for the life
		// of the deployment.
		if migrateErr != nil && !errors.Is(migrateErr, errAuditRollback) {
			findings = append(findings, m.name+": FAILED — "+migrateErr.Error())
		}
	}

	if len(findings) == 0 {
		t.Logf("schema audit clean: %d models, no DDL (recorder proven live by canary: %s)",
			len(auditedModels()), canary[0])
		return
	}
	sort.Strings(findings)
	t.Errorf("boot AutoMigrate still wants to change %d thing(s) on an already-migrated database.\n"+
		"Each line is DDL that ran at boot and will run again on the next one:\n  %s\n\n"+
		"Fix by making the struct tag describe the column the migration actually creates "+
		"(size, nullability AND default — gorm emits an ALTER ... TYPE when any of them "+
		"disagrees). See domain.AITokenUsage for a worked example.",
		len(findings), strings.Join(findings, "\n  "))
}

// errAuditRollback unwinds each model's transaction. Returning an error is how
// gorm is told to roll back, so this sentinel is a success path, not a failure.
var errAuditRollback = errors.New("schema audit: rollback")

type auditModel struct {
	name  string
	model any
}

// schemaAuditCanary is the control: a table no migration creates and no boot
// guard knows about, so AutoMigrate must always want to CREATE it. Its
// transaction is rolled back with everything else, so it never actually exists
// — which is what keeps it a reliable control on every run rather than only the
// first.
type schemaAuditCanary struct {
	ID   uint   `gorm:"primaryKey"`
	Name string `gorm:"size:16"`
}

func (schemaAuditCanary) TableName() string { return "schema_audit_canary_never_committed" }

// auditedModels is every model something AutoMigrates at boot: the platform set
// from cmd/server/main.go plus internal/automation's own. Models NOT listed here
// are never AutoMigrated, so they cannot emit boot DDL however far their tags
// have drifted — their tables are owned by migrations and boot guards.
//
// Keep this in step with both call sites; a model added there and missed here is
// simply unaudited, which is the state this whole file exists to end.
func auditedModels() []auditModel {
	return []auditModel{
		{"domain.Role", &domain.Role{}},
		{"domain.RolePermission", &domain.RolePermission{}},
		{"domain.OrgUser", &domain.OrgUser{}},
		{"domain.KnowledgeBaseEntry", &domain.KnowledgeBaseEntry{}},
		{"domain.AITokenUsage", &domain.AITokenUsage{}},
		{"domain.RecordShare", &domain.RecordShare{}},
		{"domain.OrgInvitation", &domain.OrgInvitation{}},
		{"domain.ChatSession", &domain.ChatSession{}},
		{"domain.ChatMessage", &domain.ChatMessage{}},
		{"domain.VoiceNote", &domain.VoiceNote{}},
		// Pulled in as OrgUser's belongs-to rather than listed at the call site,
		// and audited explicitly because it is the model whose drift aborted the
		// whole list.
		{"domain.User", &domain.User{}},

		{"automation.Workflow", &automation.Workflow{}},
		{"automation.WorkflowVersion", &automation.WorkflowVersion{}},
		{"automation.WorkflowRun", &automation.WorkflowRun{}},
		{"automation.RunIdempotencyClaim", &automation.RunIdempotencyClaim{}},
		{"automation.WorkflowActionLog", &automation.WorkflowActionLog{}},
		{"automation.WorkflowOrgToken", &automation.WorkflowOrgToken{}},
		{"automation.AutomationTimer", &automation.AutomationTimer{}},
		{"automation.EmailTemplate", &automation.EmailTemplate{}},
		{"automation.AssignCursor", &automation.AssignCursor{}},
	}
}

// ddlFor runs AutoMigrate for one model and returns the DDL it emitted, having
// rolled the whole thing back.
func ddlFor(db *gorm.DB, model any) ([]string, error) {
	rec := &ddlRecorder{Interface: logger.Discard}
	err := db.Session(&gorm.Session{Logger: rec}).Transaction(func(tx *gorm.DB) error {
		// Same reasoning as internal/automation's migrateSchema: SET LOCAL binds
		// the timeout to this transaction's pinned connection, which is the one
		// the DDL below would run on. An audit must not be able to stall a reader.
		if err := tx.Exec(`SET LOCAL lock_timeout = '3s'`).Error; err != nil {
			return err
		}
		if err := tx.AutoMigrate(model); err != nil {
			return err
		}
		return errAuditRollback
	})
	if errors.Is(err, errAuditRollback) {
		err = nil
	}
	return rec.ddl(), err
}

// ddlRecorder captures the schema-changing statements gorm executes. gorm has no
// dry-run mode, so the statements are observed through the logger and undone by
// the surrounding rollback.
type ddlRecorder struct {
	logger.Interface
	mu    sync.Mutex
	stmts []string
}

func (r *ddlRecorder) Trace(_ context.Context, _ time.Time, fc func() (string, int64), _ error) {
	sql, _ := fc()
	trimmed := strings.TrimSpace(sql)
	upper := strings.ToUpper(trimmed)
	// AutoMigrate reads the catalog constantly (information_schema, pg_catalog);
	// only statements that CHANGE something are findings. SET LOCAL is ours.
	switch {
	case strings.HasPrefix(upper, "ALTER "),
		strings.HasPrefix(upper, "CREATE TABLE"),
		strings.HasPrefix(upper, "CREATE INDEX"),
		strings.HasPrefix(upper, "CREATE UNIQUE INDEX"),
		strings.HasPrefix(upper, "DROP "),
		strings.HasPrefix(upper, "COMMENT ON"):
	default:
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.stmts = append(r.stmts, strings.Join(strings.Fields(trimmed), " "))
}

func (r *ddlRecorder) ddl() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.stmts...)
}

// requireLocalTarget refuses a remote database unless explicitly allowed, the
// same fail-closed rule crm-backend/scripts/lib/seedenv.js applies to the seed.
// The audit only reads and rolls back, but it still takes brief DDL locks, and
// one environment variable is all that should ever stand between a convenience
// default and someone's production database.
func requireLocalTarget(t *testing.T, dsn string) {
	t.Helper()
	if os.Getenv("SCHEMA_AUDIT_ALLOW_REMOTE") == "1" {
		return
	}
	host := dsnHost(dsn)
	if host == "localhost" || host == "127.0.0.1" || host == "::1" || host == "" {
		return
	}
	t.Skipf("schema audit target %q is not local — set SCHEMA_AUDIT_ALLOW_REMOTE=1 to audit it deliberately", host)
}

func dsnHost(dsn string) string {
	if u, err := url.Parse(dsn); err == nil && u.Host != "" {
		return u.Hostname()
	}
	// key=value DSN form (host=... port=...).
	for _, part := range strings.Fields(dsn) {
		if strings.HasPrefix(part, "host=") {
			return strings.TrimPrefix(part, "host=")
		}
	}
	return ""
}
