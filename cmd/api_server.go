package cmd

import (
	"fmt"
	"log/slog"
	"net/http"

	"github.com/Depado/ginprom"
	"github.com/aurowora/compress"
	"github.com/gin-contrib/cors"
	"github.com/gin-contrib/pprof"
	"github.com/gin-gonic/gin"
	_ "github.com/mattn/go-sqlite3"

	"github.com/map-services/fuel-prices-api/internal"
	"github.com/map-services/fuel-prices-api/internal/middleware"
	"github.com/map-services/fuel-prices-api/internal/routes"
	healthcheck "github.com/tavsec/gin-healthcheck"
	"github.com/tavsec/gin-healthcheck/checks"
	hc_config "github.com/tavsec/gin-healthcheck/config"
)

func ApiServer(dbPath string, port int, refresh string, debug bool) error {

	client, repo, err := bootstrap(dbPath, refresh, debug)
	if err != nil {
		return err
	}
	defer func() {
		if err := repo.Close(); err != nil {
			slog.Error("failed to close repository", "error", err)
		}
	}()

	if _, err := internal.StartCron(client, repo); err != nil {
		return fmt.Errorf("failed to start CRON jobs: %w", err)
	}

	r := gin.New()

	prometheus := ginprom.New(
		ginprom.Engine(r),
		ginprom.Path("/metrics"),
		ginprom.Ignore("/healthz"),
	)

	r.Use(
		gin.Recovery(),
		middleware.RequestLogger(slog.Default(), "/healthz", "/metrics"),
		prometheus.Instrument(),
		compress.Compress(),
		cors.Default(),
	)

	if debug {
		slog.Warn("pprof endpoints are enabled and exposed. Do not run with this flag in production.")
		pprof.Register(r)
	}

	err = healthcheck.New(r, hc_config.DefaultConfig(), []checks.Check{
		repo.Check(),
	})
	if err != nil {
		return fmt.Errorf("failed to initialize healthcheck: %v", err)
	}

	r.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "Route not found"})
	})

	r.NoMethod(func(c *gin.Context) {
		c.JSON(http.StatusMethodNotAllowed, gin.H{"error": "Method not allowed"})
	})

	v1 := r.Group("/v1/fuel-prices")
	v1.GET("/search", routes.Search(repo, client))
	v1.GET("/history/:node_id/:fuel_type", routes.PriceHistory(repo, client))
	v1.GET("/stats/snapshot", routes.SnapshotStats(repo))
	v1.GET("/stats/distribution", routes.DistributionStats(repo))

	addr := fmt.Sprintf(":%d", port)
	slog.Info("Starting HTTP API Server", "port", port)
	if err := r.Run(addr); err != nil && err != http.ErrServerClosed {
		return fmt.Errorf("HTTP API Server failed to start on port %d: %v", port, err)
	}

	return nil
}
