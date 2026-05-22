package middleware

import (
	"log/slog"
	"slices"
	"time"

	"github.com/gin-gonic/gin"
)

// RequestLogger is a middleware that logs details about every incoming request.
func RequestLogger(logger *slog.Logger, excludedPaths ...string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if slices.Contains(excludedPaths, c.Request.URL.Path) {
			c.Next()
			return
		}

		start := time.Now()
		c.Next()
		latency := time.Since(start)

		logger.Info("request",
			"method", c.Request.Method,
			"path", c.Request.RequestURI,
			"status", c.Writer.Status(),
			"latency_ms", float64(latency.Microseconds()) / 1000.0,
			"ip", c.ClientIP(),
			"user_agent", c.Request.UserAgent(),
			"body_size", c.Writer.Size(),
		)
	}
}
