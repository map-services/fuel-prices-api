package main

import (
	"log"

	"github.com/rm-hull/fuel-prices-api/cmd"

	"github.com/spf13/cobra"
)

func main() {
	var err error
	var dbPath string
	var filePath string
	var port int
	var debug bool
	var refresh string

	rootCmd := &cobra.Command{
		Use:  "fuel-prices",
		Long: `Fuel Prices API`,
	}

	apiServerCmd := &cobra.Command{
		Use:   "api-server [--db <path>] [--port <port>] [--debug] [--refresh=full|never|incremental]",
		Short: "Start HTTP API server",
		Run: func(_ *cobra.Command, _ []string) {
			if err = cmd.ApiServer(dbPath, port, refresh, debug); err != nil {
				log.Fatalf("API Server failed: %v", err)
			}
		},
	}

	importCmd := &cobra.Command{
		Use:   "import [--db <path>]",
		Short: "Perform one-off import of fuel prices and filling stations from the GOV.UK API",
		Run: func(_ *cobra.Command, _ []string) {
			if err := cmd.Import(dbPath); err != nil {
				log.Fatalf("Import failed: %v", err)
			}
		},
	}

	updateFaviconsCmd := &cobra.Command{
		Use:   "favicons [--file <path>]",
		Short: "Update favicons",
		Run: func(_ *cobra.Command, _ []string) {
			if err := cmd.UpdateFaviconsInCSV(filePath); err != nil {
				log.Fatalf("Update favicons failed: %v", err)
			}
		},
	}
	updateFaviconsCmd.Flags().StringVar(&filePath, "file", "./internal/brands/retailers.csv", "Path to retailers CSV file")

	apiServerCmd.Flags().IntVar(&port, "port", 8080, "Port to run HTTP server on")
	apiServerCmd.Flags().BoolVar(&debug, "debug", false, "Enable debugging (pprof) - WARING: do not enable in production")
	apiServerCmd.Flags().StringVar(&refresh, "refresh", "incremental", "when set to 'full', always fetch all PFS and fuel prices, when set to 'never' never fetch from fuel finder API, else only fetch updated stations/prices since last successful fetch")

	rootCmd.AddCommand(apiServerCmd)
	rootCmd.AddCommand(importCmd)
	rootCmd.AddCommand(updateFaviconsCmd)
	rootCmd.PersistentFlags().StringVar(&dbPath, "db", "./data/fuel_prices.db", "Path to fuel-prices SQLite database")

	if err = rootCmd.Execute(); err != nil {
		log.Fatal(err)
	}
}
