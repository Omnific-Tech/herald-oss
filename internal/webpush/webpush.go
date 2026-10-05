// Package webpush delivers messages to desktop and Apple PWAs.
package webpush

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	push "github.com/SherClockHolmes/webpush-go"
	"github.com/rs/zerolog"
)

const (
	// maxBackoff caps the retry delay for a push service that keeps refusing.
	maxBackoff = 15 * time.Minute
	// maxTTL is the largest TTL the major push services accept. FCM rejects
	// anything longer with a 400, which is not retryable.
	maxTTL = 28 * 24 * 60 * 60
	// sendTimeout bounds one request. webpush-go otherwise uses an http.Client
	// with no timeout, so a push service that accepts the connection and never
	// answers would stall the dispatcher for every application.
	sendTimeout = 15 * time.Second
)

// topicPattern is the Push API's Topic header constraint. A collapse key that
// does not fit is dropped rather than rejected: collapsing is an optimisation,
// and losing it must not lose the notification.
var topicPattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

var pushHTTPClient = &http.Client{
	Timeout: sendTimeout,
	Transport: &http.Transport{
		Proxy:       http.ProxyFromEnvironment,
		DialContext: safeDialContext,
	},
	CheckRedirect: func(request *http.Request, _ []*http.Request) error {
		if request.URL.Scheme != "https" {
			return errors.New("web push redirect must remain https")
		}
		return nil
	},
}

// Sender is the seam the dispatcher sends through, so its retry, failure and
// unsubscribe handling can be tested without a live push service.
type Sender interface {
	Send(ctx context.Context, payload []byte, subscription *push.Subscription, options *push.Options) (*http.Response, error)
}

type libarySender struct{}

func (libarySender) Send(ctx context.Context, payload []byte, subscription *push.Subscription, options *push.Options) (*http.Response, error) {
	return push.SendNotificationWithContext(ctx, payload, subscription, options)
}

// Dispatcher claims due Web Push deliveries and sends them.
type Dispatcher struct {
	store  store.Store
	cfg    config.WebPush
	sender Sender
	logger zerolog.Logger
}

func NewDispatcher(dataStore store.Store, cfg config.WebPush, logger zerolog.Logger) *Dispatcher {
	return &Dispatcher{store: dataStore, cfg: cfg, sender: libarySender{}, logger: logger}
}

// WithSender replaces the transport. Tests use it; production does not.
func (d *Dispatcher) WithSender(sender Sender) *Dispatcher {
	d.sender = sender
	return d
}

// Run dispatches until ctx is cancelled.
func (d *Dispatcher) Run(ctx context.Context) {
	ticker := time.NewTicker(d.cfg.DispatchInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := d.Dispatch(ctx); err != nil && !errors.Is(err, context.Canceled) {
				d.logger.Error().Err(err).Msg("web push dispatch failed")
			}
		}
	}
}

// Dispatch claims one batch and sends it.
func (d *Dispatcher) Dispatch(ctx context.Context) error {
	jobs, err := d.store.Deliveries().ClaimWebPush(ctx, d.cfg.BatchSize, d.cfg.Lease)
	if err != nil {
		return err
	}

	for _, job := range jobs {
		d.send(ctx, job)
	}
	return nil
}

