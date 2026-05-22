package internal

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"sort"
	"sync"
	"time"

	"github.com/kofalt/go-memoize"
	"github.com/map-services/fuel-prices-api/internal/metrics"
	"github.com/map-services/fuel-prices-api/internal/models"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/tavsec/gin-healthcheck/checks"
)

//go:embed sql/insert_pfs.sql
var insertPfsSQL string

//go:embed sql/insert_prices.sql
var insertPricesSQL string

//go:embed sql/search_pfs.sql
var searchPfsSQL string

//go:embed sql/search_prices.sql
var searchPricesSQL string

//go:embed sql/snapshot_stats.sql
var snapshotStatsSQL string

//go:embed sql/distribution_stats.sql
var distributionStatsSQL string

//go:embed sql/price_history.sql
var priceHistorySQL string

//go:embed sql/fuel_types.sql
var fuelTypesSQL string

type FuelPricesRepository interface {
	InsertPFS(batch []models.PetrolFillingStation) (int, int, error)
	InsertPrices(batch []models.ForecourtPrices) (int, int, error)
	Search(boundingBox []float64, perTypeLimit int) ([]models.SearchResult, error)
	PriceHistory(nodeId, fuelType string) ([]models.FuelPrice, error)
	FuelTypes() (map[string]struct{}, error)
	SnapshotStats() (*models.SnapshotStatistics, error)
	DistributionStats() (*models.DistributionStatistics, error)
	Close() error
	Check() checks.Check
}

type sqliteRepository struct {
	db        *sql.DB
	retailers *models.Retailers
	cache     *memoize.Memoizer
	metrics   *metrics.SqlMetrics
}

func NewFuelPricesRepository(db *sql.DB, retailers *models.Retailers) FuelPricesRepository {
	return &sqliteRepository{
		db:        db,
		retailers: retailers,
		cache:     memoize.NewMemoizer(60*time.Minute, 10*time.Minute),
		metrics:   metrics.NewSqlMetrics(prometheus.DefaultRegisterer),
	}
}

func (repo *sqliteRepository) Close() error {
	return repo.db.Close()
}

func (repo *sqliteRepository) Check() checks.Check {
	return checks.SqlCheck{Sql: repo.db}
}

func (repo *sqliteRepository) InsertPFS(batch []models.PetrolFillingStation) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}

	defer repo.metrics.Record(time.Now(), "insertPFS")
	tx, err := repo.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("error rolling back transaction", "error", rbErr)
			}
		}
	}()

	stmt, err := tx.Prepare(insertPfsSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			slog.Error("failed to close statement", "error", err)
		}
	}()

	count := 0
	for _, pfs := range batch {
		_, err = stmt.Exec(pfs.ToTuple()...)
		if err != nil {
			return 0, 0, fmt.Errorf("failed to execute individual insert: %w", err)
		}
		count++
	}

	if err = tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return count, 0, nil
}

func (repo *sqliteRepository) InsertPrices(batch []models.ForecourtPrices) (int, int, error) {
	if len(batch) == 0 {
		return 0, 0, nil
	}

	defer repo.metrics.Record(time.Now(), "insertPrices")
	tx, err := repo.db.Begin()
	if err != nil {
		return 0, 0, fmt.Errorf("failed to begin transaction: %w", err)
	}
	defer func() {
		if err != nil {
			if rbErr := tx.Rollback(); rbErr != nil {
				slog.Error("error rolling back transaction", "error", rbErr)
			}
		}
	}()

	stmt, err := tx.Prepare(insertPricesSQL)
	if err != nil {
		return 0, 0, fmt.Errorf("failed to prepare statement: %w", err)
	}
	defer func() {
		if err := stmt.Close(); err != nil {
			slog.Error("failed to close statement", "error", err)
		}
	}()

	count := 0
	dropped := 0
	for _, forecourtPrices := range batch {
		for _, fuelPrice := range forecourtPrices.FuelPrices {
			if fuelPrice.IsPriceOutOfBounds() {
				slog.Warn("price looks like an input-entry error; dropping fuel_price record", "fuelType", fuelPrice.FuelType, "price", fuelPrice.Price, "nodeId", forecourtPrices.NodeId)
				dropped++
				continue
			}
			_, err = stmt.Exec(fuelPrice.ToTuple(forecourtPrices.NodeId)...)
			if err != nil {
				return 0, 0, fmt.Errorf("failed to execute individual insert: %w", err)
			}
			count++
		}
	}

	if err = tx.Commit(); err != nil {
		return 0, 0, fmt.Errorf("failed to commit transaction: %w", err)
	}

	return count, dropped, nil
}

func (repo *sqliteRepository) Search(boundingBox []float64, perTypeLimit int) ([]models.SearchResult, error) {
	var (
		pfs       []models.SearchResult
		prices    map[string]map[string][]models.PriceInfo
		pfsErr    error
		pricesErr error
		wg        sync.WaitGroup
	)

	wg.Add(2)
	go repo.fetchPfs(boundingBox, &pfs, &pfsErr, wg.Done)
	go repo.fetchPrices(boundingBox, &prices, &pricesErr, perTypeLimit, wg.Done)

	wg.Wait()

	if pfsErr != nil {
		return nil, pfsErr
	}
	if pricesErr != nil {
		return nil, pricesErr
	}

	for i := range pfs {
		nodeId := pfs[i].NodeId
		if fuelPrices, ok := prices[nodeId]; ok {
			pfs[i].FuelPrices = fuelPrices
		}
	}

	return pfs, nil
}

