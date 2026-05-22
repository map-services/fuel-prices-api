package routes

import (
	"log/slog"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/map-services/fuel-prices-api/internal"
	"github.com/map-services/fuel-prices-api/internal/models"
)

func SnapshotStats(repo internal.FuelPricesRepository) func(c *gin.Context) {
	return func(c *gin.Context) {
		stats, err := repo.SnapshotStats()

		if err != nil {
			slog.Error("error while fetching snapshot stats", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An internal server error occurred"})
			return
		}

		c.JSON(http.StatusOK, models.SnapshotResponse{
			SnapshotStatistics: *stats,
			Attribution:        internal.ATTRIBUTION,
		})
	}
}

func DistributionStats(repo internal.FuelPricesRepository) func(c *gin.Context) {
	return func(c *gin.Context) {
		stats, err := repo.DistributionStats()

		if err != nil {
			slog.Error("error while fetching distribution stats", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An internal server error occurred"})
			return
		}

		c.JSON(http.StatusOK, models.DistributionResponse{
			DistributionStatistics: *stats,
			Attribution:            internal.ATTRIBUTION,
		})
	}
}
