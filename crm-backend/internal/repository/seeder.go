package repository

import (
	"log"

	"crm-backend/internal/domain"
	"gorm.io/gorm"
)

var systemRoles = []struct {
	Name        string
	Permissions []string
}{
	{domain.RoleOwner, []string{"all:all:all"}},
	{domain.RoleAdmin, []string{"user:read:all", "user:write:all", "deal:read:all", "deal:write:all", "contact:read:all", "contact:write:all"}},
	{domain.RoleManager, []string{"deal:read:team", "deal:write:team", "contact:read:team", "contact:write:team"}},
	{domain.RoleSales, []string{"deal:read:own", "deal:write:own", "contact:read:own", "contact:write:own"}},
	{domain.RoleViewer, []string{"deal:read:all", "contact:read:all"}},
}

func SeedSystemRoles(db *gorm.DB) error {
	return db.Transaction(func(tx *gorm.DB) error {
		for _, sr := range systemRoles {
			var role domain.Role
			// Check if system role exists
			err := tx.Where("name = ? AND is_system = ?", sr.Name, true).First(&role).Error
			if err != nil && err != gorm.ErrRecordNotFound {
				return err
			}

			if err == gorm.ErrRecordNotFound {
				// Create
				role = domain.Role{
					Name:     sr.Name,
					IsSystem: true,
				}
				if err := tx.Create(&role).Error; err != nil {
					return err
				}
				log.Printf("Seeded system role: %s", sr.Name)
			}

			// Add/Update permissions
			for _, pc := range sr.Permissions {
				var rp domain.RolePermission
				err := tx.Where("role_id = ? AND permission_code = ?", role.ID, pc).First(&rp).Error
				if err == gorm.ErrRecordNotFound {
					rp = domain.RolePermission{
						RoleID:         role.ID,
						PermissionCode: pc,
					}
					if err := tx.Create(&rp).Error; err != nil {
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
