package app

import (
	"context"
	"database/sql"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/redis/go-redis/v9"
	swaggerFiles "github.com/swaggo/files"
	ginSwagger "github.com/swaggo/gin-swagger"
	"gorm.io/gorm"

	// Blank imports fire init() self-registrations (modules, globals,
	// errors). New code must only capture deps at registration and touch
	// them when serving, so `route:list` works with nil backends.
	_ "github.com/idehen-divine/GinPlate/internal/exceptions"
	_ "github.com/idehen-divine/GinPlate/internal/mail"
	_ "github.com/idehen-divine/GinPlate/internal/middleware"
	_ "github.com/idehen-divine/GinPlate/internal/modules"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// listingKey is a dummy HMAC key used only to build the router for
// `route:list`. It is never used to sign or verify tokens at runtime
// because the command only inspects gin route metadata and never serves
// requests.
var listingKey = []byte("route-list-dummy-key-32-bytes-long!")

// RegisterAPIRoutes mounts /api/v1 from every registered module, so `serve`
// and `route:list` share one route set.
func RegisterAPIRoutes(router *gin.Engine, d *web.ModuleDeps) {
	v1 := router.Group("/api/v1")
	for _, m := range web.RegisteredModules() {
		m.Fn(v1, d)
	}
}

// RegisterInfraRoutes mounts liveness, readiness, health, metrics, the
// public-disk static route, and optional swagger. Nil backends are safe:
// handlers are registered, never run (listing).
func RegisterInfraRoutes(router *gin.Engine, databaseConnection *gorm.DB, sqlDatabase *sql.DB, rdb *redis.Client, publicURL, publicRoot string, swagger, metrics bool) {
	router.NoRoute(func(c *gin.Context) {
		web.Render(c, web.NotFound("Not found."))
	})
	router.GET("/livez", func(c *gin.Context) {
		web.Success(c, http.StatusOK, "ok", gin.H{})
	})
	router.GET("/readyz", func(c *gin.Context) {
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()
		if sqlDatabase == nil {
			web.Fail(c, http.StatusServiceUnavailable, "Not ready.", nil)
			return
		}
		if err := sqlDatabase.PingContext(ctx); err != nil {
			web.Fail(c, http.StatusServiceUnavailable, "Not ready.", nil)
			return
		}
		if rdb != nil {
			if err := rdb.Ping(ctx).Err(); err != nil {
				web.Fail(c, http.StatusServiceUnavailable, "Not ready.", nil)
				return
			}
		}
		web.Success(c, http.StatusOK, "ok", gin.H{})
	})
	// Snapshot gauges under a bounded context: slow metadata queries are
	// omitted instead of stalling probes, and missing tables (fresh,
	// unmigrated databases) simply report nothing — never 500.
	router.GET("/health", func(c *gin.Context) {
		data := gin.H{}
		if databaseConnection != nil {
			ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
			defer cancel()
			queryDatabase := databaseConnection.WithContext(ctx)
			var depth int64
			if err := queryDatabase.Table("jobs").Where("available_at <= ?", time.Now()).Count(&depth).Error; err == nil {
				data["queue_depth"] = depth
			}
			var failed int64
			if err := queryDatabase.Table("failed_jobs").Count(&failed).Error; err == nil {
				data["failed_jobs"] = failed
			}
		}
		web.Success(c, http.StatusOK, "ok", data)
	})
	if metrics {
		router.GET("/metrics", func(c *gin.Context) {
			var depth, failed int64
			if databaseConnection != nil {
				ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
				defer cancel()
				queryDatabase := databaseConnection.WithContext(ctx)
				_ = queryDatabase.Table("jobs").Where("available_at <= ?", time.Now()).Count(&depth).Error
				_ = queryDatabase.Table("failed_jobs").Count(&failed).Error
			}
			c.Header("Content-Type", "text/plain; version=0.0.4")
			c.String(http.StatusOK, "# HELP ginplate_queue_depth Due jobs awaiting a worker.\n"+
				"# TYPE ginplate_queue_depth gauge\nginplate_queue_depth %d\n"+
				"# HELP ginplate_failed_jobs Buried jobs awaiting retry or deletion.\n"+
				"# TYPE ginplate_failed_jobs gauge\nginplate_failed_jobs %d\n",
				depth, failed)
		})
	}
	if publicRoot == "" {
		publicRoot = "storage/app/public"
	}
	router.Static(storageRoutePath(publicURL), publicRoot)
	if swagger {
		router.GET("/swagger/*any", ginSwagger.WrapHandler(swaggerFiles.Handler))
	}
}

// NewListingRouter builds the same routes RunAPI serves without connecting
// to any backend, so `route:list` works config-free.
func NewListingRouter(publicURL, publicRoot string, swagger, metrics, debug bool, appName, appURL string) *gin.Engine {
	gin.SetMode(gin.TestMode)
	router := gin.New()
	var store session.Store // nil: signature-only auth, never executed here

	RegisterAPIRoutes(router, &web.ModuleDeps{
		Key: listingKey, Store: store, Debug: debug,
		AppName: appName, AppURL: appURL, AccessTTLMin: 60,
	})
	RegisterInfraRoutes(router, nil, nil, nil, publicURL, publicRoot, swagger, metrics)
	return router
}
