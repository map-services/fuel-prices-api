package cmd

import (
	"encoding/csv"
	"log/slog"
	"os"

	"github.com/cockroachdb/errors"
	"github.com/map-services/fuel-prices-api/internal/brands"
	"github.com/map-services/fuel-prices-api/internal/favicon"
	"github.com/map-services/fuel-prices-api/internal/models"
)

func UpdateFaviconsInCSV(csvFile string) error {

	retailers, err := brands.GetRetailersList()
	if err != nil {
		return err
	}

	updated := make([]*models.Retailer, 0, len(retailers))
	for idx, record := range retailers {

		slog.Info("Processing record", "index", idx, "url", record.WebsiteUrl)

		iconInfo, err := favicon.Extract(record.WebsiteUrl)
		if err != nil {
			slog.Error("failed to extract favicon", "url", record.WebsiteUrl, "error", err)
		} else {
			record.LogoUrl = &iconInfo.Href
		}
		updated = append(updated, record)
	}

	f, err := os.OpenFile(csvFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0644)
	if err != nil {
		return errors.Wrapf(err, "failed to open file %s", csvFile)
	}
	defer func() {
		if err := f.Close(); err != nil {
			slog.Error("error closing file", "error", err)
		}
	}()

	csvWriter := csv.NewWriter(f)
	defer csvWriter.Flush()

	for _, record := range updated {
		row := record.ToCSV()
		if err := csvWriter.Write(row); err != nil {
			return errors.Wrapf(err, "failed to write row=%v", row)
		}
	}
	return nil
}
