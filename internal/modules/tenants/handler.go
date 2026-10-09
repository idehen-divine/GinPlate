package tenants

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Handler binds tenant admin HTTP to the Tenants service. Move and domain
// endpoints live on their own units' handlers. All routes sit behind
// control-admin authentication (see cadmin middleware).
type Handler struct {
	tenants *Tenants
}

// NewHandler wires a Tenants service to its HTTP handlers.
func NewHandler(tenants *Tenants) *Handler {
	return &Handler{tenants: tenants}
}

// Create provisions a shared-first tenant.
// @Summary Create tenant
// @Tags AdminTenants
// @Param payload body CreateTenantDTO true "tenant"
// @Success 201 {object} map[string]interface{}
// @Router /admin/tenants [post]
func (h *Handler) Create(c *gin.Context) {
	var dto CreateTenantDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	rec, err := h.tenants.Create(c.Request.Context(), dto)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusCreated, "Tenant created.", gin.H{
		"id": rec.ID.String(), "slug": rec.Slug, "name": rec.Name,
		"placement": rec.Placement, "pool": rec.Pool, "status": rec.Status,
	})
}

// List pages tenants.
// @Summary List tenants
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants [get]
func (h *Handler) List(c *gin.Context) {
	f := web.BindFilter(c)
	rows, total, err := h.tenants.List(c.Request.Context(), f.Limit, f.Offset)
	if err != nil {
		web.Render(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, r := range rows {
		items = append(items, tenantItem(r.Slug, r.Name, r.Placement, r.Pool, r.Status, r.ID.String()))
	}
	web.Success(c, http.StatusOK, "Tenants.", gin.H{
		"data": items, "total": total, "limit": f.Limit, "offset": f.Offset,
	})
}

// Get returns one tenant.
// @Summary Get tenant
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{slug} [get]
func (h *Handler) Get(c *gin.Context) {
	rec, err := h.tenants.Get(c.Request.Context(), c.Param("slug"))
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Tenant.", tenantItem(
		rec.Slug, rec.Name, rec.Placement, rec.Pool, rec.Status, rec.ID.String()))
}

// SetStatus suspends or resumes a tenant.
// @Summary Set tenant status
// @Tags AdminTenants
// @Param payload body SetStatusDTO true "status"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{slug}/status [patch]
func (h *Handler) SetStatus(c *gin.Context) {
	var dto SetStatusDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	rec, err := h.tenants.SetStatus(c.Request.Context(), c.Param("slug"), dto.Status)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Tenant updated.", tenantItem(
		rec.Slug, rec.Name, rec.Placement, rec.Pool, rec.Status, rec.ID.String()))
}

// Remove deletes a shared tenant with its pool rows. Live migrations and
// dedicated placements are refused; see Tenants.Remove.
// @Summary Remove tenant
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{slug} [delete]
func (h *Handler) Remove(c *gin.Context) {
	if err := h.tenants.Remove(c.Request.Context(), c.Param("slug")); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Tenant removed.", nil)
}

func tenantItem(slug, name, placement, pool, status, id string) gin.H {
	return gin.H{
		"id": id, "slug": slug, "name": name,
		"placement": placement, "pool": pool, "status": status,
	}
}
