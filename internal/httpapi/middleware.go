package httpapi

import (
	"strings"

	"github.com/gin-gonic/gin"
)

const (
	// HeaderAPIKey is the header name for API key authentication.
	HeaderAPIKey = "X-API-Key"
	// HeaderInternalAPIKey is the header name for internal API key authentication.
	HeaderInternalAPIKey = "X-Internal-API-Key"
)

// APIKeyAuth returns a middleware that validates the API key.
func APIKeyAuth(apiKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(HeaderAPIKey)
		if key == "" {
			// Also check Authorization header
			auth := c.GetHeader("Authorization")
			if strings.HasPrefix(auth, "Bearer ") {
				key = strings.TrimPrefix(auth, "Bearer ")
			}
		}

		if key == "" {
			RespondUnauthorized(c, "API key is required")
			c.Abort()
			return
		}

		if key != apiKey {
			RespondUnauthorized(c, "Invalid API key")
			c.Abort()
			return
		}

		c.Next()
	}
}

// InternalAPIKeyAuth returns a middleware that validates the internal API key.
func InternalAPIKeyAuth(internalAPIKey string) gin.HandlerFunc {
	return func(c *gin.Context) {
		key := c.GetHeader(HeaderInternalAPIKey)
		if key == "" {
			RespondUnauthorized(c, "Internal API key is required")
			c.Abort()
			return
		}

		if key != internalAPIKey {
			RespondUnauthorized(c, "Invalid internal API key")
			c.Abort()
			return
		}

		c.Next()
	}
}
