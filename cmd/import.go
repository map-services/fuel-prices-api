package cmd

import (
	"fmt"
	"log/slog"
)

func Import(dbPath string) error {

	client, repo, err := bootstrap(dbPath, "full", true)
	if err != nil {
		return err
	}
	defer func() {
		if err := repo.Close(); err != nil {
			slog.Error("failed to close repository", "error", err)
		}
	}()

	numPFS, dropped, err := client.GetFillingStations(repo.InsertPFS)
	if err != nil {
		return fmt.Errorf("failed to fetch filling stations: %w", err)
	}
	slog.Info("imported filling stations", "count", numPFS, "dropped", dropped)

	numPrices, dropped, err := client.GetFuelPrices(repo.InsertPrices)
	if err != nil {
		return fmt.Errorf("failed to fetch fuel prices: %w", err)
	}
	slog.Info("imported fuel prices", "count", numPrices, "dropped", dropped)

	return nil
}
