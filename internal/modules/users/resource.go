package users

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// UserResource shapes a User for JSON output. Email shows to self and
// admins; activity state and timestamps to admins only.
type UserResource struct {
	user   User
	viewer *middleware.Claims
}

func NewUserResource(user User, viewer *middleware.Claims) UserResource {
	return UserResource{user: user, viewer: viewer}
}

func (r UserResource) isSelf() bool {
	return r.viewer != nil && r.viewer.UserID == r.user.ID
}

func (r UserResource) isAdmin() bool {
	return r.viewer != nil && r.viewer.Role == middleware.RoleAdmin
}

func (r UserResource) ToMap() gin.H {
	u := r.user
	out := gin.H{
		"id":   u.ID.String(),
		"name": u.Name,
		"role": u.Role,
	}
	if r.isSelf() || r.isAdmin() {
		out["email"] = u.Email
	}
	if r.isAdmin() {
		out["tenant_id"] = u.TenantID.String()
		out["is_active"] = u.IsActive
		out["created_at"] = u.CreatedAt
		out["updated_at"] = u.UpdatedAt
	}
	return out
}

func UserCollection(page web.ListResult[User], viewer *middleware.Claims) gin.H {
	items := make([]gin.H, 0, len(page.Data))
	for _, u := range page.Data {
		items = append(items, NewUserResource(u, viewer).ToMap())
	}
	return gin.H{
		"data":   items,
		"total":  page.Total,
		"limit":  page.Limit,
		"offset": page.Offset,
	}
}
