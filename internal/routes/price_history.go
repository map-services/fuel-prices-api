package routes

import (
	"log"
	"net/http"

	"github.com/map-services/fuel-prices-api/internal"
	"github.com/map-services/fuel-prices-api/internal/models"

	"github.com/gin-gonic/gin"
)

func PriceHistory(repo internal.FuelPricesRepository, client internal.FuelPricesClient) func(c *gin.Context) {
	return func(c *gin.Context) {

		nodeId := c.Param("node_id")
		fuelType := c.Param("fuel_type")

		fuelTypes, err := repo.FuelTypes()
		if err != nil {
			log.Printf("error while fetching fuel types: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An internal server error occurred"})
			return
		}

		if _, exists := fuelTypes[fuelType]; !exists {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown fuel type: " + fuelType})
			return
		}

		results, err := repo.PriceHistory(nodeId, fuelType)

		if err != nil {
			log.Printf("error while fetching price history: %v", err)
			c.JSON(http.StatusInternalServerError, gin.H{"error": "An internal server error occurred"})
			return
		}

		c.JSON(http.StatusOK, models.PriceHistoryResponse{
			Results:     results,
			Attribution: internal.ATTRIBUTION,
			LastUpdated: client.LastUpdated(),
		})
	}
}
