package internal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"

	// neturl "net/url"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rm-hull/fuel-prices-api/internal/metrics"
	"github.com/rm-hull/fuel-prices-api/internal/models"
)

// HTTPStatusError is returned when the remote server responds with a non-2xx status.
type HTTPStatusError struct {
	URL        string
	Status     string
	StatusCode int
	Body       string
}

func (e *HTTPStatusError) Error() string {
	if e == nil {
		return "http status error: <nil>"
	}

	body := e.Body
	// sanitize newlines and tabs so logs remain single-line
	body = strings.ReplaceAll(body, "\n", "\\n")
	body = strings.ReplaceAll(body, "\r", "\\r")
	body = strings.ReplaceAll(body, "\t", "\\t")

	// truncate very large bodies to avoid excessive log sizes
	const maxBody = 1000
	if len(body) > maxBody {
		body = body[:maxBody] + "...(truncated)"
	}

	return fmt.Sprintf("unexpected http response (%s) from %s, body: %s", e.Status, e.URL, body)
}

type BatchCallback[T any] func([]T) (int, int, error)

type FuelPricesClient interface {
	ShouldRefresh() bool
	GetFuelPrices(BatchCallback[models.ForecourtPrices]) (int, int, error)
	GetFillingStations(BatchCallback[models.PetrolFillingStation]) (int, int, error)
	LastUpdated() *time.Time
}

type timeTracker struct {
	started         time.Time
	lastAuth        time.Time
	lastPfsFetch    time.Time
	lastPricesFetch time.Time
}

type fuelPricesManager struct {
	baseUrl     string
	authReq     models.AuthRequest
	tokenData   models.TokenData
	timeTracker timeTracker
	client      *http.Client
	metrics     *metrics.ClientFetchMetrics
	refresh     string
}

func NewFuelPricesClient(clientId, clientSecret string, refresh string) (FuelPricesClient, error) {
	baseUrl := "https://www.fuel-finder.service.gov.uk/api/v1"
	if envBaseUrl := os.Getenv("FUEL_PRICES_API_BASE_URL"); envBaseUrl != "" {
		baseUrl = envBaseUrl
	}

	mgr := &fuelPricesManager{
		baseUrl: baseUrl,
		timeTracker: timeTracker{
			started: time.Now(),
		},
		refresh: refresh,
		client:  &http.Client{},
		authReq: models.AuthRequest{
			ClientId:     clientId,
			ClientSecret: clientSecret,
		},
		metrics: metrics.NewClientFetchMetrics(prometheus.DefaultRegisterer),
	}

	err := mgr.authenticate()
	if err != nil {
		return nil, fmt.Errorf("failed to authenticate: %v", err)
	}

	return mgr, nil
}

func (mgr *fuelPricesManager) ShouldRefresh() bool {
	return mgr.refresh != "never"
}

func (mgr *fuelPricesManager) LastUpdated() *time.Time {
	if mgr.timeTracker.lastPricesFetch.IsZero() {
		return nil
	}
	return &mgr.timeTracker.lastPricesFetch
}

func (mgr *fuelPricesManager) GetFuelPrices(callback BatchCallback[models.ForecourtPrices]) (int, int, error) {
	decode := func(body io.ReadCloser, batchNo int) ([]models.ForecourtPrices, error) {
		var resp []models.ForecourtPrices
		decoder := json.NewDecoder(body)
		if err := decoder.Decode(&resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
		return resp, nil
	}

	return fetchBatched(mgr, "pfs/fuel-prices", &mgr.timeTracker.lastPricesFetch, decode, callback)
}

func (mgr *fuelPricesManager) GetFillingStations(callback BatchCallback[models.PetrolFillingStation]) (int, int, error) {
	decode := func(body io.ReadCloser, batchNo int) ([]models.PetrolFillingStation, error) {
		var resp []models.PetrolFillingStation
		decoder := json.NewDecoder(body)
		if err := decoder.Decode(&resp); err != nil {
			return nil, fmt.Errorf("failed to unmarshal response: %w", err)
		}
		return resp, nil
	}

	return fetchBatched(mgr, "pfs", &mgr.timeTracker.lastPfsFetch, decode, callback)
}

func (mgr *fuelPricesManager) authenticate() error {
	url := fmt.Sprintf("%s/oauth/generate_access_token", mgr.baseUrl)
	body, err := mgr.post(url, "application/json", mgr.authReq)
	if err != nil {
		return err
	}
	defer func() {
		if err := body.Close(); err != nil {
			log.Printf("failed to close body: %v", err)
		}
	}()

	var resp models.AuthResponse
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("authentication failed: %s", resp.Message)
	}

	mgr.tokenData = resp.Data
	mgr.timeTracker.lastAuth = time.Now()
	log.Printf("Authenticated successfully, token expires in %d seconds", resp.Data.ExpiresIn)

	return nil
}

