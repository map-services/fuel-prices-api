package internal

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/rm-hull/fuel-prices-api/internal/metrics"
	"github.com/rm-hull/fuel-prices-api/internal/models"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestHTTPStatusError_Error(t *testing.T) {
	err := &HTTPStatusError{
		URL:        "http://example.com",
		Status:     "404 Not Found",
		StatusCode: 404,
		Body:       "page not found\nwith newline",
	}
	expected := "unexpected http response (404 Not Found) from http://example.com, body: page not found\\nwith newline"
	assert.Equal(t, expected, err.Error())

	errLong := &HTTPStatusError{
		URL:    "http://example.com",
		Status: "500 Internal Server Error",
		Body:   string(make([]byte, 2000)),
	}
	assert.Contains(t, errLong.Error(), "...(truncated)")

	var nilErr *HTTPStatusError
	assert.Equal(t, "http status error: <nil>", nilErr.Error())
}

func setupTestClient(t *testing.T, baseUrl string) *fuelPricesManager {
	return &fuelPricesManager{
		baseUrl: baseUrl,
		timeTracker: timeTracker{
			started: time.Now(),
		},
		refresh: "incremental",
		client:  &http.Client{},
		authReq: models.AuthRequest{
			ClientId:     "test-client-id",
			ClientSecret: "test-client-secret",
		},
		metrics: metrics.NewClientFetchMetrics(prometheus.NewRegistry()),
	}
}

func TestAuthenticate_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth/generate_access_token", r.URL.Path)
		assert.Equal(t, "POST", r.Method)

		var req models.AuthRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "test-client-id", req.ClientId)

		resp := models.AuthResponse{
			Success: true,
			Data: models.TokenData{
				AccessToken:  "test-access-token",
				ExpiresIn:    3600,
				RefreshToken: "test-refresh-token",
			},
		}
		w.WriteHeader(http.StatusOK)
		err = json.NewEncoder(w).Encode(resp)
		require.NoError(t, err)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	err := mgr.authenticate()

	require.NoError(t, err)
	assert.Equal(t, "test-access-token", mgr.tokenData.AccessToken)
	assert.Equal(t, "test-refresh-token", mgr.tokenData.RefreshToken)
	assert.False(t, mgr.timeTracker.lastAuth.IsZero())
}

func TestAuthenticate_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.AuthResponse{
			Success: false,
			Message: "invalid credentials",
		}
		w.WriteHeader(http.StatusOK)
		err := json.NewEncoder(w).Encode(resp)
		require.NoError(t, err)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	err := mgr.authenticate()

	require.Error(t, err)
	assert.Contains(t, err.Error(), "authentication failed: invalid credentials")
}

func TestAuthenticate_HttpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, err := w.Write([]byte("unauthorized"))
		require.NoError(t, err)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	err := mgr.authenticate()

	require.Error(t, err)
	var stErr *HTTPStatusError
	require.ErrorAs(t, err, &stErr)
	assert.Equal(t, http.StatusUnauthorized, stErr.StatusCode)
	assert.Equal(t, "unauthorized", stErr.Body)
}

func TestTokenRefresh_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/oauth/regenerate_access_token", r.URL.Path)
		var req models.TokenRefreshRequest
		err := json.NewDecoder(r.Body).Decode(&req)
		require.NoError(t, err)
		assert.Equal(t, "test-refresh-token", req.RefreshToken)

		resp := models.AuthResponse{
			Success: true,
			Data: models.TokenData{
				AccessToken: "new-access-token",
				ExpiresIn:   3600,
			},
		}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	mgr.tokenData.RefreshToken = "test-refresh-token"
	err := mgr.tokenRefresh()

	require.NoError(t, err)
	assert.Equal(t, "new-access-token", mgr.tokenData.AccessToken)
}

func TestTokenRefresh_Failure(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.AuthResponse{Success: false, Message: "failed"}
		w.WriteHeader(http.StatusOK)
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	err := mgr.tokenRefresh()
	require.Error(t, err)
}

