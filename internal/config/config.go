package config

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/joho/godotenv"
)

type Config struct {
	Server        Server
	CORS          CORS
	Admin         Admin
	Database      Database
	WebPush       WebPush
	FCM           FCM
	ObjectStorage ObjectStorage
	Secrets       Secrets
}

type Server struct {
	Port            string
	Environment     string
	ServiceName     string
	LogLevel        string
	ReadTimeout     time.Duration
	WriteTimeout    time.Duration
	IdleTimeout     time.Duration
	ShutdownTimeout time.Duration
	MaxBodyBytes    int64
}

type CORS struct {
	AllowedOrigins []string
}

// Admin configures the single-operator control plane. It is disabled when
// Username and PasswordHash are both empty.
type Admin struct {
	Origin           string
	Username         string
	PasswordHash     string
	SessionTTL       time.Duration
	LoginMaxAttempts int
	LoginWindow      time.Duration
}

func (a Admin) Enabled() bool {
	return a.Username != "" && a.PasswordHash != ""
}

type Database struct {
	URL             string
	MaxOpenConns    int
	MaxIdleConns    int
	ConnMaxLifetime time.Duration
}

// WebPush tunes the dispatcher that pushes to desktop and Apple PWAs.
type WebPush struct {
	// DispatchInterval is how often the dispatcher claims due deliveries.
	DispatchInterval time.Duration
	// BatchSize caps how many deliveries one dispatch pass claims.
	BatchSize int
	// Lease is how far a claimed delivery's next attempt is pushed forward, so a
	// crashed dispatcher's work is retried rather than lost.
	Lease time.Duration
	// MaxAttempts bounds retries before a delivery is marked failed.
	MaxAttempts int
	// ExpirySweepInterval is how often overdue deliveries are marked expired.
	ExpirySweepInterval time.Duration
}

// FCM tunes native Android delivery through Firebase Cloud Messaging.
type FCM struct {
	ProjectID         string
	CredentialsObject string
	DispatchInterval  time.Duration
	BatchSize         int
	Lease             time.Duration
	MaxAttempts       int
}

// ObjectStorage locates secrets stored in an S3-compatible private bucket.
type ObjectStorage struct {
	Provider        string
	Endpoint        string
	Bucket          string
	Region          string
	AccessKeyID     string
	SecretAccessKey string
}

// Secrets holds key material read from the environment.
type Secrets struct {
	// EncryptionKey is a base64 encoded 32 byte key protecting VAPID private
	// keys at rest. Web Push is disabled when it is empty.
	EncryptionKey string
	// RetiredKeys are previous encryption keys that must still decrypt during a
	// rotation, until `heraldctl rewrap` has re-encrypted everything.
	RetiredKeys []string
}