func (mgr *fuelPricesManager) tokenRefresh() error {

	tokenReq := models.TokenRefreshRequest{
		ClientId:     mgr.authReq.ClientId,
		RefreshToken: mgr.tokenData.RefreshToken,
	}
	url := fmt.Sprintf("%s/oauth/regenerate_access_token", mgr.baseUrl)
	body, err := mgr.post(url, "application/json", tokenReq)
	if err != nil {
		var stErr *HTTPStatusError
		if errors.As(err, &stErr) && stErr.StatusCode >= http.StatusInternalServerError {
			log.Printf("Failed to refresh access token: %v", err)
			log.Printf("Trying to recover from token refresh error response (HTTP %d)...", stErr.StatusCode)
			return mgr.authenticate()
		}
		return err
	}
	defer func() {
		if err := body.Close(); err != nil {
			log.Printf("failed to close body: %v", err)
		}
	}()

	var resp models.AuthResponse
	decoder := json.NewDecoder(body)
	if err := decoder.Decode(&resp); err != nil {
		return fmt.Errorf("failed to unmarshal response: %w", err)
	}
	if !resp.Success {
		return fmt.Errorf("authentication failed: %s", resp.Message)
	}

	mgr.tokenData.AccessToken = resp.Data.AccessToken
	mgr.tokenData.ExpiresIn = resp.Data.ExpiresIn
	mgr.timeTracker.lastAuth = time.Now()
	log.Printf("Token refresh completed successfully, token expires in %d seconds", mgr.tokenData.ExpiresIn)

	return nil
}

func (mgr *fuelPricesManager) checkTokenExpiry() error {
	expiryTime := mgr.timeTracker.lastAuth.Add(time.Duration(mgr.tokenData.ExpiresIn) * time.Second)
	expiresSoon := time.Until(expiryTime) < 5*time.Minute

	if expiresSoon {
		log.Printf("Access token has either expired or is expiring soon, refreshing...")
		if err := mgr.tokenRefresh(); err != nil {
			return fmt.Errorf("failed to refresh token: %w", err)
		}
	}
	return nil
}

func (mgr *fuelPricesManager) getEffectiveStartTimestamp(path string, lastFetch *time.Time) string {

	if lastFetch == nil || lastFetch.IsZero() || mgr.refresh == "full" {
		return ""
	}

	log.Printf("Time since last fetch for %s: %s", path, time.Since(*lastFetch))
	return lastFetch.Format("2006-01-02 15:04:05") // Not quite RFC3339 ...
}

func fetchBatched[T any](
	mgr *fuelPricesManager,
	path string,
	lastFetch *time.Time,
	decode func(io.ReadCloser, int) ([]T, error),
	callback BatchCallback[T],
) (int, int, error) {
	if err := mgr.checkTokenExpiry(); err != nil {
		return 0, 0, err
	}

	batchNo := 1
	count := 0
	totalDropped := 0

	startTime := time.Now()
	effectiveStartTimestamp := mgr.getEffectiveStartTimestamp(path, lastFetch)

	for {
		params := neturl.Values{}
		params.Add("batch-number", strconv.Itoa(batchNo))
		if effectiveStartTimestamp != "" {
			params.Add("effective-start-timestamp", effectiveStartTimestamp)
		}
		url := fmt.Sprintf("%s/%s?%s", mgr.baseUrl, path, params.Encode())
		body, err := mgr.get(url)
		if err != nil {
			var stErr *HTTPStatusError
			if errors.As(err, &stErr) && stErr.StatusCode == http.StatusNotFound {
				log.Printf("No more batches available for %s, stopping at batch %d", path, batchNo-1)
				break
			}
			return 0, 0, err
		}

		data, err := decode(body, batchNo)
		if err != nil {
			_ = body.Close()
			return 0, 0, err
		}
		_ = body.Close()

		numRecords, dropped, err := callback(data)
		if err != nil {
			return 0, 0, fmt.Errorf("callback error: %w", err)
		}
		mgr.metrics.RecordFetchedItems(path, numRecords, dropped)
		count += numRecords
		totalDropped += dropped
		batchNo++

		if numRecords == 0 {
			break
		}
	}

	*lastFetch = startTime
	return count, totalDropped, nil
}

func (mgr *fuelPricesManager) get(url string) (io.ReadCloser, error) {
	start := time.Now()
	log.Printf("GET %s", url)
	req, err := http.NewRequest("GET", url, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+mgr.tokenData.AccessToken)
	req.Header.Set("Accept", "application/json")

	resp, err := mgr.client.Do(req)
	mgr.metrics.RecordHttpCall(start, "GET", url, resp, err)

	if err != nil {
		return nil, fmt.Errorf("failed to fetch from %s: %w", url, err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			bodyBytes = fmt.Appendf(nil, "<failed to read body: %v>", readErr)
		}
		_ = resp.Body.Close()

		return nil, &HTTPStatusError{
			URL:        url,
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
		}
	}
	return resp.Body, nil
}

func (mgr *fuelPricesManager) post(url, contentType string, data any) (io.ReadCloser, error) {
	start := time.Now()
	log.Printf("POST %s", url)
	jsonData, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %w", err)
	}

	req, err := http.NewRequest("POST", url, bytes.NewBuffer(jsonData))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %w", err)
	}
	req.Header.Set("Content-Type", contentType)
	req.Header.Set("Accept", "application/json")

	resp, err := mgr.client.Do(req)
	mgr.metrics.RecordHttpCall(start, "POST", url, resp, err)

	if err != nil {
		return nil, fmt.Errorf("failed to perform request: %w", err)
	}

	if resp.StatusCode >= http.StatusBadRequest {
		bodyBytes, readErr := io.ReadAll(resp.Body)
		if readErr != nil {
			bodyBytes = fmt.Appendf(nil, "<failed to read body: %v>", readErr)
		}
		_ = resp.Body.Close()

		return nil, &HTTPStatusError{
			URL:        url,
			Status:     resp.Status,
			StatusCode: resp.StatusCode,
			Body:       string(bodyBytes),
		}
	}

	return resp.Body, nil
}
