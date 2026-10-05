package webpush_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
	"github.com/Omnific-Tech/herald/internal/testsupport"
	"github.com/Omnific-Tech/herald/internal/webpush"
	push "github.com/SherClockHolmes/webpush-go"
	"github.com/rs/zerolog"
)

// recorder stands in for a push service.
type recorder struct {
	status  int
	err     error
	calls   int
	payload []byte
	options push.Options
}

func (r *recorder) Send(_ context.Context, payload []byte, _ *push.Subscription, options *push.Options) (*http.Response, error) {
	r.calls++
	r.payload = payload
	r.options = *options
	if r.err != nil {
		return nil, r.err
	}
	return &http.Response{StatusCode: r.status, Body: io.NopCloser(strings.NewReader(""))}, nil
}

type fixture struct {
	db         *sql.DB
	store      *postgres.Store
	dispatcher *webpush.Dispatcher
	sender     *recorder
	device     model.Device
	producer   model.Producer
}

func newFixture(t *testing.T, sender *recorder, cipher *crypto.Cipher) fixture {
	t.Helper()

	db := testsupport.Database(t, "webpush")
	ctx := context.Background()

	if cipher == nil {
		key, _ := crypto.NewEncryptionKey()
		built, err := crypto.NewCipher(key)
		if err != nil {
			t.Fatalf("cipher: %v", err)
		}
		cipher = built
	}

	dataStore := postgres.New(db, cipher)
	admin := postgres.NewAdmin(dataStore)

	application, _, err := admin.CreateApplication(ctx, "sample", "Sample", "https://sample.example", "mailto:ops@sample.example")
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	_, producerToken, err := admin.CreateProducer(ctx, "sample", "sample-api")
	if err != nil {
		t.Fatalf("create producer: %v", err)
	}
	producer, err := dataStore.Producers().ByToken(ctx, producerToken)
	if err != nil {
		t.Fatalf("resolve producer: %v", err)
	}

	device, _, err := dataStore.Devices().Create(ctx, application.ID, model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformWeb,
		WebPush: &model.WebPushSubscription{Endpoint: "https://push.example/x", P256DH: "p256", Auth: "auth"},
	})
	if err != nil {
		t.Fatalf("create device: %v", err)
	}
	if err := dataStore.Devices().ReplaceSubscriptions(ctx, device.ID, []model.Subscription{{Channel: "alerts"}}); err != nil {
		t.Fatalf("subscribe: %v", err)
	}

	dispatcher := webpush.NewDispatcher(dataStore, config.WebPush{
		DispatchInterval: time.Second,
		BatchSize:        10,
		Lease:            30 * time.Second,
		MaxAttempts:      3,
	}, zerolog.Nop()).WithSender(sender)

	return fixture{db: db, store: dataStore, dispatcher: dispatcher, sender: sender, device: device, producer: producer}
}

func (f fixture) publish(t *testing.T, request model.PublishRequest) {
	t.Helper()
	if _, _, _, err := f.store.Messages().Publish(context.Background(), f.producer, request); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func (f fixture) state(t *testing.T) (string, int) {
	t.Helper()

	jobs, err := f.store.Deliveries().Pending(context.Background(), f.device.ID, 0, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(jobs) == 0 {
		return "", 0
	}
	return "pending", len(jobs)
}

func alert(key string) model.PublishRequest {
	return model.PublishRequest{
		IdempotencyKey: key,
		Channel:        "alerts",
		Priority:       model.PriorityHigh,
		Title:          "M5.1 earthquake",
		Body:           "Depth 10 km",
		ExpiresAt:      time.Now().Add(10 * time.Minute),
	}
}

func TestDispatchSendsAndMarksSent(t *testing.T) {
	sender := &recorder{status: http.StatusCreated}
	f := newFixture(t, sender, nil)
	f.publish(t, alert("key-1"))

	if err := f.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("sends: got %d want 1", sender.calls)
	}

	var event model.StreamEvent
	if err := json.Unmarshal(sender.payload, &event); err != nil {
		t.Fatalf("payload is not a stream event: %v", err)
	}
	if event.Message.Title != "M5.1 earthquake" || event.Sequence != 1 {
		t.Fatalf("unexpected payload: %+v", event)
	}
	var raw struct {
		Message map[string]any `json:"message"`
	}
	if err := json.Unmarshal(sender.payload, &raw); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"application_id", "producer_id", "idempotency_key"} {
		if _, present := raw.Message[field]; present {
			t.Fatalf("payload exposes %s to the device", field)
		}
	}

	// Accepted by the push service is "sent", never "acknowledged": the device
	// still has to confirm, so the delivery must still replay.
	if state, count := f.state(t); state != "pending" || count != 1 {
		t.Fatalf("delivery left as %q x%d, want it still replayable", state, count)
	}
}

