package cadmin

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Handler binds control auth HTTP to the Service.
type Handler struct{ svc *Service }

// NewHandler wires a Service to its HTTP handlers.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Login issues a control token.
// @Summary Control admin login
// @Tags AdminAuth
// @Param payload body LoginDTO true "login"
// @Success 200 {object} map[string]interface{}
// @Router /admin/auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	a, tok, err := h.svc.Login(web.MustDB(c), dto.Email, dto.Password)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Logged in.", gin.H{
		"admin": gin.H{"id": a.ID.String(), "email": a.Email, "role": a.Role},
		"token": gin.H{"access_token": tok, "token_type": "Bearer"},
	})
}

// Me returns the authenticated control admin.
// @Summary Current control admin
// @Tags AdminAuth
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /admin/auth/me [get]
func (h *Handler) Me(c *gin.Context) {
	a := CurrentAdmin(c)
	if a == nil {
		web.Render(c, web.Unauthorized("Unauthenticated."))
		return
	}
	web.Success(c, http.StatusOK, "Me.", gin.H{
		"id": a.ID.String(), "email": a.Email, "role": a.Role,
	})
}

// Logout revokes the current control token.
// @Summary Control admin logout
// @Tags AdminAuth
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /admin/auth/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	token := strings.TrimSpace(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer "))
	if err := h.svc.Logout(token); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Logged out.", nil)
}

// ForgotDTO carries the control forgot payload.
type ForgotDTO struct {
	Email string `json:"email" binding:"required,email"`
}

// ResetDTO carries the control reset payload.
type ResetDTO struct {
	Token       string `json:"token" binding:"required"`
	NewPassword string `json:"new_password" binding:"required,min=8"`
}

// Forgot issues a control password-reset link, always reporting success.
// @Summary Request control password reset
// @Tags AdminAuth
// @Param payload body ForgotDTO true "forgot"
// @Success 200 {object} map[string]interface{}
// @Router /admin/auth/forgot [post]
func (h *Handler) Forgot(c *gin.Context) {
	var dto ForgotDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	if err := h.svc.ForgotPassword(web.MustDB(c), dto.Email); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "If an account exists, a reset link was sent.", nil)
}

// Reset consumes a control reset token and sets a new password.
// @Summary Reset control password
// @Tags AdminAuth
// @Param payload body ResetDTO true "reset"
// @Success 200 {object} map[string]interface{}
// @Router /admin/auth/reset [post]
func (h *Handler) Reset(c *gin.Context) {
	var dto ResetDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	if err := h.svc.ResetPassword(web.MustDB(c), dto.Token, dto.NewPassword); err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Password reset.", nil)
}
