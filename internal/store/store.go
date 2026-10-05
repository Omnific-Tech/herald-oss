// Package store defines the persistence boundary for Herald.
package store

import (
	"context"
	"errors"
	"time"

	"github.com/Omnific-Tech/herald/internal/model"
)

var (
	// ErrNotFound is returned when a lookup matches no row.
	ErrNotFound = errors.New("not found")
	// ErrConflict is returned when a unique active resource already exists.
	ErrConflict = errors.New("conflict")
	// ErrAlreadyRevoked distinguishes an existing disabled producer from an
	// unknown producer id.
	ErrAlreadyRevoked  = errors.New("already revoked")
	ErrAlreadyArchived = errors.New("already archived")
	ErrInvalidChannel  = errors.New("invalid channel")
	ErrInvalidTopic    = errors.New("invalid topic")
)

// Every scheduling and expiry decision below is made against the database
// clock. Comparing an application server's wall clock with timestamps the
// database wrote is wrong by exactly the skew between the two machines, which
// is enough to leave due deliveries unclaimed and overdue ones unexpired.

// Applications reads application records and their VAPID keypair.
type Applications interface {
	BySlug(ctx context.Context, slug string) (model.Application, error)
	// VAPIDPublicKey returns the base64url application server key for the app.
	VAPIDPublicKey(ctx context.Context, slug string) (string, error)
}

// Producers authenticates publishers. The application is derived here, which is
// what prevents a producer from publishing for another application.
type Producers interface {
	// ByToken resolves a plaintext producer token to a live, unrevoked producer.
	ByToken(ctx context.Context, token string) (model.Producer, error)
}

// Administration is Herald's authenticated control-plane persistence surface.
type Administration interface {
	CreateApplication(ctx context.Context, slug, displayName, origin, vapidSubject string) (model.Application, string, error)
	ListApplications(ctx context.Context) ([]model.Application, error)
	CreateProducer(ctx context.Context, applicationSlug, name string) (model.Producer, string, error)
	ListApplicationProducers(ctx context.Context, applicationSlug string) ([]model.Producer, error)
	RevokeProducerByID(ctx context.Context, applicationSlug, producerID string) (model.Producer, error)
	CreateSession(ctx context.Context, username, tokenHash string, ttl time.Duration) (model.AdminSession, error)
	SessionByTokenHash(ctx context.Context, tokenHash string) (model.AdminSession, error)
	DeleteSession(ctx context.Context, tokenHash string) error
}

// Routing owns the per-application topic catalog. Channel IDs come from the
// shipped Android products and are deliberately fixed.
type Routing interface {
	ListTopics(ctx context.Context, applicationID string) ([]model.RoutingTopic, error)
	CreateTopic(ctx context.Context, applicationSlug string, topic model.RoutingTopic) (model.RoutingTopic, error)
	RenameTopic(ctx context.Context, applicationSlug, topicID, label string) (model.RoutingTopic, error)
	ArchiveTopic(ctx context.Context, applicationSlug, topicID string) (model.RoutingTopic, error)
}

// Devices registers clients and authenticates subscription and ack endpoints.
type Devices interface {
	// Create provisions a new device with a server-generated install ID and
	// returns it with a freshly issued plaintext token.
	Create(ctx context.Context, applicationID string, request model.RegisterDeviceRequest) (device model.Device, token string, err error)
	// Rotate re-registers an existing device, issuing a new token and keeping
	// the sequence and delivery history. The caller must already have proved
	// ownership by presenting the device's live token.
	Rotate(ctx context.Context, deviceID string, request model.RegisterDeviceRequest) (device model.Device, token string, err error)
	ByToken(ctx context.Context, token string) (model.Device, error)
	// AcknowledgedSequence is the highest sequence the device has acknowledged.
	// It is the only value a client may safely resume from.
	AcknowledgedSequence(ctx context.Context, deviceID string) (int64, error)
	// ReplaceSubscriptions atomically swaps the device's full subscription set.
	ReplaceSubscriptions(ctx context.Context, deviceID string, subscriptions []model.Subscription) error
	Subscriptions(ctx context.Context, deviceID string) ([]model.Subscription, error)
	// UpdateFCM refreshes and re-enables an Android Firebase installation.
	UpdateFCM(ctx context.Context, deviceID, fcmFID string) error
	// DisableWebPush clears a device's push subscription after the push service
	// reports it gone. The device row, its sequence, and its delivery history
	// survive so a later re-subscribe resumes rather than restarts.
	DisableWebPush(ctx context.Context, deviceID string) error
	// DisableFCM clears an invalid Firebase installation while preserving history.
	DisableFCM(ctx context.Context, deviceID string) error
	TouchLastSeen(ctx context.Context, deviceID string) error
}

