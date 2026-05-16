package metrics

import (
	"log"
	"strconv"

	"github.com/map-services/fuel-prices-api/internal/models"
	"github.com/prometheus/client_golang/prometheus"
)

type fuelPricesDistributionCollector struct {
	distDesc *prometheus.Desc
	distFunc func() (*models.DistributionStatistics, error)
}

func (c *fuelPricesDistributionCollector) Describe(ch chan<- *prometheus.Desc) {
	ch <- c.distDesc
}

func (c *fuelPricesDistributionCollector) Collect(ch chan<- prometheus.Metric) {
	stats, err := c.distFunc()
	if err != nil {
		log.Printf("failed to collect fuel price distribution stats: %v", err)
		return
	}

	for _, d := range stats.Distribution {
		postcodeArea := ""
		if d.PostcodeArea != nil {
			postcodeArea = *d.PostcodeArea
		}

		for bucket, count := range d.Buckets {
			ch <- prometheus.MustNewConstMetric(c.distDesc, prometheus.GaugeValue, float64(count), postcodeArea, d.FuelType, strconv.Itoa(bucket))
		}
	}
}

func RegisterFuelDistributionCollector(reg prometheus.Registerer, distFn func() (*models.DistributionStatistics, error)) {
	distLabels := []string{"postcode_area", "fuel_type", "price_bucket"}

	collector := fuelPricesDistributionCollector{
		distDesc: prometheus.NewDesc("fuel_prices_govuk_api_price_distribution", "Price distribution sample size at national and postcode area by fuel_type and price bucket", distLabels, nil),
		distFunc: distFn,
	}

	RegisterOrPanic(reg, &collector)
}
