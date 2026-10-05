package middleware

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

const bearerTokenKey = "bearer_token"

// RequireBearer rejects requests without a well-formed Authorization header.
//
// Which principal the token names is resolved by the handler against storage,
// because that lookup is what binds a publish to a single application.
func RequireBearer() gin.HandlerFunc {
	return func(c *gin.Context) {
		token, ok := ParseBearer(c)
		if !ok {
			c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "missing or invalid authorization header"})
			return
		}

		c.Set(bearerTokenKey, token)
		c.Next()
	}
}

// ParseBearer reads the Authorization header without requiring it.
func ParseBearer(c *gin.Context) (string, bool) {
	scheme, token, ok := strings.Cut(c.GetHeader("Authorization"), " ")
	token = strings.TrimSpace(token)
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", false
	}
	return token, true
}

// BearerToken returns the credential stored by RequireBearer.
func BearerToken(c *gin.Context) string {
	return c.GetString(bearerTokenKey)
}
