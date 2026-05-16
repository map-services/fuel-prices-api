package brands

import (
	_ "embed"
	"strings"

	"github.com/cockroachdb/errors"
	"github.com/map-services/fuel-prices-api/internal"
	"github.com/map-services/fuel-prices-api/internal/models"
)

//go:embed retailers.csv
var retailersCSV string

func GetRetailersList() ([]*models.Retailer, error) {
	arr := make([]*models.Retailer, 0, 100)
	reader := strings.NewReader(retailersCSV)

	for record := range internal.ParseCSV(reader, false, models.FromCSV) {
		if record.Error != nil {
			return nil, errors.Wrap(record.Error, "failed to load promoter organisations")
		}
		arr = append(arr, record.Value)
	}

	return arr, nil
}

func GetRetailersMap() (models.Retailers, error) {
	retailers, err := GetRetailersList()
	if err != nil {
		return nil, err
	}

	m := make(map[string]*models.Retailer, len(retailers))
	for _, record := range retailers {
		if _, ok := m[record.Name]; ok {
			return nil, errors.Newf("duplicate key detected: %s", record.Name)
		}
		m[record.Name] = record
	}

	return m, nil
}
