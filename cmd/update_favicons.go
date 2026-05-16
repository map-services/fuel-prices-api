package cmd

import (
	"encoding/csv"
	"log"
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

		log.Printf("Processing record %d: %s", idx, record.WebsiteUrl)

		iconInfo, err := favicon.Extract(record.WebsiteUrl)
		if err != nil {
			log.Printf("failed to extract favicon for %s: %v", record.WebsiteUrl, err)
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
			log.Printf("error closing file: %v", err)
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
