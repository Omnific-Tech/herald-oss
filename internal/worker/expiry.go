// Package worker holds Herald's background loops.
package worker

import (
	"context"
	"errors"
	"time"

	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/rs/zerolog"
)

// Expiry marks overdue deliveries expired.
//
// The stream and dispatch queries already filter on expiry, so this loop exists
// to settle the record rather than to enforce the rule.
type Expiry struct {
	store    store.Store
	interval time.Duration
	logger   zerolog.Logger
}

func NewExpiry(dataStore store.Store, interval time.Duration, logger zerolog.Logger) *Expiry {
	return &Expiry{store: dataStore, interval: interval, logger: logger}
}

func (e *Expiry) Run(ctx context.Context) {
	ticker := time.NewTicker(e.interval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			expired, err := e.store.Deliveries().ExpireOverdue(ctx)
			if err != nil {
				if !errors.Is(err, context.Canceled) {
					e.logger.Error().Err(err).Msg("expire deliveries")
				}
				continue
			}
			if expired > 0 {
				e.logger.Info().Int("count", expired).Msg("deliveries expired")
			}
		}
	}
}