func (repo *sqliteRepository) fetchPfs(boundingBox []float64, results *[]models.SearchResult, err *error, done func()) {
	defer done()

	defer repo.metrics.Record(time.Now(), "fetchPFS")
	rows, queryErr := repo.db.Query(searchPfsSQL, boundingBox[1], boundingBox[3], boundingBox[0], boundingBox[2])
	if queryErr != nil {
		*err = fmt.Errorf("failed to execute search query: %w", queryErr)
		return
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Error("failed to close rows", "error", closeErr)
		}
	}()

	for rows.Next() {
		var result models.SearchResult
		var openingTimesJSON, amenitiesJSON, fuelTypesJSON string
		if scanErr := rows.Scan(
			&result.NodeId, &result.MftOrganisationName, &result.PublicPhoneNumber, &result.TradingName,
			&result.IsSameTradingAndBrandName, &result.BrandName, &result.TemporaryClosure,
			&result.PermanentClosure, &result.PermanentClosureDate, &result.IsMotorwayServiceStation,
			&result.IsSupermarketServiceStation,
			&result.Location.AddressLine1, &result.Location.AddressLine2, &result.Location.City, &result.Location.Country,
			&result.Location.County, &result.Location.Postcode, &result.Location.Latitude, &result.Location.Longitude,
			&openingTimesJSON, &amenitiesJSON, &fuelTypesJSON,
		); scanErr != nil {
			*err = fmt.Errorf("failed to scan row: %w", scanErr)
			return
		}
		if unmarshalErr := json.Unmarshal([]byte(openingTimesJSON), &result.OpeningTimes); unmarshalErr != nil {
			*err = fmt.Errorf("failed to unmarshal opening times: %w", unmarshalErr)
			return
		}
		if unmarshalErr := json.Unmarshal([]byte(amenitiesJSON), &result.Amenities); unmarshalErr != nil {
			*err = fmt.Errorf("failed to unmarshal amenities: %w", unmarshalErr)
			return
		}
		if unmarshalErr := json.Unmarshal([]byte(fuelTypesJSON), &result.FuelTypes); unmarshalErr != nil {
			*err = fmt.Errorf("failed to unmarshal fuel types: %w", unmarshalErr)
			return
		}

		result.Retailer = repo.retailers.MatchBrandName(result.BrandName)
		*results = append(*results, result)
	}

	if rowsErr := rows.Err(); rowsErr != nil {
		*err = fmt.Errorf("error iterating over rows: %w", rowsErr)
	}
}

func (repo *sqliteRepository) fetchPrices(boundingBox []float64, results *map[string]map[string][]models.PriceInfo, err *error, perTypeLimit int, done func()) {
	defer done()

	defer repo.metrics.Record(time.Now(), "fetchPrices")
	rows, queryErr := repo.db.Query(searchPricesSQL, boundingBox[1], boundingBox[3], boundingBox[0], boundingBox[2], perTypeLimit)
	if queryErr != nil {
		*err = fmt.Errorf("failed to execute search query: %w", queryErr)
		return
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Error("failed to close rows", "error", closeErr)
		}
	}()

	*results = make(map[string]map[string][]models.PriceInfo)

	for rows.Next() {
		var nodeId string
		var fuelPrice models.FuelPrice
		if scanErr := rows.Scan(
			&nodeId, &fuelPrice.FuelType, &fuelPrice.PriceLastUpdated,
			&fuelPrice.Price, &fuelPrice.PriceChangeEffectiveTimestamp,
		); scanErr != nil {
			*err = fmt.Errorf("failed to scan row: %w", scanErr)
			return
		}
		if _, exists := (*results)[nodeId]; !exists {
			(*results)[nodeId] = make(map[string][]models.PriceInfo)
		}

		(*results)[nodeId][fuelPrice.FuelType] = append((*results)[nodeId][fuelPrice.FuelType], models.PriceInfo{
			Price:         fuelPrice.Price,
			UpdatedOn:     fuelPrice.PriceLastUpdated,
			EffectiveFrom: fuelPrice.PriceChangeEffectiveTimestamp,
		})
	}
}

func (repo *sqliteRepository) SnapshotStats() (*models.SnapshotStatistics, error) {
	result, err, _ := memoize.Call(repo.cache, "snapshot_stats", repo.snapshotQuery)
	return result, err
}

func (repo *sqliteRepository) DistributionStats() (*models.DistributionStatistics, error) {
	result, err, _ := memoize.Call(repo.cache, "distribution_stats", repo.distributionQuery)
	return result, err
}

