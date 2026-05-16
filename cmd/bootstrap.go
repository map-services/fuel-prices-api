package cmd

import (
	"fmt"
	"log"
	"os"
	"time"

	"github.com/earthboundkid/versioninfo/v2"
	"github.com/getsentry/sentry-go"
	"github.com/joho/godotenv"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/rm-hull/godx"

	"github.com/map-services/fuel-prices-api/internal"
	"github.com/map-services/fuel-prices-api/internal/brands"
	"github.com/map-services/fuel-prices-api/internal/metrics"
)

// bootstrap initialises shared resources used by both the API server and import
// commands. It returns the authenticated client, a repository, and an error
// if something failed during startup.
func bootstrap(dbPath, refresh string, debug bool) (internal.FuelPricesClient, internal.FuelPricesRepository, error) {
	if err := godotenv.Load(); err != nil {
		log.Println("No .env file found")
	}

	environment := "development"
	if os.Getenv("ENVIRONMENT") != "" {
		environment = os.Getenv("ENVIRONMENT")
	}
	err := sentry.Init(sentry.ClientOptions{
		Dsn:         os.Getenv("SENTRY_DSN"),
		Debug:       debug,
		Release:     versioninfo.Revision[:7],
		Environment: environment,
		EnableLogs:  true,
	})
	if err != nil {
		return nil, nil, fmt.Errorf("sentry initialization failed: %w", err)
	}
	defer sentry.Flush(2 * time.Second)

	godx.GitVersion()
	godx.EnvironmentVars()
	godx.UserInfo()

	clientId := os.Getenv("CLIENT_ID")
	clientSecret := os.Getenv("CLIENT_SECRET")

	client, err := internal.NewFuelPricesClient(clientId, clientSecret, refresh)
	if err != nil {
		return nil, nil, fmt.Errorf("GOV.UK authentication failed: %w", err)
	}

	db, err := internal.Connect(dbPath)
	if err != nil {
		return nil, nil, fmt.Errorf("failed to initialize database: %w", err)
	}

	if err := internal.Migrate("migrations", dbPath); err != nil {
		_ = db.Close()
		return nil, nil, fmt.Errorf("failed to migrate SQL: %w", err)
	}

	retailers, err := brands.GetRetailersMap()
	if err != nil {
		return nil, nil, fmt.Errorf("failed to load retailers: %w", err)
	}

	repo := internal.NewFuelPricesRepository(db, &retailers)
	metrics.RegisterFuelSnapshotCollector(prometheus.DefaultRegisterer, repo.SnapshotStats)
	metrics.RegisterFuelDistributionCollector(prometheus.DefaultRegisterer, repo.DistributionStats)

	return client, repo, nil
}
