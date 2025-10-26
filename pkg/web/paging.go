package web

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

// ListFilter standardizes list queries across domains:
// GET /resources?limit=25&offset=0&sort=id&order=DESC&search=foo
type ListFilter struct {
	Limit  int
	Offset int
	Sort   string
	Order  string
	Search string
}

// ListResult wraps paged data with the total count.
type ListResult[T any] struct {
	Data   []T   `json:"data"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

// BindFilter parses query params with safe defaults (limit capped at 100).
func BindFilter(c *gin.Context) ListFilter {
	f := ListFilter{Limit: 25, Offset: 0, Sort: "id", Order: "DESC"}
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			f.Limit = n
		}
	}
	if f.Limit > 100 {
		f.Limit = 100
	}
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			f.Offset = n
		}
	}
	if v := strings.TrimSpace(c.Query("sort")); v != "" {
		f.Sort = v
	}
	if v := strings.ToUpper(strings.TrimSpace(c.Query("order"))); v == "ASC" || v == "DESC" {
		f.Order = v
	}
	f.Search = strings.TrimSpace(c.Query("search"))
	return f
}

// ApplyPaging applies limit/offset/order to a GORM query. Sort column is
// allow-listed by the caller via allowed map to prevent SQL injection.
func ApplyPaging(db *gorm.DB, f ListFilter, allowedSorts map[string]string) *gorm.DB {
	col, ok := allowedSorts[f.Sort]
	if !ok {
		col = allowedSorts["id"]
		if col == "" {
			col = "id"
		}
	}
	order := "DESC"
	if f.Order == "ASC" {
		order = "ASC"
	}
	return db.Order(col + " " + order).Limit(f.Limit).Offset(f.Offset)
}

// PagedResult builds a ListResult from data + total + filter.
func PagedResult[T any](data []T, total int64, f ListFilter) ListResult[T] {
	if data == nil {
		data = []T{}
	}
	return ListResult[T]{Data: data, Total: total, Limit: f.Limit, Offset: f.Offset}
}