func (repo *sqliteRepository) snapshotQuery() (*models.SnapshotStatistics, error) {
	now := time.Now()

	defer repo.metrics.Record(now, "statistics")
	snapshotRows, err := repo.db.Query(snapshotStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to execute snapshot stats: %w", err)
	}
	defer func() {
		if closeErr := snapshotRows.Close(); closeErr != nil {
			slog.Error("failed to close snapshot rows", "error", closeErr)
		}
	}()

	snapshotResults := make([]models.Snapshot, 0, 50)
	for snapshotRows.Next() {
		var snapshot models.Snapshot
		if err := snapshotRows.Scan(
			&snapshot.Scope, &snapshot.PostcodeArea, &snapshot.FuelType,
			&snapshot.LowestPrice, &snapshot.AveragePrice, &snapshot.HighestPrice,
			&snapshot.StandardDeviation, &snapshot.SampleSize,
		); err != nil {
			return nil, fmt.Errorf("failed to scan snapshot row: %w", err)
		}

		snapshotResults = append(snapshotResults, snapshot)
	}

	return &models.SnapshotStatistics{
		Snapshot:    snapshotResults,
		LastUpdated: &now,
	}, nil
}

func (repo *sqliteRepository) distributionQuery() (*models.DistributionStatistics, error) {
	now := time.Now()

	defer repo.metrics.Record(now, "distribution")
	distributionRows, err := repo.db.Query(distributionStatsSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to execute distribution stats: %w", err)
	}
	defer func() {
		if closeErr := distributionRows.Close(); closeErr != nil {
			slog.Error("failed to close distribution rows", "error", closeErr)
		}
	}()

	type distKey struct {
		scope        string
		postcodeArea string
		fuelType     string
	}

	distMap := make(map[distKey]*models.Distribution)
	for distributionRows.Next() {
		var scope, fuelType string
		var postcodeArea sql.NullString
		var priceBucket, sampleSize int

		if err := distributionRows.Scan(
			&scope, &postcodeArea, &fuelType, &priceBucket, &sampleSize,
		); err != nil {
			return nil, fmt.Errorf("failed to scan distribution row: %w", err)
		}

		key := distKey{scope: scope, postcodeArea: postcodeArea.String, fuelType: fuelType}
		if _, ok := distMap[key]; !ok {
			var pcArea *string
			if postcodeArea.Valid {
				s := postcodeArea.String
				pcArea = &s
			}
			distMap[key] = &models.Distribution{
				Scope:        scope,
				PostcodeArea: pcArea,
				FuelType:     fuelType,
				Buckets:      make(map[int]int),
			}
		}
		distMap[key].Buckets[priceBucket] = sampleSize
	}

	keys := make([]distKey, 0, len(distMap))
	for k := range distMap {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool {
		if keys[i].scope != keys[j].scope {
			return keys[i].scope < keys[j].scope
		}
		if keys[i].postcodeArea != keys[j].postcodeArea {
			return keys[i].postcodeArea < keys[j].postcodeArea
		}
		return keys[i].fuelType < keys[j].fuelType
	})

	distributionResults := make([]models.Distribution, 0, len(distMap))
	for _, k := range keys {
		distributionResults = append(distributionResults, *distMap[k])
	}

	return &models.DistributionStatistics{
		Distribution: distributionResults,
		LastUpdated:  &now,
	}, nil
}

func (repo *sqliteRepository) PriceHistory(nodeId, fuelType string) ([]models.FuelPrice, error) {

	defer repo.metrics.Record(time.Now(), "priceHistory")
	rows, err := repo.db.Query(priceHistorySQL, nodeId, fuelType)
	if err != nil {
		return nil, fmt.Errorf("failed to execute price history query: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Error("failed to close rows", "error", closeErr)
		}
	}()

	results := make([]models.FuelPrice, 0, 30)
	for rows.Next() {
		var result models.FuelPrice
		if err := rows.Scan(&result.FuelType, &result.Price, &result.PriceLastUpdated, &result.PriceChangeEffectiveTimestamp); err != nil {
			return nil, fmt.Errorf("failed to scan row: %w", err)
		}
		results = append(results, result)
	}

	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("error iterating over rows: %w", err)
	}

	return results, nil
}

func (repo *sqliteRepository) FuelTypes() (map[string]struct{}, error) {
	result, err, _ := memoize.Call(repo.cache, "fuel_types", repo.fuelTypesQuery)
	return result, err
}

func (repo *sqliteRepository) fuelTypesQuery() (map[string]struct{}, error) {
	now := time.Now()

	defer repo.metrics.Record(now, "fuelTypes")
	rows, err := repo.db.Query(fuelTypesSQL)
	if err != nil {
		return nil, fmt.Errorf("failed to execute fuel-types query: %w", err)
	}
	defer func() {
		if closeErr := rows.Close(); closeErr != nil {
			slog.Error("failed to close snapshot rows", "error", closeErr)
		}
	}()

	results := make(map[string]struct{}, 0)
	for rows.Next() {
		var fuelType string
		if err := rows.Scan(&fuelType); err != nil {
			return nil, fmt.Errorf("failed to scan snapshot row: %w", err)
		}

		results[fuelType] = struct{}{}
	}

	return results, nil
}
