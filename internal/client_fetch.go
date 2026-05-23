package internal

import (
	"bytes"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	neturl "net/url"
	"os"
	"strconv"
	"strings"

	// neturl "net/url"
	"time"

	"github.com/map-services/fuel-prices-api/internal/metrics"
	"github.com/map-services/fuel-prices-api/internal/models"
	"github.com/prometheus/client_golang/prometheus"
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
	started            time.Time
	accessTokenExpiry  time.Time
	refreshTokenExpiry time.Time
	lastPfsFetch       time.Time
	lastPricesFetch    time.Time
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
			slog.Error("failed to close body", "error", err)
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
	now := time.Now()
	expiry := time.Duration(resp.Data.ExpiresIn) * time.Second
	mgr.timeTracker.accessTokenExpiry = now.Add(expiry)
	if resp.Data.RefreshTokenExpiresIn > 0 {
		mgr.timeTracker.refreshTokenExpiry = now.Add(time.Duration(resp.Data.RefreshTokenExpiresIn) * time.Second)
	} else {
		mgr.timeTracker.refreshTokenExpiry = time.Time{}
	}
	slog.Info("Authenticated successfully",
		"expiresIn", humanDuration(expiry),
		"accessTokenExpiry", mgr.timeTracker.accessTokenExpiry.Format(time.RFC3339))

	if !mgr.timeTracker.refreshTokenExpiry.IsZero() {
		slog.Info("Refresh token expires",
			"expiresIn", humanDuration(time.Duration(resp.Data.RefreshTokenExpiresIn)*time.Second),
			"refreshTokenExpiry", mgr.timeTracker.refreshTokenExpiry.Format(time.RFC3339))
	}
	return nil
}

func (mgr *fuelPricesManager) tokenRefresh() error {
	if expiresSoon(mgr.timeTracker.refreshTokenExpiry) {
		slog.Warn("Refresh token has either expired or is expiring soon, re-authenticating...")
		return mgr.authenticate()
	}

	tokenReq := models.TokenRefreshRequest{
		ClientId:     mgr.authReq.ClientId,
		RefreshToken: mgr.tokenData.RefreshToken,
	}
	url := fmt.Sprintf("%s/oauth/regenerate_access_token", mgr.baseUrl)
	body, err := mgr.post(url, "application/json", tokenReq)
	if err != nil {
		var stErr *HTTPStatusError
		if errors.As(err, &stErr) {
			slog.Error("Failed to refresh access token", "error", err)
			slog.Warn("Trying to recover from token refresh error response", "statusCode", stErr.StatusCode)
			return mgr.authenticate()
		}
		return err
	}
	defer func() {
		if err := body.Close(); err != nil {
			slog.Error("failed to close body", "error", err)
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

	now := time.Now()
	expiry := time.Duration(resp.Data.ExpiresIn) * time.Second
	mgr.tokenData.AccessToken = resp.Data.AccessToken
	mgr.tokenData.ExpiresIn = resp.Data.ExpiresIn
	mgr.timeTracker.accessTokenExpiry = now.Add(expiry)
	if resp.Data.RefreshToken != "" {
		mgr.tokenData.RefreshToken = resp.Data.RefreshToken
		mgr.tokenData.RefreshTokenExpiresIn = resp.Data.RefreshTokenExpiresIn
		if resp.Data.RefreshTokenExpiresIn > 0 {
			mgr.timeTracker.refreshTokenExpiry = now.Add(time.Duration(resp.Data.RefreshTokenExpiresIn) * time.Second)
		} else {
			mgr.timeTracker.refreshTokenExpiry = time.Time{}
		}
	}

	slog.Info("Token refresh completed successfully",
		"expiresIn", humanDuration(expiry),
		"accessTokenExpiry", mgr.timeTracker.accessTokenExpiry.Format(time.RFC3339))

	return nil
}

func (mgr *fuelPricesManager) checkTokenExpiry() error {
	if expiresSoon(mgr.timeTracker.accessTokenExpiry) {
		slog.Warn("Access token has either expired or is expiring soon, refreshing...")
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

	slog.Info("Time since last fetch",
		"path", path,
		"elapsed", humanDuration(time.Since(*lastFetch)),
		"lastFetch", lastFetch.Format(time.RFC3339))

	return lastFetch.Format("2006-01-02 15:04:05") // Not quite RFC3339 ...
}

func fetchBatched[T any](
	mgr *fuelPricesManager,
	path string,
	lastFetch *time.Time,
	decode func(io.ReadCloser, int) ([]T, error),
	callback BatchCallback[T],
) (int, int, error) {
	batchNo := 1
	count := 0
	totalDropped := 0

	startTime := time.Now()
	effectiveStartTimestamp := mgr.getEffectiveStartTimestamp(path, lastFetch)

	for {
		if err := mgr.checkTokenExpiry(); err != nil {
			return 0, 0, err
		}

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
				slog.Info("No more batches available", "path", path, "lastBatch", batchNo-1)
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
	slog.Info("GET", "url", url)
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
	slog.Info("POST", "url", url)
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

func expiresSoon(t time.Time) bool {
	return !t.IsZero() && time.Until(t) < 5*time.Minute
}

func humanDuration(d time.Duration) string {
	switch {
	case d < time.Second:
		return fmt.Sprintf("%dms", d.Milliseconds())
	case d < time.Minute:
		return fmt.Sprintf("%.1fs", d.Seconds())
	case d < time.Hour:
		mins, secs := int(d.Minutes()), int(d.Seconds())%60
		if secs == 0 {
			return fmt.Sprintf("%dm", mins)
		}
		return fmt.Sprintf("%dm%ds", mins, secs)
	case d < 24*time.Hour:
		hours, mins := int(d.Hours()), int(d.Minutes())%60
		if mins == 0 {
			return fmt.Sprintf("%dh", hours)
		}
		return fmt.Sprintf("%dh%dm", hours, mins)
	default:
		days, hours := int(d.Hours())/24, int(d.Hours())%24
		if hours == 0 {
			return fmt.Sprintf("%dd", days)
		}
		return fmt.Sprintf("%dd%dh", days, hours)
	}
}
