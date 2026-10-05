package config_test

import (
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
)

func TestLoadUsesEnvironment(t *testing.T) {
	t.Setenv("PORT", "9090")
	t.Setenv("ENV", "test")
	t.Setenv("SERVICE_NAME", "test-herald")
	t.Setenv("READ_TIMEOUT", "5s")
	t.Setenv("CORS_ALLOWED_ORIGINS", "https://one.example, https://two.example")
	t.Setenv("DATABASE_URL", "postgres://localhost/herald")
	t.Setenv("OBJECT_STORAGE_PROVIDER", "s3")
	t.Setenv("S3_BUCKET", "herald-app")

	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}

	if cfg.Server.Port != "9090" || cfg.Server.Environment != "test" {
		t.Fatalf("unexpected server config: %+v", cfg.Server)
	}
	if cfg.Server.ReadTimeout != 5*time.Second {
		t.Fatalf("read timeout: got %s", cfg.Server.ReadTimeout)
	}
	if len(cfg.CORS.AllowedOrigins) != 2 {
		t.Fatalf("origins: got %v", cfg.CORS.AllowedOrigins)
	}
	if cfg.Database.URL != "postgres://localhost/herald" {
		t.Fatalf("database url: got %q", cfg.Database.URL)
	}
	if cfg.ObjectStorage.Provider != "s3" || cfg.ObjectStorage.Bucket != "herald-app" {
		t.Fatalf("object storage: got %+v", cfg.ObjectStorage)
	}
}

func TestLoadDefaultsWriteTimeout(t *testing.T) {
	cfg, err := config.Load()
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Server.WriteTimeout != 15*time.Second {
		t.Fatalf("write timeout: got %s want 15s", cfg.Server.WriteTimeout)
	}
}

func TestLoadRejectsInvalidValues(t *testing.T) {
	t.Setenv("READ_TIMEOUT", "soon")

	if _, err := config.Load(); err == nil {
		t.Fatal("Load succeeded with invalid duration")
	}
}

// A zero interval reaches time.NewTicker and panics there, and a panic in a
// worker goroutine takes the process down.
func TestLoadRejectsNonPositiveIntervals(t *testing.T) {
	for _, key := range []string{
		"WEBPUSH_DISPATCH_INTERVAL",
		"WEBPUSH_LEASE",
		"FCM_DISPATCH_INTERVAL",
		"FCM_LEASE",
		"EXPIRY_SWEEP_INTERVAL",
	} {
		for _, value := range []string{"0s", "-1s"} {
			t.Run(key+"="+value, func(t *testing.T) {
				t.Setenv(key, value)
				if _, err := config.Load(); err == nil {
					t.Fatalf("Load accepted %s=%s", key, value)
				}
			})
		}
	}
}
