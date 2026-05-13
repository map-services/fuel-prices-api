package internal

import (
	"log"

	"github.com/robfig/cron/v3"
)

const CRON_SCHEDULE_PFS = "0 */6 * * *"     // Every 6 hours
const CRON_SCHEDULE_PRICES = "10 */1 * * *" // Every hour

func StartCron(client FuelPricesClient, repo FuelPricesRepository) (*cron.Cron, error) {

	c := cron.New()

	log.Print("Starting CRON jobs to update petrol filling stations and fuel prices")

	if _, err := c.AddFunc(CRON_SCHEDULE_PFS, func() {
		if !client.ShouldRefresh() {
			log.Print("Skipping filling stations fetch due to refresh policy")
			return
		}
		numPFS, dropped, err := client.GetFillingStations(repo.InsertPFS)
		if err != nil {
			log.Printf("Error fetching PFS: %v (dropped: %d) \n", err, dropped)
			return
		}
		log.Printf("Inserted %d PFS", numPFS)
	}); err != nil {
		return nil, err
	}

	if _, err := c.AddFunc(CRON_SCHEDULE_PRICES, func() {
		if !client.ShouldRefresh() {
			log.Print("Skipping fuel prices fetch due to refresh policy")
			return
		}
		numPrices, dropped, err := client.GetFuelPrices(repo.InsertPrices)
		if err != nil {
			log.Printf("Error fetching fuel prices: %v\n", err)
			return
		}
		log.Printf("Inserted %d fuel prices (dropped: %d) ", numPrices, dropped)
	}); err != nil {
		return nil, err
	}

	c.Start()
	return c, nil
}
