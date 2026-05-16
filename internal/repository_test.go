package internal

import (
	"os"
	"testing"
	"time"

	"github.com/map-services/fuel-prices-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func setupTestDB(t *testing.T) FuelPricesRepository {
	tmpFile, err := os.CreateTemp("", "fuel_prices_test-*.db")
	require.NoError(t, err)
	dbPath := tmpFile.Name()
	_ = tmpFile.Close()

	db, err := Connect(dbPath)
	require.NoError(t, err)
	db.SetMaxOpenConns(1)

	err = Migrate("../migrations", dbPath)
	require.NoError(t, err)

	t.Cleanup(func() {
		require.NoError(t, db.Close())
		_ = os.Remove(dbPath)
	})

	return NewFuelPricesRepository(db, &models.Retailers{})
}

func TestFetchPricesIntegration(t *testing.T) {
	repo := setupTestDB(t)

	now := time.Now().UTC().Truncate(time.Second)

	pfs1 := models.PetrolFillingStation{
		NodeId: "node-1",
		Location: models.Location{
			Latitude:  51.5,
			Longitude: -0.1,
		},
		FuelTypes: []string{"E10", "B7"},
	}

	pfs2 := models.PetrolFillingStation{
		NodeId: "node-2",
		Location: models.Location{
			Latitude:  52.0,
			Longitude: 0.0,
		},
		FuelTypes: []string{"E10"},
	}

	_, _, err := repo.InsertPFS([]models.PetrolFillingStation{pfs1, pfs2})
	require.NoError(t, err)

	prices := []models.ForecourtPrices{
		{
			NodeId: "node-1",
			FuelPrices: []models.FuelPrice{
				{FuelType: "E10", Price: 140.9, PriceLastUpdated: now.Add(-4 * time.Hour)},
				{FuelType: "E10", Price: 142.9, PriceLastUpdated: now.Add(-3 * time.Hour)},
				{FuelType: "E10", Price: 142.9, PriceLastUpdated: now.Add(-2 * time.Hour)},
				{FuelType: "E10", Price: 142.9, PriceLastUpdated: now.Add(-1 * time.Hour)}, // Same price as previous
				{FuelType: "E10", Price: 141.9, PriceLastUpdated: now},
				{FuelType: "B7", Price: 150.9, PriceLastUpdated: now},
			},
		},
		{
			NodeId: "node-2",
			FuelPrices: []models.FuelPrice{
				{FuelType: "E10", Price: 139.9, PriceLastUpdated: now},
			},
		},
	}

	_, _, err = repo.InsertPrices(prices)
	require.NoError(t, err)

	t.Run("Bounding box filtering", func(t *testing.T) {
		// Box containing only node-1
		results, err := repo.Search([]float64{-0.2, 51.4, 0.0, 51.6}, 1)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "node-1", results[0].NodeId)

		// Box containing only node-2
		results, err = repo.Search([]float64{-0.1, 51.9, 0.1, 52.1}, 1)
		require.NoError(t, err)
		assert.Len(t, results, 1)
		assert.Equal(t, "node-2", results[0].NodeId)

		// Box containing both
		results, err = repo.Search([]float64{-0.2, 51.0, 0.2, 53.0}, 1)
		require.NoError(t, err)
		assert.Len(t, results, 2)

		// Box containing neither
		results, err = repo.Search([]float64{1.0, 1.0, 2.0, 2.0}, 1)
		require.NoError(t, err)
		assert.Empty(t, results)
	})

	t.Run("Latest price per fuel type (perTypeLimit=1)", func(t *testing.T) {
		results, err := repo.Search([]float64{-0.2, 51.4, 0.0, 51.6}, 1)
		require.NoError(t, err)
		require.Len(t, results, 1)

		p := results[0].FuelPrices
		require.Contains(t, p, "E10")
		require.Contains(t, p, "B7")

		assert.Len(t, p["E10"], 1)
		assert.Equal(t, 141.9, p["E10"][0].Price)
		assert.True(t, p["E10"][0].UpdatedOn.Equal(now))

		assert.Len(t, p["B7"], 1)
		assert.Equal(t, 150.9, p["B7"][0].Price)
	})

	t.Run("Historical prices and deduplication (perTypeLimit=5)", func(t *testing.T) {
		results, err := repo.Search([]float64{-0.2, 51.4, 0.0, 51.6}, 5)
		require.NoError(t, err)
		require.Len(t, results, 1)

		p := results[0].FuelPrices
		require.Contains(t, p, "E10")

		// Expected E10 prices:
		// 1. 141.9 at now
		// 2. 142.9 at now-1h (now-2h and now-3h are both skipped because they're same as now-1h)
		// 3. 140.9 at now-4h
		assert.Len(t, p["E10"], 3)
		assert.Equal(t, 141.9, p["E10"][0].Price)
		assert.Equal(t, 142.9, p["E10"][1].Price)
		assert.Equal(t, 140.9, p["E10"][2].Price)

		assert.True(t, p["E10"][0].UpdatedOn.Equal(now))
		assert.True(t, p["E10"][1].UpdatedOn.Equal(now.Add(-1*time.Hour)))
		assert.True(t, p["E10"][2].UpdatedOn.Equal(now.Add(-4*time.Hour)))
	})

	t.Run("Limit per type", func(t *testing.T) {
		results, err := repo.Search([]float64{-0.2, 51.4, 0.0, 51.6}, 2)
		require.NoError(t, err)
		require.Len(t, results, 1)

		p := results[0].FuelPrices
		require.Contains(t, p, "E10")
		assert.Len(t, p["E10"], 2)
		assert.Equal(t, 141.9, p["E10"][0].Price)
		assert.Equal(t, 142.9, p["E10"][1].Price)
	})
}
