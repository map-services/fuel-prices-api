package internal

import (
	"log/slog"

	"github.com/robfig/cron/v3"
)

const CRON_SCHEDULE_PFS = "0 */6 * * *"     // Every 6 hours
const CRON_SCHEDULE_PRICES = "10 */1 * * *" // Every hour

func StartCron(client FuelPricesClient, repo FuelPricesRepository) (*cron.Cron, error) {

	c := cron.New()

	slog.Info("Starting CRON jobs to update petrol filling stations and fuel prices")

	if _, err := c.AddFunc(CRON_SCHEDULE_PFS, func() {
		if !client.ShouldRefresh() {
			slog.Info("Skipping filling stations fetch due to refresh policy")
			return
		}
		numPFS, dropped, err := client.GetFillingStations(repo.InsertPFS)
		if err != nil {
			slog.Error("Error fetching PFS", "error", err, "dropped", dropped)
			return
		}
		slog.Info("Inserted PFS", "count", numPFS)
	}); err != nil {
		return nil, err
	}

	if _, err := c.AddFunc(CRON_SCHEDULE_PRICES, func() {
		if !client.ShouldRefresh() {
			slog.Info("Skipping fuel prices fetch due to refresh policy")
			return
		}
		numPrices, dropped, err := client.GetFuelPrices(repo.InsertPrices)
		if err != nil {
			slog.Error("Error fetching fuel prices", "error", err)
			return
		}
		slog.Info("Inserted fuel prices", "count", numPrices, "dropped", dropped)
	}); err != nil {
		return nil, err
	}

	c.Start()
	return c, nil
}
