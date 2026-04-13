package main

import (
	"fmt"
	"os"

	"gorm.io/driver/postgres"
	"gorm.io/gorm"
)

func main() {
	dsn := os.Getenv("DATABASE_URL")
	if len(os.Args) > 1 {
		dsn = os.Args[1]
	}
	if dsn == "" {
		fmt.Println("Usage: go run . <DATABASE_URL>")
		fmt.Println("  or: DATABASE_URL=... go run .")
		os.Exit(1)
	}

	db, err := gorm.Open(postgres.Open(dsn), &gorm.Config{})
	if err != nil {
		fmt.Println("Failed to connect:", err)
		os.Exit(1)
	}

	sql := `
	-- Create tables if not exist
	CREATE TABLE IF NOT EXISTS custom_object_defs (
		id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		slug          VARCHAR(100) NOT NULL,
		label         VARCHAR(255) NOT NULL,
		label_plural  VARCHAR(255) NOT NULL,
		icon          VARCHAR(50) DEFAULT '📦',
		fields        JSONB DEFAULT '[]',
		created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at    TIMESTAMPTZ
	);

	CREATE TABLE IF NOT EXISTS custom_object_records (
		id            UUID PRIMARY KEY DEFAULT uuid_generate_v4(),
		org_id        UUID NOT NULL REFERENCES organizations(id) ON DELETE CASCADE,
		object_def_id UUID NOT NULL REFERENCES custom_object_defs(id) ON DELETE CASCADE,
		display_name  VARCHAR(500) NOT NULL DEFAULT '',
		data          JSONB DEFAULT '{}',
		contact_id    UUID REFERENCES contacts(id) ON DELETE SET NULL,
		deal_id       UUID REFERENCES deals(id) ON DELETE SET NULL,
		created_by    UUID REFERENCES users(id) ON DELETE SET NULL,
		created_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		updated_at    TIMESTAMPTZ NOT NULL DEFAULT NOW(),
		deleted_at    TIMESTAMPTZ
	);

	-- Partial unique index (excludes soft-deleted rows)
	CREATE UNIQUE INDEX IF NOT EXISTS uix_custom_object_defs_org_slug
	ON custom_object_defs(org_id, slug) WHERE deleted_at IS NULL;

	CREATE INDEX IF NOT EXISTS idx_custom_object_defs_org ON custom_object_defs(org_id);
	CREATE INDEX IF NOT EXISTS idx_cor_org_def ON custom_object_records(org_id, object_def_id);
	CREATE INDEX IF NOT EXISTS idx_cor_contact ON custom_object_records(contact_id) WHERE contact_id IS NOT NULL;
	CREATE INDEX IF NOT EXISTS idx_cor_deal ON custom_object_records(deal_id) WHERE deal_id IS NOT NULL;
	`

	if err := db.Exec(sql).Error; err != nil {
		fmt.Println("Migration failed:", err)
		os.Exit(1)
	}

	fmt.Println("✅ Custom objects tables created successfully")
}
