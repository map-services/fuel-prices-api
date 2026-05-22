package middleware

import (
	"bytes"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/stretchr/testify/assert"
)

func TestRequestLogger(t *testing.T) {
	gin.SetMode(gin.TestMode)

	t.Run("Logs standard paths", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(buf, nil))

		r := gin.New()
		r.Use(RequestLogger(logger, "/healthz"))
		r.GET("/test", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req, _ := http.NewRequest("GET", "/test", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Contains(t, buf.String(), "method=GET")
		assert.Contains(t, buf.String(), "path=/test")
		assert.Contains(t, buf.String(), "latency_ms=")
	})

	t.Run("Excludes paths", func(t *testing.T) {
		buf := &bytes.Buffer{}
		logger := slog.New(slog.NewTextHandler(buf, nil))

		r := gin.New()
		r.Use(RequestLogger(logger, "/healthz"))
		r.GET("/healthz", func(c *gin.Context) {
			c.Status(http.StatusOK)
		})

		req, _ := http.NewRequest("GET", "/healthz", nil)
		w := httptest.NewRecorder()
		r.ServeHTTP(w, req)

		assert.Equal(t, http.StatusOK, w.Code)
		assert.Empty(t, buf.String(), "Log output should be empty for excluded path")
	})
}
