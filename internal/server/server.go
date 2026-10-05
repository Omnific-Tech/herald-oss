package server

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/middleware"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type Server struct {
	httpServer      *http.Server
	shutdownTimeout time.Duration
	logger          zerolog.Logger
}

// New builds the HTTP server. db and dataStore may be nil, in which case
// /health reports a degraded database and the v1 handlers answer 503.
func New(cfg config.Config, version string, db *sql.DB, dataStore store.Store, logger zerolog.Logger) *Server {
	if cfg.Server.Environment == "production" {
		gin.SetMode(gin.ReleaseMode)
	}

	registerValidators()

	router := gin.New()
	router.Use(
		middleware.RequestID(),
		middleware.AccessLogger(logger),
		middleware.Recovery(logger),
		middleware.SecurityHeaders(),
		middleware.AdminCORS(cfg.Admin.Origin),
		middleware.CORS(cfg.CORS.AllowedOrigins),
		middleware.MaxBodyBytes(cfg.Server.MaxBodyBytes),
	)

	api := &api{cfg: cfg, version: version, db: db, store: dataStore, logger: logger, loginLimiter: newLoginLimiter()}
	api.register(router)

	return &Server{
		httpServer: &http.Server{
			Addr:              ":" + cfg.Server.Port,
			Handler:           router,
			ReadTimeout:       cfg.Server.ReadTimeout,
			ReadHeaderTimeout: cfg.Server.ReadTimeout,
			WriteTimeout:      cfg.Server.WriteTimeout,
			IdleTimeout:       cfg.Server.IdleTimeout,
			MaxHeaderBytes:    1 << 20,
		},
		shutdownTimeout: cfg.Server.ShutdownTimeout,
		logger:          logger,
	}
}

func (s *Server) Handler() http.Handler {
	return s.httpServer.Handler
}

// Routes reports the wired route table, so a test can hold it against the
// protocol document.
func (s *Server) Routes() gin.RoutesInfo {
	return s.httpServer.Handler.(*gin.Engine).Routes()
}

func (s *Server) Run(ctx context.Context) error {
	errorsChannel := make(chan error, 1)
	go func() {
		s.logger.Info().Str("address", s.httpServer.Addr).Msg("server started")
		errorsChannel <- s.httpServer.ListenAndServe()
	}()

	select {
	case err := <-errorsChannel:
		if errors.Is(err, http.ErrServerClosed) {
			return nil
		}
		return fmt.Errorf("serve: %w", err)
	case <-ctx.Done():
		shutdownContext, cancel := context.WithTimeout(context.Background(), s.shutdownTimeout)
		defer cancel()

		s.logger.Info().Msg("server shutting down")
		if err := s.httpServer.Shutdown(shutdownContext); err != nil {
			return fmt.Errorf("shutdown: %w", err)
		}
		return nil
	}
}
