package server_test

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/server"
	"github.com/rs/zerolog"
)

func testConfig() config.Config {
	return config.Config{
		Server: config.Server{
			Port:            "0",
			Environment:     "test",
			ServiceName:     "test-herald",
			ReadTimeout:     time.Second,
			IdleTimeout:     time.Second,
			ShutdownTimeout: time.Second,
			MaxBodyBytes:    1 << 20,
		},
	}
}

func newHandler() http.Handler {
	return server.New(testConfig(), "test", nil, nil, zerolog.Nop()).Handler()
}

func TestHealthReportsDegradedWithoutDatabase(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/health", nil)
	response := httptest.NewRecorder()

	newHandler().ServeHTTP(response, request)

	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want %d", response.Code, http.StatusServiceUnavailable)
	}
	if response.Header().Get("X-Request-ID") == "" {
		t.Fatal("missing X-Request-ID response header")
	}
}

// Routing must be decided before storage: an unauthorized caller gets 401 and
// an authorized one gets 503 for missing storage, never 404.
func TestVersionOneRoutesAreWired(t *testing.T) {
	tests := []struct {
		method string
		path   string
		auth   bool
		want   int
	}{
		{method: http.MethodGet, path: "/v1/applications/sample/vapid-public-key", want: http.StatusServiceUnavailable},
		{method: http.MethodPost, path: "/v1/devices/register", want: http.StatusServiceUnavailable},
		{method: http.MethodPost, path: "/v1/messages", want: http.StatusUnauthorized},
		{method: http.MethodPost, path: "/v1/messages", auth: true, want: http.StatusServiceUnavailable},
		{method: http.MethodPut, path: "/v1/subscriptions", want: http.StatusUnauthorized},
		{method: http.MethodPut, path: "/v1/subscriptions", auth: true, want: http.StatusServiceUnavailable},
		{method: http.MethodPut, path: "/v1/device/fcm", auth: true, want: http.StatusServiceUnavailable},
		{method: http.MethodPost, path: "/v1/deliveries/abc/ack", auth: true, want: http.StatusServiceUnavailable},
	}

	for _, tt := range tests {
		t.Run(tt.method+" "+tt.path, func(t *testing.T) {
			request := httptest.NewRequest(tt.method, tt.path, nil)
			if tt.auth {
				request.Header.Set("Authorization", "Bearer token")
			}
			response := httptest.NewRecorder()

			newHandler().ServeHTTP(response, request)

			if response.Code != tt.want {
				t.Fatalf("status: got %d want %d", response.Code, tt.want)
			}
		})
	}
}

func TestUnknownRouteReturnsNotFound(t *testing.T) {
	request := httptest.NewRequest(http.MethodGet, "/v1/nope", nil)
	response := httptest.NewRecorder()

	newHandler().ServeHTTP(response, request)

	if response.Code != http.StatusNotFound {
		t.Fatalf("status: got %d want %d", response.Code, http.StatusNotFound)
	}
}