func Load() (Config, error) {
	_ = godotenv.Load()

	maxBodyBytes, err := integer("MAX_BODY_BYTES", 1<<20)
	if err != nil {
		return Config{}, err
	}
	readTimeout, err := duration("READ_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	writeTimeout, err := duration("WRITE_TIMEOUT", 15*time.Second)
	if err != nil {
		return Config{}, err
	}
	idleTimeout, err := duration("IDLE_TIMEOUT", 60*time.Second)
	if err != nil {
		return Config{}, err
	}
	shutdownTimeout, err := duration("SHUTDOWN_TIMEOUT", 10*time.Second)
	if err != nil {
		return Config{}, err
	}
	connMaxLifetime, err := duration("DATABASE_CONN_MAX_LIFETIME", 30*time.Minute)
	if err != nil {
		return Config{}, err
	}
	maxOpenConns, err := integer("DATABASE_MAX_OPEN_CONNS", 20)
	if err != nil {
		return Config{}, err
	}
	maxIdleConns, err := integer("DATABASE_MAX_IDLE_CONNS", 5)
	if err != nil {
		return Config{}, err
	}
	dispatchInterval, err := positiveDuration("WEBPUSH_DISPATCH_INTERVAL", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	webPushBatchSize, err := integer("WEBPUSH_BATCH_SIZE", 50)
	if err != nil {
		return Config{}, err
	}
	webPushLease, err := positiveDuration("WEBPUSH_LEASE", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	maxAttempts, err := integer("WEBPUSH_MAX_ATTEMPTS", 5)
	if err != nil {
		return Config{}, err
	}
	expirySweepInterval, err := positiveDuration("EXPIRY_SWEEP_INTERVAL", time.Minute)
	if err != nil {
		return Config{}, err
	}
	fcmDispatchInterval, err := positiveDuration("FCM_DISPATCH_INTERVAL", 2*time.Second)
	if err != nil {
		return Config{}, err
	}
	fcmBatchSize, err := integer("FCM_BATCH_SIZE", 50)
	if err != nil {
		return Config{}, err
	}
	fcmLease, err := positiveDuration("FCM_LEASE", 30*time.Second)
	if err != nil {
		return Config{}, err
	}
	fcmMaxAttempts, err := integer("FCM_MAX_ATTEMPTS", 5)
	if err != nil {
		return Config{}, err
	}
	adminSessionTTL, err := positiveDuration("ADMIN_SESSION_TTL", 8*time.Hour)
	if err != nil {
		return Config{}, err
	}
	adminLoginMaxAttempts, err := integer("ADMIN_LOGIN_MAX_ATTEMPTS", 5)
	if err != nil {
		return Config{}, err
	}
	adminLoginWindow, err := positiveDuration("ADMIN_LOGIN_WINDOW", 15*time.Minute)
	if err != nil {
		return Config{}, err
	}
	adminUsername := env("ADMIN_USERNAME", "")
	adminPasswordHash := env("ADMIN_PASSWORD_HASH", "")
	if (adminUsername == "") != (adminPasswordHash == "") {
		return Config{}, fmt.Errorf("ADMIN_USERNAME and ADMIN_PASSWORD_HASH must be set together")
	}
	if adminPasswordHash != "" {
		if err := crypto.ValidatePasswordHash(adminPasswordHash); err != nil {
			return Config{}, fmt.Errorf("ADMIN_PASSWORD_HASH: %w", err)
		}
	}

	return Config{
		Server: Server{
			Port:            env("PORT", "8080"),
			Environment:     env("ENV", "development"),
			ServiceName:     env("SERVICE_NAME", "herald"),
			LogLevel:        env("LOG_LEVEL", "info"),
			ReadTimeout:     readTimeout,
			WriteTimeout:    writeTimeout,
			IdleTimeout:     idleTimeout,
			ShutdownTimeout: shutdownTimeout,
			MaxBodyBytes:    int64(maxBodyBytes),
		},
		CORS: CORS{
			AllowedOrigins: list("CORS_ALLOWED_ORIGINS", "http://localhost:5173"),
		},
		Admin: Admin{
			Origin:           env("ADMIN_ORIGIN", "http://localhost:5173"),
			Username:         adminUsername,
			PasswordHash:     adminPasswordHash,
			SessionTTL:       adminSessionTTL,
			LoginMaxAttempts: adminLoginMaxAttempts,
			LoginWindow:      adminLoginWindow,
		},
		Database: Database{
			URL:             env("DATABASE_URL", ""),
			MaxOpenConns:    maxOpenConns,
			MaxIdleConns:    maxIdleConns,
			ConnMaxLifetime: connMaxLifetime,
		},
		WebPush: WebPush{
			DispatchInterval:    dispatchInterval,
			BatchSize:           webPushBatchSize,
			Lease:               webPushLease,
			MaxAttempts:         maxAttempts,
			ExpirySweepInterval: expirySweepInterval,
		},
		FCM: FCM{
			ProjectID:         env("FCM_PROJECT_ID", ""),
			CredentialsObject: env("FCM_CREDENTIALS_OBJECT", "firebase-svc-acc-private-key.json"),
			DispatchInterval:  fcmDispatchInterval,
			BatchSize:         fcmBatchSize,
			Lease:             fcmLease,
			MaxAttempts:       fcmMaxAttempts,
		},
		ObjectStorage: ObjectStorage{
			Provider:        env("OBJECT_STORAGE_PROVIDER", ""),
			Endpoint:        env("S3_ENDPOINT", ""),
			Bucket:          env("S3_BUCKET", ""),
			Region:          env("AWS_DEFAULT_REGION", ""),
			AccessKeyID:     env("AWS_ACCESS_KEY_ID", ""),
			SecretAccessKey: env("AWS_SECRET_ACCESS_KEY", ""),
		},
		Secrets: Secrets{
			EncryptionKey: env("ENCRYPTION_KEY", ""),
			RetiredKeys:   list("ENCRYPTION_KEYS_RETIRED", ""),
		},
	}, nil
}

func env(key, fallback string) string {
	if value, ok := os.LookupEnv(key); ok {
		return value
	}
	return fallback
}

func duration(key string, fallback time.Duration) (time.Duration, error) {
	value := env(key, fallback.String())
	parsed, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("%s must be a duration: %w", key, err)
	}
	return parsed, nil
}

// positiveDuration rejects zero and negative values. They reach time.NewTicker
// and panic there, and a panic in a worker goroutine kills the process.
func positiveDuration(key string, fallback time.Duration) (time.Duration, error) {
	value, err := duration(key, fallback)
	if err != nil {
		return 0, err
	}
	if value <= 0 {
		return 0, fmt.Errorf("%s must be a positive duration", key)
	}
	return value, nil
}

func integer(key string, fallback int) (int, error) {
	value := env(key, strconv.Itoa(fallback))
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", key)
	}
	return parsed, nil
}

func list(key, fallback string) []string {
	parts := strings.Split(env(key, fallback), ",")
	values := make([]string, 0, len(parts))
	for _, part := range parts {
		if value := strings.TrimSpace(part); value != "" {
			values = append(values, value)
		}
	}
	return values
}