// webpush-go prepends mailto: to any non-https subject, so the stored subject
// must be handed over without one.
func TestSubscriberIsNotDoublePrefixed(t *testing.T) {
	sender := &recorder{status: http.StatusCreated}
	f := newFixture(t, sender, nil)
	f.publish(t, alert("key-1"))

	if err := f.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.options.Subscriber != "ops@sample.example" {
		t.Fatalf("subscriber: got %q want %q", sender.options.Subscriber, "ops@sample.example")
	}
}

// The TTL must come from the database's view of expiry, and stay inside what
// the push services accept.
func TestTTLIsBoundedByExpiryAndByTheService(t *testing.T) {
	sender := &recorder{status: http.StatusCreated}
	f := newFixture(t, sender, nil)

	request := alert("key-1")
	request.ExpiresAt = time.Now().Add(10 * time.Minute)
	f.publish(t, request)

	if err := f.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.options.TTL <= 0 || sender.options.TTL > 600 {
		t.Fatalf("ttl: got %d want (0, 600]", sender.options.TTL)
	}

	long := newFixture(t, sender, nil)
	far := alert("key-2")
	far.ExpiresAt = time.Now().Add(90 * 24 * time.Hour)
	long.publish(t, far)

	if err := long.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.options.TTL > 28*24*60*60 {
		t.Fatalf("ttl: got %d, above what push services accept", sender.options.TTL)
	}
}

func TestCollapseKeyBecomesTopicOnlyWhenItFits(t *testing.T) {
	sender := &recorder{status: http.StatusCreated}
	f := newFixture(t, sender, nil)

	request := alert("key-1")
	request.CollapseKey = "earthquake-8f21"
	f.publish(t, request)

	if err := f.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.options.Topic != "earthquake-8f21" {
		t.Fatalf("topic: got %q", sender.options.Topic)
	}

	oversized := newFixture(t, sender, nil)
	request = alert("key-2")
	request.CollapseKey = strings.Repeat("a", 40)
	oversized.publish(t, request)

	if err := oversized.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	// Dropped, not rejected: collapsing is an optimisation and the notification
	// still has to go out.
	if sender.options.Topic != "" {
		t.Fatalf("topic: got %q want empty", sender.options.Topic)
	}
}

func TestTransportFailureRetriesUntilAttemptsAreSpent(t *testing.T) {
	sender := &recorder{err: errors.New("connection refused")}
	f := newFixture(t, sender, nil)
	f.publish(t, alert("key-1"))

	ctx := context.Background()
	delivery := deliveryID(t, f)
	for attempt := 1; attempt <= 3; attempt++ {
		if err := f.dispatcher.Dispatch(ctx); err != nil {
			t.Fatalf("dispatch %d: %v", attempt, err)
		}
		// Make the lease due again so the next pass claims the same delivery.
		// Retry only moves a pending row, so once the attempts are spent this
		// becomes a no-op and the loop stops finding work.
		if err := f.store.Deliveries().Retry(ctx, delivery, 0, "test requeue"); err != nil {
			t.Fatalf("requeue: %v", err)
		}
	}

	if sender.calls != 3 {
		t.Fatalf("sends: got %d want 3", sender.calls)
	}
	if state, _ := f.state(t); state == "pending" {
		t.Fatal("delivery is still pending after its attempts were spent")
	}
	var state, reason string
	if err := f.db.QueryRow(`SELECT state, last_error FROM deliveries`).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || reason != "Web Push transport error" {
		t.Fatalf("exhausted retry details: state=%q reason=%q", state, reason)
	}
}

