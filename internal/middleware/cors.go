package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func CORS(allowedOrigins []string) gin.HandlerFunc {
	allowed := make(map[string]struct{}, len(allowedOrigins))
	for _, origin := range allowedOrigins {
		allowed[origin] = struct{}{}
	}

	return func(c *gin.Context) {
		// The admin control plane has its own exact-origin credentialed policy.
		if strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}
		origin := c.GetHeader("Origin")
		if _, ok := allowed["*"]; ok {
			c.Header("Access-Control-Allow-Origin", "*")
		} else if _, ok := allowed[origin]; ok {
			c.Header("Access-Control-Allow-Origin", origin)
			c.Header("Vary", "Origin")
			c.Header("Access-Control-Allow-Credentials", "true")
		}

		c.Header("Access-Control-Allow-Headers", "Authorization, Content-Type, Last-Event-ID, X-Request-ID")
		c.Header("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")

		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// AdminCORS applies only to /api/* and never reflects any origin other than
// the configured admin UI. It also prevents browsers and intermediaries from
// caching authenticated responses.
func AdminCORS(adminOrigin string) gin.HandlerFunc {
	return func(c *gin.Context) {
		if !strings.HasPrefix(c.Request.URL.Path, "/api/") {
			c.Next()
			return
		}

		c.Header("Cache-Control", "no-store")
		c.Header("Pragma", "no-cache")
		origin := c.GetHeader("Origin")
		if adminOrigin != "" && origin == adminOrigin {
			c.Header("Access-Control-Allow-Origin", adminOrigin)
			c.Header("Access-Control-Allow-Credentials", "true")
			c.Header("Access-Control-Allow-Headers", "Content-Type, X-Request-ID")
			c.Header("Access-Control-Allow-Methods", "GET, POST, PATCH, DELETE, OPTIONS")
			c.Header("Vary", "Origin")
		}

		if c.Request.Method == http.MethodOptions {
			if adminOrigin == "" || origin != adminOrigin {
				c.AbortWithStatusJSON(http.StatusForbidden, gin.H{"error": "origin is not allowed", "code": "origin_rejected"})
				return
			}
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
