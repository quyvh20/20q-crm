package repository

import (
	"log"

	"crm-backend/internal/domain"
	"gorm.io/gorm"
)

// systemRoleOrder is the fixed set of built-in roles, seeded in this order.
var systemRoleOrder = []string{
	domain.RoleOwner, domain.RoleAdmin, domain.RoleManager, domain.RoleSales, domain.RoleViewer,
}

// SeedSystemRoles ensures the five built-in roles exist with their capability
// grants (role_permissions as the system-capability store, plan D5) and row scope
// (roles.data_scope). System roles are global singletons (org_id NULL) shared by
// every org, so they are NOT admin-editable — this seeder is their single owner
// and keeps their capabilities aligned to DefaultRoleCapabilities. Admins
// customize by CLONING a system role into an org-scoped custom role and editing
// that (role_usecase refuses SetCapabilities/Update/Delete on system roles).
//
// The seed is idempotent: it creates missing roles, aligns each role's data_scope
// with the default, and inserts any missing default capability rows (never
// stamping org_id — system-role rows must stay global). owner holds no capability
// rows (it bypasses checks), so an empty table can never lock it out.
func SeedSystemRoles(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, name := range systemRoleOrder {
			var role domain.Role
			err := tx.Where("name = ? AND is_system = ?", name, true).First(&role).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}

			scope := domain.DefaultRoleDataScope[name]
			if scope == "" {
				scope = domain.DataScopeAll
			}

			if err == gorm.ErrRecordNotFound {
				templateKey := name
				role = domain.Role{
					Name:        name,
					IsSystem:    true,
					IsOwner:     name == domain.RoleOwner,
					TemplateKey: &templateKey,
					DataScope:   scope,
				}
				if err := tx.Create(&role).Error; err != nil {
					return err
				}
				log.Printf("Seeded system role: %s", name)
			} else if role.DataScope != scope {
				// Keep system-role scope aligned with the documented default.
				if err := tx.Model(&domain.Role{}).Where("id = ?", role.ID).
					Update("data_scope", scope).Error; err != nil {
					return err
				}
			}
			if name == domain.RoleOwner && !role.IsOwner {
				// Realign the flag for rows created before is_owner existed.
				if err := tx.Model(&domain.Role{}).Where("id = ?", role.ID).
					Update("is_owner", true).Error; err != nil {
					return err
				}
			}

			// Ensure each default capability row exists (idempotent insert-missing).
			// System-role rows are global — org_id stays NULL.
			for _, code := range domain.DefaultRoleCapabilities[name] {
				var exists int64
				if err := tx.Model(&domain.RolePermission{}).
					Where("role_id = ? AND permission_code = ?", role.ID, code).
					Count(&exists).Error; err != nil {
					return err
				}
				if exists == 0 {
					if err := tx.Create(&domain.RolePermission{
						RoleID:         role.ID,
						PermissionCode: code,
					}).Error; err != nil {
						return err
					}
				}
			}
		}

		// Perform one-off data migration if org_users.role (varchar) still exists
		// We use raw SQL because we removed `Role` from the GORM model.
		var count int64
		tx.Raw("SELECT count(*) FROM information_schema.columns WHERE table_name='org_users' AND column_name='role'").Count(&count)
		if count > 0 {
			log.Println("Found legacy 'role' column in org_users. Migrating to 'role_id'...")
			err := tx.Exec(`
				UPDATE org_users 
				SET role_id = roles.id 
				FROM roles 
				WHERE 
					roles.is_system = true AND 
					roles.name = org_users.role AND 
					org_users.role_id IS NULL AND
					org_users.role IS NOT NULL
			`).Error
			if err != nil {
				return err
			}

			// Also handle "super_admin" which was our previous highest role -> map to 'owner'
			err = tx.Exec(`
				UPDATE org_users 
				SET role_id = (SELECT id FROM roles WHERE name = 'owner' AND is_system = true) 
				WHERE org_users.role = 'super_admin' AND org_users.role_id IS NULL
			`).Error
			if err != nil {
				return err
			}

			// Alter table to drop the legacy column safely since data is migrated
			log.Println("Dropping legacy 'role' column...")
			if err := tx.Exec("ALTER TABLE org_users DROP COLUMN role").Error; err != nil {
				log.Printf("Warning: Failed to drop legacy role column: %v", err)
			}
		}

		return nil
	})
}
