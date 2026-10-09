package tenantdomains

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Handler binds custom-domain admin HTTP to the TenantDomain service. All
// routes sit behind control-admin authentication (see cadmin middleware).
type Handler struct {
	svc *TenantDomain
}

// NewHandler wires a TenantDomain service to its HTTP handlers.
func NewHandler(svc *TenantDomain) *Handler { return &Handler{svc: svc} }

// AddDomainDTO carries a custom domain to map to a tenant.
type AddDomainDTO struct {
	Domain string `json:"domain" binding:"required,max=253"`
}

// VerifyDomainDTO flips a mapping's verified flag.
type VerifyDomainDTO struct {
	Verified bool `json:"verified"`
}

func domainItem(d Domain) gin.H {
	return gin.H{
		"id": d.ID.String(), "tenant_id": d.TenantID.String(),
		"domain": d.Domain, "verified": d.Verified,
	}
}

// AddDomain maps a customer-owned domain to a tenant (starts unverified).
// @Summary Add tenant domain
// @Tags AdminTenants
// @Param payload body AddDomainDTO true "domain"
// @Success 201 {object} map[string]interface{}
// @Router /admin/tenants/{slug}/domains [post]
func (h *Handler) AddDomain(c *gin.Context) {
	var dto AddDomainDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	d, err := h.svc.AddDomain(c.Request.Context(), c.Param("slug"), dto.Domain)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusCreated, "Domain added.", domainItem(*d))
}

// ListDomains lists a tenant's custom-domain mappings.
// @Summary List tenant domains
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/{slug}/domains [get]
func (h *Handler) ListDomains(c *gin.Context) {
	rows, err := h.svc.ListDomains(c.Request.Context(), c.Param("slug"))
	if err != nil {
		web.Render(c, err)
		return
	}
	items := make([]gin.H, 0, len(rows))
	for _, d := range rows {
		items = append(items, domainItem(d))
	}
	web.Success(c, http.StatusOK, "Domains.", gin.H{"data": items})
}

// VerifyDomain records out-of-band ownership proof for a mapping.
// @Summary Verify tenant domain
// @Tags AdminTenants
// @Param payload body VerifyDomainDTO true "verified"
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/domains/{id}/verify [patch]
func (h *Handler) VerifyDomain(c *gin.Context) {
	var dto VerifyDomainDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		web.Render(c, web.Wrap(http.StatusBadRequest, "Invalid domain id.", nil))
		return
	}
	d, err := h.svc.VerifyDomain(c.Request.Context(), id, dto.Verified)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Domain updated.", domainItem(*d))
}

// RemoveDomain deletes a custom-domain mapping.
// @Summary Remove tenant domain
// @Tags AdminTenants
// @Success 200 {object} map[string]interface{}
// @Router /admin/tenants/domains/{id} [delete]
func (h *Handler) RemoveDomain(c *gin.Context) {
	id, err := uuid.Parse(c.Param("id"))
	if err != nil {
		web.Render(c, web.Wrap(http.StatusBadRequest, "Invalid domain id.", nil))
		return
	}
	if err := h.svc.RemoveDomain(c.Request.Context(), id); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Domain removed.", nil)
}