func (d *Dispatcher) send(ctx context.Context, job store.WebPushJob) {
	// The claim already committed this job's lease, so a key this process cannot
	// read has to be retired here. Left alone it is re-claimed every lease
	// forever, inflating attempts and starving the rest of the batch.
	if job.DecryptErr != nil {
		d.logger.Error().Err(job.DecryptErr).
			Str("delivery_id", job.DeliveryID).
			Str("application_id", job.Message.ApplicationID).
			Msg("vapid key unreadable, failing delivery")
		if err := d.store.Deliveries().FailUndecryptable(ctx, job.DeliveryID); err != nil {
			d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("fail undecryptable delivery")
		}
		return
	}

	// TTLSeconds comes from the database alongside the claim. Recomputing it
	// here would compare the database's expiry against this machine's clock.
	if job.TTLSeconds <= 0 {
		d.fail(ctx, job, "expired before Web Push send")
		return
	}
	ttl := min(job.TTLSeconds, maxTTL)

	payload, err := json.Marshal(model.StreamEvent{
		DeliveryID: job.DeliveryID,
		Sequence:   job.Sequence,
		Message:    job.Message.Delivery(),
	})
	if err != nil {
		d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("encode web push payload")
		d.fail(ctx, job, "Web Push payload encoding failed")
		return
	}

	urgency := push.UrgencyNormal
	if job.Message.Priority == model.PriorityHigh {
		urgency = push.UrgencyHigh
	}

	topic := ""
	if topicPattern.MatchString(job.Message.CollapseKey) {
		topic = job.Message.CollapseKey
	}

	sendCtx, cancel := context.WithTimeout(ctx, sendTimeout)
	defer cancel()

	response, err := d.sender.Send(sendCtx, payload, &push.Subscription{
		Endpoint: job.Endpoint,
		Keys:     push.Keys{P256dh: job.P256DH, Auth: job.Auth},
	}, &push.Options{
		Subscriber:      vapidSubject(job.VAPIDSubject),
		VAPIDPublicKey:  job.VAPIDPublicKey,
		VAPIDPrivateKey: job.VAPIDPrivateKey,
		TTL:             ttl,
		Urgency:         urgency,
		Topic:           topic,
		HTTPClient:      pushHTTPClient,
	})
	if err != nil {
		d.retry(ctx, job, fmt.Errorf("send web push: %w", err), "Web Push transport error")
		return
	}
	defer func() { _ = response.Body.Close() }()

	switch {
	case response.StatusCode >= 200 && response.StatusCode < 300:
		// The device still has to acknowledge, so this is sent, not acknowledged.
		if err := d.store.Deliveries().MarkSent(ctx, job.DeliveryID); err != nil {
			d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("mark web push sent")
		}

	case response.StatusCode == http.StatusGone:
		// RFC 8030: the subscription no longer exists.
		d.logger.Info().Str("device_id", job.DeviceID).Msg("web push subscription gone")
		if err := d.store.Devices().DisableWebPush(ctx, job.DeviceID); err != nil {
			d.logger.Error().Err(err).Str("device_id", job.DeviceID).Msg("disable web push")
		}
		d.fail(ctx, job, "Web Push HTTP 410: subscription gone")

	case response.StatusCode == http.StatusNotFound:
		// A 404 is also "gone" per RFC 8030, but a misrouted proxy produces one
		// too, so it only disables the subscription once retries are spent.
		if job.Attempts >= d.cfg.MaxAttempts {
			d.logger.Info().Str("device_id", job.DeviceID).Msg("web push endpoint repeatedly absent")
			if err := d.store.Devices().DisableWebPush(ctx, job.DeviceID); err != nil {
				d.logger.Error().Err(err).Str("device_id", job.DeviceID).Msg("disable web push")
			}
		}
		d.retry(ctx, job, errors.New("push service returned 404"), "Web Push HTTP 404: endpoint unavailable")

	case response.StatusCode == http.StatusTooManyRequests || response.StatusCode >= 500:
		d.retry(ctx, job, fmt.Errorf("push service returned %d", response.StatusCode), fmt.Sprintf("Web Push HTTP %d", response.StatusCode))

	default:
		// Not retryable.
		d.logger.Error().
			Int("status", response.StatusCode).
			Str("delivery_id", job.DeliveryID).
			Msg("web push rejected")
		d.fail(ctx, job, fmt.Sprintf("Web Push HTTP %d", response.StatusCode))
	}
}

func safeDialContext(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	addresses, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, address := range addresses {
		ip := address.IP
		if ip.IsPrivate() || ip.IsLoopback() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsUnspecified() {
			return nil, fmt.Errorf("web push endpoint resolved to a private address")
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("web push endpoint resolved to no addresses")
	}
	return (&net.Dialer{Timeout: sendTimeout}).DialContext(ctx, network, net.JoinHostPort(addresses[0].IP.String(), port))
}

func (d *Dispatcher) retry(ctx context.Context, job store.WebPushJob, cause error, reason string) {
	if job.Attempts >= d.cfg.MaxAttempts {
		d.logger.Error().Err(cause).Str("delivery_id", job.DeliveryID).Msg("web push exhausted retries")
		d.fail(ctx, job, reason)
		return
	}

	d.logger.Warn().Err(cause).
		Str("delivery_id", job.DeliveryID).
		Int("attempts", job.Attempts).
		Msg("web push will retry")

	if err := d.store.Deliveries().Retry(ctx, job.DeliveryID, backoff(job.Attempts), reason); err != nil {
		d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("reschedule web push")
	}
}

func (d *Dispatcher) fail(ctx context.Context, job store.WebPushJob, reason string) {
	if err := d.store.Deliveries().Fail(ctx, job.DeliveryID, reason); err != nil {
		d.logger.Error().Err(err).Str("delivery_id", job.DeliveryID).Msg("fail web push delivery")
	}
}

// vapidSubject strips a mailto: prefix. webpush-go prepends one to any subject
// that is not an https URL, so passing the stored "mailto:ops@example" through
// would sign a JWT with sub "mailto:mailto:ops@example", which strict push
// services reject.
func vapidSubject(subject string) string {
	return strings.TrimPrefix(subject, "mailto:")
}

// backoff doubles from 30 seconds per attempt, capped.
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
