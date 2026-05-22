package models

import (
	"fmt"
	"log/slog"
	"os"
	"strings"
	"time"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

type Location struct {
	AddressLine1 string  `json:"address_line_1"`
	AddressLine2 string  `json:"address_line_2,omitempty"`
	City         string  `json:"city"`
	Country      string  `json:"country"`
	County       string  `json:"county,omitempty"`
	Postcode     string  `json:"postcode"`
	Latitude     float64 `json:"latitude"`
	Longitude    float64 `json:"longitude"`
}

type DailyOpeningTimes struct {
	Open      string `json:"open"`
	Close     string `json:"close"`
	Is24Hours bool   `json:"is_24_hours"`
}

type PetrolFillingStation struct {
	NodeId                      string     `json:"node_id"`
	MftOrganisationName         string     `json:"mft_organisation_name"`
	PublicPhoneNumber           string     `json:"public_phone_number"`
	TradingName                 string     `json:"trading_name"`
	IsSameTradingAndBrandName   bool       `json:"is_same_trading_and_brand_name"`
	BrandName                   string     `json:"brand_name"`
	TemporaryClosure            bool       `json:"temporary_closure"`
	PermanentClosure            bool       `json:"permanent_closure"`
	PermanentClosureDate        *time.Time `json:"permanent_closure_date,omitempty"`
	IsMotorwayServiceStation    bool       `json:"is_motorway_service_station"`
	IsSupermarketServiceStation bool       `json:"is_supermarket_service_station"`
	Location                    Location   `json:"location"`
	Amenities                   []string   `json:"amenities"`
	OpeningTimes                struct {
		UsualDays   map[string]DailyOpeningTimes `json:"usual_days"`
		BankHoliday struct {
			Type      string `json:"type"`
			OpenTime  string `json:"open_time"`
			CloseTime string `json:"close_time"`
			Is24Hours bool   `json:"is_24_hours"`
		} `json:"bank_holiday"`
	} `json:"opening_times"`
	FuelTypes []string `json:"fuel_types"`
}

type FuelPrice struct {
	FuelType                      string     `json:"fuel_type"`
	Price                         float64    `json:"price"`
	PriceLastUpdated              time.Time  `json:"price_last_updated"`
	PriceChangeEffectiveTimestamp *time.Time `json:"price_change_effective_timestamp,omitempty"`
}

type ForecourtPrices struct {
	NodeId              string      `json:"node_id"`
	MftOrganisationName string      `json:"mft_organisation_name"`
	PublicPhoneNumber   string      `json:"public_phone_number"`
	TradingName         string      `json:"trading_name"`
	FuelPrices          []FuelPrice `json:"fuel_prices"`
}

type MetaData struct {
	BatchNumber  int  `json:"batch_number"`
	BatchSize    int  `json:"batch_size"`
	TotalBatches int  `json:"total_batches"`
	Cached       bool `json:"cached"`
}

func (pfs *PetrolFillingStation) ToTuple() []any {

	return []any{
		pfs.NodeId,
		pfs.MftOrganisationName,
		pfs.PublicPhoneNumber,
		strings.TrimSpace(pfs.TradingName),
		pfs.IsSameTradingAndBrandName,
		strings.TrimSpace(pfs.BrandName),
		pfs.TemporaryClosure,
		pfs.PermanentClosure,
		pfs.PermanentClosureDate,
		pfs.IsMotorwayServiceStation,
		pfs.IsSupermarketServiceStation,
		cleanseAddressLine1(pfs.Location.AddressLine1, pfs.Location.City, pfs.Location.Postcode),
		pfs.Location.AddressLine2,
		pfs.Location.City,
		pfs.Location.Country,
		pfs.Location.County,
		strings.ToUpper(pfs.Location.Postcode),
		pfs.Location.Latitude,
		pfs.Location.Longitude,
		toJSON(pfs.OpeningTimes),
		toJSON(pfs.Amenities),
		toJSON(pfs.FuelTypes),
	}
}

func (fp *FuelPrice) ToTuple(nodeId string) []any {

	price, logMsg := cleansePrice(fp.Price)
	if logMsg != "" {
		slog.Warn(logMsg)
	}

	return []any{
		nodeId,
		fp.FuelType,
		fp.PriceLastUpdated,
		price,
		fp.PriceChangeEffectiveTimestamp,
	}
}

func (fp *FuelPrice) IsPriceOutOfBounds() bool {
	price, _ := cleansePrice(fp.Price)
	return price < 100 || price > 300
}

func toJSON(v any) string {
	jsonBytes, err := json.Marshal(v)
	if err != nil {
		slog.Error("error marshaling to JSON", "error", err)
		os.Exit(1)
	}
	return string(jsonBytes)
}

func cleanseAddressLine1(addressLine1, city, postcode string) string {
	// The API sometime returns records with the address line 1 containing
	// the full address, so we just tidy that before writing to the database.

	suffixesToRemove := []string{
		", " + city + ", " + postcode,
		city + ", " + postcode,
		", " + postcode,
		postcode,
	}

	for _, suffix := range suffixesToRemove {
		addressLine1 = strings.TrimSuffix(addressLine1, suffix)
	}
	return addressLine1
}

func cleansePrice(price float64) (float64, string) {
	// The API sometimes returns prices in pounds (rather than pence) or in tenths of pence
	// (rather than pence), so we just adjust accordingly before writing to the database.
	if price < 10 {
		return price * 100, fmt.Sprintf("WARNING: correcting price %0.3f -> %0.1f", price, price*100)
	} else if price > 1000 {
		return price / 10, fmt.Sprintf("WARNING: correcting price %0.1f -> %0.1f", price, price/10)
	}
	return price, ""
}
