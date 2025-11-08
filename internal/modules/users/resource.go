package users

import (
	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// UserResource shapes a User for JSON output, Laravel-resource style.
// Email is visible to the user themselves and admins; activity state and
// timestamps are admin-only. Everyone sees id, name, and role.
type UserResource struct {
	user   User
	viewer *web.Claims
}

// NewUserResource wraps one user for the viewer (nil viewer = guest,
// though the listing endpoint always authenticates).
func NewUserResource(user User, viewer *web.Claims) UserResource {
	return UserResource{user: user, viewer: viewer}
}

func (r UserResource) isSelf() bool {
	return r.viewer != nil && r.viewer.UserID == r.user.ID
}

func (r UserResource) isAdmin() bool {
	return r.viewer != nil && r.viewer.Role == web.RoleAdmin
}

// ToMap renders the resource with role-gated fields.
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
		out["is_active"] = u.IsActive
		out["created_at"] = u.CreatedAt
		out["updated_at"] = u.UpdatedAt
	}
	return out
}

// UserCollection renders a paged list through the resource, preserving the
// total/limit/offset keys so clients see the same envelope as raw pages.
func UserCollection(page web.ListResult[User], viewer *web.Claims) gin.H {
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
