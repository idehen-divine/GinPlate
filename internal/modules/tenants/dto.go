package tenants

// CreateTenantDTO creates a shared-first tenant.
type CreateTenantDTO struct {
	Slug string `json:"slug" binding:"required,min=2,max=63"`
	Name string `json:"name" binding:"required,max=255"`
	Pool string `json:"pool" binding:"max=64"`
}

// SetStatusDTO flips a tenant between active and suspended.
type SetStatusDTO struct {
	Status string `json:"status" binding:"required,oneof=active suspended"`
}
