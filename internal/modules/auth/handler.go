package auth

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

type Handler struct{ svc *Service }

// NewHandler wires an auth Service to its HTTP handlers.
func NewHandler(svc *Service) *Handler { return &Handler{svc: svc} }

// Signup registers a new member account.
// @Summary Register
// @Tags Auth
// @Param payload body SignupDTO true "signup"
// @Success 201 {object} map[string]interface{}
// @Router /auth/signup [post]
func (h *Handler) Signup(c *gin.Context) {
	var dto SignupDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	u, err := h.svc.Register(web.MustDB(c), dto)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusCreated, "Registered.", gin.H{"id": u.ID, "email": u.Email, "role": u.Role})
}

// Login verifies credentials and issues a token pair.
// @Summary Login (JWT + session)
// @Tags Auth
// @Param payload body LoginDTO true "login"
// @Success 200 {object} map[string]interface{}
// @Router /auth/login [post]
func (h *Handler) Login(c *gin.Context) {
	var dto LoginDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	u, pair, err := h.svc.Login(web.MustDB(c), dto)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Logged in.", gin.H{"user": gin.H{"id": u.ID, "email": u.Email, "role": u.Role}, "token": pair})
}

// Logout invalidates the current session token.
// @Summary Logout (invalidate jti)
// @Tags Auth
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /auth/logout [post]
func (h *Handler) Logout(c *gin.Context) {
	if cl := web.CurrentClaims(c); cl != nil {
		if err := h.svc.Logout(cl.SessionID); err != nil {
			web.Render(c, err)
			return
		}
	}
	web.Success(c, http.StatusOK, "Logged out.", nil)
}

// Me returns the authenticated caller's claims.
// @Summary Current user
// @Tags Auth
// @Security Bearer
// @Success 200 {object} map[string]interface{}
// @Router /auth/me [get]
func (h *Handler) Me(c *gin.Context) {
	web.Success(c, http.StatusOK, "Me.", web.CurrentClaims(c))
}

// Refresh rotates a refresh token into a new token pair.
// @Summary Refresh access token (rotation)
// @Tags Auth
// @Param payload body RefreshDTO true "refresh"
// @Success 200 {object} map[string]interface{}
// @Router /auth/refresh [post]
func (h *Handler) Refresh(c *gin.Context) {
	var dto RefreshDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	u, pair, err := h.svc.Refresh(web.MustDB(c), dto.RefreshToken)
	if err != nil {
		web.Render(c, err)
		return
	}
	web.Success(c, http.StatusOK, "Refreshed.", gin.H{"user": gin.H{"id": u.ID, "email": u.Email, "role": u.Role}, "token": pair})
}

// Check reports whether a token is valid without side effects.
// @Summary Validate a token without side effects
// @Tags Auth
// @Param payload body CheckDTO true "token"
// @Success 200 {object} map[string]interface{}
// @Router /auth/check [post]
func (h *Handler) Check(c *gin.Context) {
	var dto CheckDTO
	if err := c.ShouldBindJSON(&dto); err != nil {
		web.ValidationErrors(c, err)
		return
	}
	res := h.svc.Check(dto.Token)
	if !res.Valid {
		web.Render(c, web.Unauthorized("Invalid token."))
		return
	}
	web.Success(c, http.StatusOK, "Token valid.", res)
}
