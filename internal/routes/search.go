package routes

import (
	"fmt"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"strings"

	"github.com/map-services/fuel-prices-api/internal"
	"github.com/map-services/fuel-prices-api/internal/models"
	"github.com/map-services/fuel-prices-api/internal/stats"

	"github.com/gin-gonic/gin"
)

const MAX_BOUNDS = 50_000 // Maximum bounds in meters (50 KM)

func Search(repo internal.FuelPricesRepository, client internal.FuelPricesClient) func(c *gin.Context) {
	return func(c *gin.Context) {
		bbox, err := parseBBox(c.Query("bbox"))
		if err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": err.Error()})
			return
		}

		limitStr := c.Query("limit")
		limit := 1 // default to 1 (most recent only) if not provided
		if limitStr != "" {
			l, lerr := strconv.Atoi(limitStr)
			if lerr != nil || l < 0 {
				c.JSON(http.StatusBadRequest, gin.H{"error": "invalid limit parameter"})
				return
			}
			limit = l
		}

		results, err := repo.Search(bbox, limit)

		if err != nil {
			slog.Error("error while fetching fuel prices", "error", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An internal server error occurred"})
			return
		}

		c.JSON(http.StatusOK, models.SearchResponse{
			Results:     results,
			Attribution: internal.ATTRIBUTION,
			Statistics:  stats.Derive(results, 3),
			LastUpdated: client.LastUpdated(),
		})
	}
}

func parseBBox(bboxStr string) ([]float64, error) {
	bboxParts := strings.Split(bboxStr, ",")
	if len(bboxParts) != 4 {
		return nil, fmt.Errorf("bbox must have 4 comma-separated values")
	}

	bbox := make([]float64, 4)
	for i, part := range bboxParts {
		val, err := strconv.ParseFloat(strings.TrimSpace(part), 64)
		if err != nil {
			return nil, fmt.Errorf("invalid bbox value '%s': not a valid float", part)
		}
		bbox[i] = val
	}

	latSpan := bbox[3] - bbox[1]
	lonSpan := bbox[2] - bbox[0]
	avgLatRad := (bbox[1] + bbox[3]) / 2 * math.Pi / 180.0

	if math.Abs(latSpan)*111132 > MAX_BOUNDS || math.Abs(lonSpan)*111132*math.Cos(avgLatRad) > MAX_BOUNDS {
		return nil, fmt.Errorf("bbox must define a valid area (no more than %d KM in either dimension)", MAX_BOUNDS/1000)
	}

	return bbox, nil
}