func TestTokenRefresh_RetryOnServerError(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.URL.Path == "/oauth/regenerate_access_token" {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		if r.URL.Path == "/oauth/generate_access_token" {
			resp := models.AuthResponse{
				Success: true,
				Data:    models.TokenData{AccessToken: "recovered-token", ExpiresIn: 3600},
			}
			_ = json.NewEncoder(w).Encode(resp)
			return
		}
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	err := mgr.tokenRefresh()

	require.NoError(t, err)
	assert.Equal(t, "recovered-token", mgr.tokenData.AccessToken)
	assert.Equal(t, 2, calls)
}

func TestCheckTokenExpiry(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.AuthResponse{
			Success: true,
			Data:    models.TokenData{AccessToken: "refreshed", ExpiresIn: 3600},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)

	// Not expired
	mgr.timeTracker.lastAuth = time.Now()
	mgr.tokenData.ExpiresIn = 3600
	err := mgr.checkTokenExpiry()
	require.NoError(t, err)
	assert.Empty(t, mgr.tokenData.AccessToken) // No refresh happened

	// Expiring soon
	mgr.timeTracker.lastAuth = time.Now().Add(-56 * time.Minute)
	mgr.tokenData.ExpiresIn = 3600 // expires in 4 mins
	err = mgr.checkTokenExpiry()
	require.NoError(t, err)
	assert.Equal(t, "refreshed", mgr.tokenData.AccessToken)
}

func TestGetFuelPrices_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/pfs/fuel-prices", r.URL.Path)
		batch := r.URL.Query().Get("batch-number")

		if batch == "1" {
			resp := []models.ForecourtPrices{
				{NodeId: "1", TradingName: "Station 1"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	mgr.tokenData.ExpiresIn = 3600
	mgr.timeTracker.lastAuth = time.Now()

	count, dropped, err := mgr.GetFuelPrices(func(batch []models.ForecourtPrices) (int, int, error) {
		return len(batch), 0, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, 0, dropped)
}

func TestGetFillingStations_Success(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, "/pfs", r.URL.Path)
		batch := r.URL.Query().Get("batch-number")

		if batch == "1" {
			resp := []models.PetrolFillingStation{
				{NodeId: "1", BrandName: "Brand 1"},
			}
			_ = json.NewEncoder(w).Encode(resp)
		} else {
			w.WriteHeader(http.StatusNotFound)
		}
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	mgr.tokenData.ExpiresIn = 3600
	mgr.timeTracker.lastAuth = time.Now()

	count, dropped, err := mgr.GetFillingStations(func(batch []models.PetrolFillingStation) (int, int, error) {
		return len(batch), 0, nil
	})

	require.NoError(t, err)
	assert.Equal(t, 1, count)
	assert.Equal(t, 0, dropped)
}

func TestGetEffectiveStartTimestamp(t *testing.T) {
	mgr := &fuelPricesManager{refresh: "incremental"}
	lastFetch := time.Date(2023, 1, 1, 12, 0, 0, 0, time.UTC)

	ts := mgr.getEffectiveStartTimestamp("path", &lastFetch)
	assert.Equal(t, "2023-01-01 12:00:00", ts)

	mgr.refresh = "full"
	ts = mgr.getEffectiveStartTimestamp("path", &lastFetch)
	assert.Equal(t, "", ts)

	mgr.refresh = "incremental"
	ts = mgr.getEffectiveStartTimestamp("path", nil)
	assert.Equal(t, "", ts)
}

func TestLastUpdated(t *testing.T) {
	mgr := &fuelPricesManager{}
	assert.Nil(t, mgr.LastUpdated())

	now := time.Now()
	mgr.timeTracker.lastPricesFetch = now
	assert.Equal(t, &now, mgr.LastUpdated())
}

func TestFetchBatched_CallbackError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode([]models.ForecourtPrices{{NodeId: "1"}})
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	mgr.tokenData.ExpiresIn = 3600
	mgr.timeTracker.lastAuth = time.Now()

	_, _, err := mgr.GetFuelPrices(func(batch []models.ForecourtPrices) (int, int, error) {
		return 0, 0, fmt.Errorf("callback failed")
	})

	require.Error(t, err)
	assert.Contains(t, err.Error(), "callback error: callback failed")
}

func TestGetFuelPrices_InvalidUrl(t *testing.T) {
	mgr := setupTestClient(t, " http://invalid") // leading space makes it invalid
	_, _, err := mgr.GetFuelPrices(nil)
	require.Error(t, err)
}

func TestGetFuelPrices_DecodeError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, err := w.Write([]byte("invalid json"))
		require.NoError(t, err)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	mgr.tokenData.ExpiresIn = 3600
	mgr.timeTracker.lastAuth = time.Now()

	_, _, err := mgr.GetFuelPrices(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to unmarshal response")
}

func TestFetchBatched_TokenExpiryError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer server.Close()

	mgr := setupTestClient(t, server.URL)
	mgr.timeTracker.lastAuth = time.Now().Add(-1 * time.Hour)
	mgr.tokenData.ExpiresIn = 1800 // expired

	_, _, err := mgr.GetFuelPrices(nil)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to refresh token")
}

func TestPost_MarshalError(t *testing.T) {
	mgr := setupTestClient(t, "http://example.com")
	// can't marshal a channel
	_, err := mgr.post("http://example.com", "app/json", make(chan int))
	require.Error(t, err)
	assert.Contains(t, err.Error(), "failed to marshal request body")
}

func TestNewFuelPricesClient(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		resp := models.AuthResponse{
			Success: true,
			Data:    models.TokenData{AccessToken: "token"},
		}
		_ = json.NewEncoder(w).Encode(resp)
	}))
	defer server.Close()

	t.Setenv("FUEL_PRICES_API_BASE_URL", server.URL)

	client, err := NewFuelPricesClient("id", "secret", "incremental")
	require.NoError(t, err)
	assert.NotNil(t, client)
}
