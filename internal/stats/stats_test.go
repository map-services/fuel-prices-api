package stats

import (
	"testing"
	"time"

	"github.com/map-services/fuel-prices-api/internal/models"
	"github.com/stretchr/testify/assert"
)

func TestDerive_StalePriceFiltering(t *testing.T) {
	now := time.Now().UTC()
	staleDate := now.Add(-stalenessThreshold - time.Hour)
	freshDate := now.Add(-stalenessThreshold + time.Hour)

	results := []models.SearchResult{
		{
			PetrolFillingStation: models.PetrolFillingStation{NodeId: "FreshStation"},
			FuelPrices: map[string][]models.PriceInfo{
				"E10": {
					{Price: 140.0, UpdatedOn: freshDate},
				},
			},
		},
		{
			PetrolFillingStation: models.PetrolFillingStation{NodeId: "StaleStation"},
			FuelPrices: map[string][]models.PriceInfo{
				"E10": {
					{Price: 130.0, UpdatedOn: staleDate}, // Cheaper but stale
				},
			},
		},
	}

	stats := Derive(results, 3)

	// After filtering, the stale station should be ignored.
	// Lowest price should be 140.0, not 130.0.
	assert.Equal(t, 140.0, stats.LowestPrice["E10"])
	assert.Equal(t, []string{"FreshStation"}, stats.CheapestPfs["E10"])
}