func TestGoneDisablesTheSubscriptionButKeepsTheDevice(t *testing.T) {
	sender := &recorder{status: http.StatusGone}
	f := newFixture(t, sender, nil)
	f.publish(t, alert("key-1"))

	ctx := context.Background()
	if err := f.dispatcher.Dispatch(ctx); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	sequence, err := f.store.Devices().AcknowledgedSequence(ctx, f.device.ID)
	if err != nil {
		t.Fatalf("device was deleted along with its history: %v", err)
	}
	if sequence != 0 {
		t.Fatalf("acknowledged sequence: got %d want 0", sequence)
	}

	jobs, err := f.store.Deliveries().ClaimWebPush(ctx, 10, time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("a disabled subscription was claimed again: %d jobs", len(jobs))
	}
}

func TestPermanentRejectionFailsImmediately(t *testing.T) {
	sender := &recorder{status: http.StatusBadRequest}
	f := newFixture(t, sender, nil)
	f.publish(t, alert("key-1"))

	if err := f.dispatcher.Dispatch(context.Background()); err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	if sender.calls != 1 {
		t.Fatalf("sends: got %d want 1", sender.calls)
	}
	if state, _ := f.state(t); state == "pending" {
		t.Fatal("a permanently rejected delivery is still queued")
	}
	var state, reason string
	if err := f.db.QueryRow(`SELECT state, last_error FROM deliveries`).Scan(&state, &reason); err != nil {
		t.Fatal(err)
	}
	if state != "failed" || reason != "Web Push HTTP 400" {
		t.Fatalf("rejection details: state=%q reason=%q", state, reason)
	}
}

// A key this process cannot read must retire its own delivery. Left alone it is
// re-claimed every lease forever and starves the rest of the batch.
func TestUnreadableKeyFailsOnlyItsOwnDelivery(t *testing.T) {
	sender := &recorder{status: http.StatusCreated}

	writeKey, _ := crypto.NewEncryptionKey()
	writeCipher, err := crypto.NewCipher(writeKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	f := newFixture(t, sender, writeCipher)
	f.publish(t, alert("key-1"))

	// Swap in a cipher that cannot read what was written.
	otherKey, _ := crypto.NewEncryptionKey()
	otherCipher, err := crypto.NewCipher(otherKey)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	broken := postgres.New(f.db, otherCipher)
	dispatcher := webpush.NewDispatcher(broken, config.WebPush{
		DispatchInterval: time.Second, BatchSize: 10, Lease: 30 * time.Second, MaxAttempts: 3,
	}, zerolog.Nop()).WithSender(sender)

	ctx := context.Background()
	if err := dispatcher.Dispatch(ctx); err != nil {
		t.Fatalf("dispatch returned an error instead of failing one delivery: %v", err)
	}
	if sender.calls != 0 {
		t.Fatalf("sends: got %d want 0", sender.calls)
	}

	jobs, err := broken.Deliveries().ClaimWebPush(ctx, 10, time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("the undecryptable delivery is still queued: %d jobs", len(jobs))
	}
}

func deliveryID(t *testing.T, f fixture) string {
	t.Helper()

	events, err := f.store.Deliveries().Pending(context.Background(), f.device.ID, 0, 1)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(events) == 0 {
		t.Fatal("no pending delivery")
	}
	return events[0].DeliveryID
}
