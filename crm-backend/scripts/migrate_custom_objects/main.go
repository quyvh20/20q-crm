package main

import (
	"fmt"
	"os"

	"crm-backend/pkg/config"
	"crm-backend/pkg/database"
)

func main() {
	cfg, _ := config.LoadConfig()
	db, err := database.NewConnection(cfg.DatabaseURL)
	if err != nil {
		fmt.Println("Failed to connect:", err)
		os.Exit(1)
	}

	// Fix: Replace the full UNIQUE with a partial unique index that
	// excludes soft-deleted rows, so we can re-create slugs after deletion.
	sql := `
	-- Drop the old full unique constraint
	ALTER TABLE custom_object_defs DROP CONSTRAINT IF EXISTS custom_object_defs_org_id_slug_key;

	-- Create partial unique index (only for non-deleted rows)
	CREATE UNIQUE INDEX IF NOT EXISTS uix_custom_object_defs_org_slug
	ON custom_object_defs(org_id, slug) WHERE deleted_at IS NULL;

	-- Hard-delete any soft-deleted test rows to clean up
	DELETE FROM custom_object_records WHERE deleted_at IS NOT NULL;
	DELETE FROM custom_object_defs WHERE deleted_at IS NOT NULL;
	`

	if err := db.Exec(sql).Error; err != nil {
		fmt.Println("Migration fix failed:", err)
		os.Exit(1)
	}

	fmt.Println("✅ Fixed UNIQUE constraint to exclude soft-deleted rows")
}
