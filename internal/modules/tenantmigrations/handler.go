package tenantmigrations

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Handler binds move admin HTTP to the TenantMigrations service. All routes
// sit behind control-admin authentication (see cadmin middleware).
type Handler struct {
	svc *TenantMigrations
}

// NewHandler wires a TenantMigrations service to its HTTP handlers.
func NewHandler(svc *TenantMigrations) *Handler { return &Handler{svc: svc} }

// MigrateDTO opens a shared-to-dedicated move.
type MigrateDTO struct {
	Reason string `json:"reason" binding:"max=500"`
}

// Migrate opens a move ledger row; execution runs in the worker (or the
// migrate CLI), not inline.
// @Summary Request tenant migration
// @Tags AdminTenants
// @Param payload body MigrateDTO true "reason"
// @Success 202 {object} map[string]interface{}
// @Router /admin/tenants/{slug}/migrate [post]
func (h *Handler) Migrate(c *gin.Context) {
	var dto MigrateDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	mig, err := h.svc.RequestMigration(c.Request.Context(), c.Param("slug"), dto.Reason)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusAccepted, "Migration requested.", gin.H{
		"migration": gin.H{"id": mig.ID.String(), "phase": mig.Phase},
	})
}

// Rollback aborts a non-terminal move and restores shared/active.
// @Summary Roll back a tenant migration
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/migrations/{id}/rollback [post]
func (h *Handler) Rollback(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		web.Render(c, web.Wrap(http.StatusBadRequest, "Invalid migration id.", nil))
		return
	}
	if err := h.svc.Rollback(c.Request.Context(), id); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Migration rolled back.", nil)
}

// MigrationStatus reports the newest move ledger row, if any.
// @Summary Tenant migration status
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{slug}/migration [get]
func (h *Handler) MigrationStatus(c *gin.Context) {
	m, err := h.svc.Status(c.Request.Context(), c.Param("slug"))
	if err != nil {
		web.Render(c, err)
		return
	}
	if m == nil {
		web.Success(c, http.StatusOK, "No migration.", gin.H{"migration": nil})
		return
	}
	web.Success(c, http.StatusOK, "Migration.", gin.H{"migration": gin.H{
		"id": m.ID.String(), "phase": m.Phase, "attempts": m.Attempts, "error": m.Error,
	}})
}
