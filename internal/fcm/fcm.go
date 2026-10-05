// Package fcm delivers native Android notifications through Firebase Cloud Messaging.
package fcm

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/rs/zerolog"
	"golang.org/x/oauth2"
	"golang.org/x/oauth2/google"
)

const (
	fcmScope           = "https://www.googleapis.com/auth/firebase.messaging"
	maxBackoff         = 15 * time.Minute
	requestTimeout     = 15 * time.Second
	deliveryPayloadKey = "herald_delivery"
)

// Sender is the authenticated FCM HTTP transport.
type Sender interface {
	Send(ctx context.Context, payload []byte) (status int, body []byte, err error)
}

type httpSender struct {
	client *http.Client
	url    string
}

// NewSender uses the supplied service-account JSON, or Application Default
// Credentials when credentialsJSON is empty.
func NewSender(ctx context.Context, projectID string, credentialsJSON []byte) (Sender, error) {
	var (
		credentials *google.Credentials
		err         error
	)
	if len(credentialsJSON) > 0 {
		credentials, err = google.CredentialsFromJSON(ctx, credentialsJSON, fcmScope)
	} else {
		credentials, err = google.FindDefaultCredentials(ctx, fcmScope)
	}
	if err != nil {
		return nil, fmt.Errorf("find firebase credentials: %w", err)
	}
	return &httpSender{
		client: oauth2.NewClient(ctx, credentials.TokenSource),
		url:    "https://fcm.googleapis.com/v1/projects/" + url.PathEscape(projectID) + "/messages:send",
	}, nil
}

func (s *httpSender) Send(ctx context.Context, payload []byte) (int, []byte, error) {
	requestCtx, cancel := context.WithTimeout(ctx, requestTimeout)
	defer cancel()

	request, err := http.NewRequestWithContext(requestCtx, http.MethodPost, s.url, bytes.NewReader(payload))
	if err != nil {
		return 0, nil, err
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := s.client.Do(request)
	if err != nil {
		return 0, nil, err
	}
	defer func() { _ = response.Body.Close() }()
	body, err := io.ReadAll(io.LimitReader(response.Body, 64<<10))
	return response.StatusCode, body, err
}

// Dispatcher claims Android deliveries and hands them to FCM.
type Dispatcher struct {
	store  store.Store
	cfg    config.FCM
	sender Sender
	logger zerolog.Logger
}

func NewDispatcher(dataStore store.Store, cfg config.FCM, sender Sender, logger zerolog.Logger) *Dispatcher {
	return &Dispatcher{store: dataStore, cfg: cfg, sender: sender, logger: logger}
}

func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.DispatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.Dispatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Error().Err(err).Msg("fcm dispatch failed")
			}
		}
	}
}

func (d *Dispatcher) Dispatch(ctx context.Context) error {
	jobs, err := d.store.Deliveries().ClaimFCM(ctx, d.cfg.BatchSize, d.cfg.Lease)
	if err != nil {
		return err
	}
	for _, job := range jobs {
		d.send(ctx, job)
	}
	return nil
}

func (d *Dispatcher) send(ctx context.Context, job store.FCMJob) {
	if job.TTLSeconds <= 0 {
		d.fail(ctx, job, "expired before FCM send")
		return
	}

	delivery, err := json.Marshal(model.StreamEvent{
		DeliveryID: job.DeliveryID,
		Sequence:   job.Sequence,
		Message:    job.Message.Delivery(),
	})
	if err != nil || len(delivery) > model.MaxPushDeliveryBytes {
		d.logger.Error().Err(err).Int("bytes", len(delivery)).Str("delivery_id", job.DeliveryID).Msg("fcm payload too large")
		d.fail(ctx, job, "FCM payload encoding failed or too large")
		return
	}

	priority := "normal"
	if job.Message.Priority == model.PriorityHigh {
		priority = "high"
	}
	payload, err := json.Marshal(map[string]any{
		"message": map[string]any{
			"fid":  job.FCMFID,
			"data": map[string]string{deliveryPayloadKey: string(delivery)},
			"android": map[string]any{
				"priority":     priority,
				"ttl":          fmt.Sprintf("%ds", job.TTLSeconds),
				"collapse_key": job.Message.CollapseKey,
			},
		},
	})
	if err != nil {
		d.fail(ctx, job, "FCM payload encoding failed")
		return
	}

	status, body, err := d.sender.Send(ctx, payload)
	if err != nil {
		d.retry(ctx, job, fmt.Errorf("send fcm: %w", err), "FCM transport error")
		return
	}
	reason := fcmResponseReason(status, body, job.FCMFID)
	switch {
	case status >= 200 && status < 300:
		if err := d.store.Deliveries().MarkSent(ctx, job.DeliveryID); err != nil {
			d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("mark fcm sent")
		}
	case status == http.StatusNotFound:
		if err := d.store.Devices().DisableFCM(ctx, job.DeviceID); err != nil {
			d.logger.Error().Err(err).Str("device_id", job.DeviceID).Msg("disable fcm")
		}
		d.fail(ctx, job, reason)
	case status == http.StatusTooManyRequests || status >= 500 || status == http.StatusUnauthorized || status == http.StatusForbidden:
		d.retry(ctx, job, fmt.Errorf("fcm returned %d: %s", status, body), reason)
	default:
		d.logger.Error().Int("status", status).Bytes("body", body).Str("delivery_id", job.DeliveryID).Msg("fcm rejected")
		d.fail(ctx, job, reason)
	}
}

func fcmResponseReason(status int, body []byte, fid string) string {
	var response struct {
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if json.Unmarshal(body, &response) == nil && response.Error.Message != "" {
		message := strings.TrimSpace(response.Error.Message)
		if fid != "" {
			message = strings.ReplaceAll(message, fid, "[redacted]")
		}
		return fmt.Sprintf("FCM %d: %s", status, message)
	}
	return fmt.Sprintf("FCM HTTP %d", status)
}

func (d *Dispatcher) retry(ctx context.Context, job store.FCMJob, cause error, reason string) {
	if job.Attempts >= d.cfg.MaxAttempts {
		d.logger.Error().Err(cause).Str("delivery_id", job.DeliveryID).Msg("fcm exhausted retries")
		d.fail(ctx, job, reason)
		return
	}
	if err := d.store.Deliveries().Retry(ctx, job.DeliveryID, backoff(job.Attempts), reason); err != nil {
		d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("reschedule fcm")
	}
}

func (d *Dispatcher) fail(ctx context.Context, job store.FCMJob, reason string) {
	if err := d.store.Deliveries().Fail(ctx, job.DeliveryID, reason); err != nil {
		d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("fail fcm delivery")
	}
}

func backoff(attempts int) time.Duration {
	delay := 30 * time.Second
	for attempt := 1; attempt < attempts; attempt++ {
		delay *= 2
		if delay >= maxBackoff {
			return maxBackoff
		}
	}
	return delay
}
