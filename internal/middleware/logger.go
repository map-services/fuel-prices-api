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
		start := time.Now()
		path := c.Request.URL.Path
		raw := c.Request.URL.RawQuery

		c.Next()

		if slices.Contains(excludedPaths, c.Request.URL.Path) {
			return
		}

		if raw != "" {
			path = path + "?" + raw
		}

		end := time.Now()
		latency := end.Sub(start)

		logger.Info("request",
			"method", c.Request.Method,
			"path", path,
			"status", c.Writer.Status(),
			"latency_ms", latency.Milliseconds(),
			"ip", c.ClientIP(),
			"user_agent", c.Request.UserAgent(),
			"body_size", c.Writer.Size(),
		)
	}
}
