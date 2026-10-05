package middleware_test

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Omnific-Tech/herald/internal/middleware"
	"github.com/gin-gonic/gin"
)

func TestRequireBearer(t *testing.T) {
	gin.SetMode(gin.TestMode)

	tests := []struct {
		name   string
		header string
		want   int
	}{
		{name: "valid", header: "Bearer secret", want: http.StatusOK},
		{name: "missing", want: http.StatusUnauthorized},
		{name: "empty token", header: "Bearer ", want: http.StatusUnauthorized},
		{name: "wrong scheme", header: "Basic secret", want: http.StatusUnauthorized},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			router := gin.New()
			router.Use(middleware.RequireBearer())
			router.POST("/", func(c *gin.Context) {
				c.String(http.StatusOK, middleware.BearerToken(c))
			})

			request := httptest.NewRequest(http.MethodPost, "/", nil)
			if tt.header != "" {
				request.Header.Set("Authorization", tt.header)
			}
			response := httptest.NewRecorder()
			router.ServeHTTP(response, request)

			if response.Code != tt.want {
				t.Fatalf("status: got %d want %d", response.Code, tt.want)
			}
			if tt.want == http.StatusOK && response.Body.String() != "secret" {
				t.Fatalf("token: got %q", response.Body.String())
			}
		})
	}
}