// Messages persists publications and enforces producer idempotency.
type Messages interface {
	// Publish stores the message and fans out one delivery per matching device
	// in a single transaction, allocating each device's next sequence.
	//
	// When idempotencyKey already exists for the application it returns the
	// stored message with deduplicated true and creates no new deliveries.
	Publish(ctx context.Context, producer model.Producer, request model.PublishRequest) (message model.Message, deduplicated bool, deviceCount int, err error)
	// PublishAsAdmin records the authenticated username instead of assigning the
	// message to a producer credential.
	PublishAsAdmin(ctx context.Context, application model.Application, username string, request model.PublishRequest) (message model.Message, deduplicated bool, deviceCount int, err error)
	// ListHistory returns newest first, with counts from retained delivery rows.
	ListHistory(ctx context.Context, applicationID string, beforeAt *time.Time, beforeID string, limit int) ([]model.AdminMessageHistory, error)
}

// WebPushJob is one Web Push send, assembled by a single query.
type WebPushJob struct {
	DeliveryID      string
	DeviceID        string
	Sequence        int64
	Attempts        int
	Endpoint        string
	P256DH          string
	Auth            string
	VAPIDPublicKey  string
	VAPIDPrivateKey string
	VAPIDSubject    string
	// TTLSeconds is the message's remaining lifetime, computed by the database
	// alongside the claim rather than from the caller's clock.
	TTLSeconds int
	// DecryptErr records a VAPID key that could not be recovered. The job is
	// returned rather than dropped so the dispatcher can fail that one delivery
	// instead of discarding the whole batch it was claimed with.
	DecryptErr error
	Message    model.Message
}

// FCMJob is one Android delivery addressed to a Firebase Installation ID.
type FCMJob struct {
	DeliveryID string
	DeviceID   string
	Sequence   int64
	Attempts   int
	FCMFID     string
	TTLSeconds int
	Message    model.Message
}

// Deliveries drives FCM and Web Push dispatch plus acknowledgements.
type Deliveries interface {
	// Pending returns deliveries for a device with sequence > afterSequence that
	// are not acknowledged, superseded, or expired, ordered by sequence.
	//
	// This is the replay path: a reconnecting client passes the last sequence it
	// acknowledged, not the last one it received.
	Pending(ctx context.Context, deviceID string, afterSequence int64, limit int) ([]model.StreamEvent, error)
	// MarkSent records a transmission attempt. It never acknowledges.
	MarkSent(ctx context.Context, deliveryID string) error
	// Acknowledge is idempotent. Repeating an ack returns the stored record and
	// no error. It returns ErrNotFound if the delivery is not the device's.
	Acknowledge(ctx context.Context, deviceID, deliveryID string, status model.AckStatus) (model.Acknowledgement, error)
	// ClaimWebPush leases due Web Push deliveries, pushing each claimed row's
	// next attempt forward so concurrent dispatchers do not double send.
	ClaimWebPush(ctx context.Context, limit int, lease time.Duration) ([]WebPushJob, error)
	// ClaimFCM leases due Android deliveries for the FCM dispatcher.
	ClaimFCM(ctx context.Context, limit int, lease time.Duration) ([]FCMJob, error)
	// FailUndecryptable marks a delivery whose application key could not be
	// recovered, so one bad key cannot stall the queue behind it.
	FailUndecryptable(ctx context.Context, deliveryID string) error
	// Retry returns a delivery to the queue after delay.
	Retry(ctx context.Context, deliveryID string, delay time.Duration, reason string) error
	// Fail marks a delivery permanently undeliverable.
	Fail(ctx context.Context, deliveryID string, reason string) error
	// ExpireOverdue marks unacknowledged deliveries past their message expiry.
	ExpireOverdue(ctx context.Context) (int, error)
}

// Store is the full persistence surface handed to the HTTP layer.
type Store interface {
	Administration() Administration
	Routing() Routing
	Applications() Applications
	Producers() Producers
	Devices() Devices
	Messages() Messages
	Deliveries() Deliveries
}
