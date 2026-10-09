package web

import (
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

type ListFilter struct {
	Limit  int
	Offset int
	Sort   string
	Order  string
	Search string
}

type ListResult[T any] struct {
	Data   []T   `json:"data"`
	Total  int64 `json:"total"`
	Limit  int   `json:"limit"`
	Offset int   `json:"offset"`
}

func BindFilter(c *gin.Context) ListFilter {
	filter := ListFilter{Limit: 25, Offset: 0, Sort: "id", Order: "DESC"}
	if v := strings.TrimSpace(c.Query("limit")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			filter.Limit = n
		}
	}
	if filter.Limit > 100 {
		filter.Limit = 100
	}
	if v := strings.TrimSpace(c.Query("offset")); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			filter.Offset = n
		}
	}
	if v := strings.TrimSpace(c.Query("sort")); v != "" {
		filter.Sort = v
	}
	if v := strings.ToUpper(strings.TrimSpace(c.Query("order"))); v == "ASC" || v == "DESC" {
		filter.Order = v
	}
	filter.Search = strings.TrimSpace(c.Query("search"))
	return filter
}

// ApplyPaging applies limit/offset/order; sort is allow-listed against SQL injection.
func ApplyPaging(db *gorm.DB, filter ListFilter, allowedSorts map[string]string) *gorm.DB {
	col, ok := allowedSorts[filter.Sort]
	if !ok {
		col = allowedSorts["id"]
		if col == "" {
			col = "id"
		}
	}
	order := "DESC"
	if filter.Order == "ASC" {
		order = "ASC"
	}
	return db.Order(col + " " + order).Limit(filter.Limit).Offset(filter.Offset)
}

func PagedResult[T any](data []T, total int64, filter ListFilter) ListResult[T] {
	if data == nil {
		data = []T{}
	}
	return ListResult[T]{Data: data, Total: total, Limit: filter.Limit, Offset: filter.Offset}
}
