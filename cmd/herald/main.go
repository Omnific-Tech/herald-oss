package main

import (
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/fcm"
	"github.com/Omnific-Tech/herald/internal/objectstorage"
	"github.com/Omnific-Tech/herald/internal/server"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
	"github.com/Omnific-Tech/herald/internal/webpush"
	"github.com/Omnific-Tech/herald/internal/worker"
	"github.com/rs/zerolog"
)

var version = "dev"

func main() {
	cfg, err := config.Load()
	if err != nil {
		fmt.Fprintf(os.Stderr, "load config: %v\n", err)
		os.Exit(1)
	}

	logger := newLogger(cfg.Server.Environment, cfg.Server.LogLevel)

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	var cipher *crypto.Cipher
	if cfg.Secrets.EncryptionKey != "" {
		cipher, err = crypto.NewCipher(cfg.Secrets.EncryptionKey, cfg.Secrets.RetiredKeys...)
		if err != nil {
			logger.Fatal().Err(err).Msg("load encryption key")
		}
	} else {
		// VAPID private keys are unreadable without it, so Web Push stays off.
		logger.Warn().Msg("ENCRYPTION_KEY is empty, web push is disabled")
	}

	var (
		db        *sql.DB
		dataStore store.Store
	)
	if cfg.Database.URL != "" {
		dialContext, cancel := context.WithTimeout(ctx, 10*time.Second)
		db, err = postgres.Open(dialContext, cfg.Database.URL, postgres.Options{
			MaxOpenConns:    cfg.Database.MaxOpenConns,
			MaxIdleConns:    cfg.Database.MaxIdleConns,
			ConnMaxLifetime: cfg.Database.ConnMaxLifetime,
		})
		cancel()
		if err != nil {
			logger.Fatal().Err(err).Msg("connect database")
		}
		defer func() { _ = db.Close() }()
		dataStore = postgres.New(db, cipher)
	} else {
		logger.Warn().Msg("DATABASE_URL is empty, starting without storage")
	}

	var workers sync.WaitGroup
	if dataStore != nil {
		workers.Add(1)
		go func() {
			defer workers.Done()
			worker.NewExpiry(dataStore, cfg.WebPush.ExpirySweepInterval, logger).Run(ctx)
		}()

		if cipher != nil {
			workers.Add(1)
			go func() {
				defer workers.Done()
				webpush.NewDispatcher(dataStore, cfg.WebPush, logger).Run(ctx)
			}()
		}

		if cfg.FCM.ProjectID != "" {
			var credentialsJSON []byte
			if cfg.ObjectStorage.Provider != "" {
				credentialsJSON, err = objectstorage.Read(ctx, cfg.ObjectStorage, cfg.FCM.CredentialsObject)
				if err != nil {
					logger.Fatal().Err(err).Msg("load fcm credentials")
				}
			}
			sender, err := fcm.NewSender(ctx, cfg.FCM.ProjectID, credentialsJSON)
			if err != nil {
				logger.Fatal().Err(err).Msg("configure fcm")
			}
			workers.Add(1)
			go func() {
				defer workers.Done()
				fcm.NewDispatcher(dataStore, cfg.FCM, sender, logger).Run(ctx)
			}()
		} else {
			logger.Warn().Msg("FCM_PROJECT_ID is empty, Android delivery is disabled")
		}
	}

	runError := server.New(cfg, version, db, dataStore, logger).Run(ctx)

	// Cancel before waiting. The workers only exit on ctx.Done(), so a server
	// that failed to start would otherwise leave this blocked forever and the
	// process alive but serving nothing.
	stop()
	workers.Wait()

	if runError != nil {
		logger.Fatal().Err(runError).Msg("server stopped")
	}
}

func newLogger(environment, levelName string) zerolog.Logger {
	level, err := zerolog.ParseLevel(levelName)
	if err != nil {
		level = zerolog.InfoLevel
	}

	// ConsoleWriter is for humans. In production the structured fields attached
	// throughout are the point, so emit JSON.
	var writer io.Writer = os.Stdout
	if environment != "production" {
		writer = zerolog.ConsoleWriter{Out: os.Stdout, TimeFormat: time.RFC3339}
	}

	return zerolog.New(writer).
		Level(level).
		With().
		Timestamp().
		Logger()
}
