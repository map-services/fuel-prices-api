package stats

import (
	"fmt"
	"math"
	"time"

	"github.com/map-services/fuel-prices-api/internal/models"
)

const stalenessThreshold = 14 * 24 * time.Hour

func Derive(results []models.SearchResult, bucketSize int) *models.SearchStatistics {
	if bucketSize <= 0 {
		bucketSize = 3
	}
	stats := &models.SearchStatistics{
		CheapestPfs:       make(map[string][]string),
		LowestPrice:       make(map[string]float64),
		AveragePrice:      make(map[string]float64),
		HighestPrice:      make(map[string]float64),
		PriceDistribution: make(map[string]map[string]int),
		StandardDeviation: make(map[string]float64),
		BrandDistribution: make(map[string]int),
	}

	// Group prices by fuel type
	fuelTypePrices := make(map[string][]float64)
	fuelTypeStations := make(map[string]map[float64][]string) // price -> station names

	now := time.Now().UTC()

	for _, result := range results {
		hasFreshPrice := false
		for fuelType, priceInfos := range result.FuelPrices {
			if len(priceInfos) == 0 {
				continue
			}

			// Use the most recent price (first in slice)
			priceInfo := priceInfos[0]
			if now.Sub(priceInfo.UpdatedOn) > stalenessThreshold {
				continue
			}

			hasFreshPrice = true
			price := priceInfo.Price
			fuelTypePrices[fuelType] = append(fuelTypePrices[fuelType], price)

			if fuelTypeStations[fuelType] == nil {
				fuelTypeStations[fuelType] = make(map[float64][]string)
			}
			fuelTypeStations[fuelType][price] = append(fuelTypeStations[fuelType][price], result.NodeId)
		}

		// Brand distribution - count results by retailer, but only if they have at least one fresh price
		if hasFreshPrice && result.Retailer != nil {
			stats.BrandDistribution[result.Retailer.Name]++
		}
	}

	for fuelType, prices := range fuelTypePrices {
		if len(prices) == 0 {
			continue
		}

		// Lowest/avg/hightest price and cheapest stations
		lowestPrice := prices[0]
		highestPrice := prices[0]
		sum := 0.0

		for _, p := range prices {
			if p < lowestPrice {
				lowestPrice = p
			}
			if p > highestPrice {
				highestPrice = p
			}
			sum += p
		}
		stats.LowestPrice[fuelType] = lowestPrice
		stats.HighestPrice[fuelType] = highestPrice
		stats.CheapestPfs[fuelType] = fuelTypeStations[fuelType][lowestPrice]

		avgPrice := sum / float64(len(prices))
		stats.AveragePrice[fuelType] = math.Round(avgPrice*10) / 10

		// Standard deviation and Price Distribution
		stats.PriceDistribution[fuelType] = make(map[string]int)
		variance := 0.0
		for _, p := range prices {
			diff := p - avgPrice
			variance += diff * diff

			price := int(p)
			bucketStart := (price / bucketSize) * bucketSize
			bucketEnd := bucketStart + bucketSize - 1
			bucketKey := fmt.Sprintf("%d-%d", bucketStart, bucketEnd)
			stats.PriceDistribution[fuelType][bucketKey]++
		}

		if len(prices) > 1 {
			variance /= float64(len(prices))
			stats.StandardDeviation[fuelType] = math.Sqrt(variance)
		}
	}

	return stats
}
