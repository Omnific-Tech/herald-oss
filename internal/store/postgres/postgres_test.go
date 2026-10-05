package postgres_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
	"github.com/Omnific-Tech/herald/internal/testsupport"
)

// newStore returns a migrated, empty database private to this suite.
func newStore(t *testing.T) (*postgres.Store, *sql.DB) {
	t.Helper()

	db := testsupport.Database(t, "store")

	key, err := crypto.NewEncryptionKey()
	if err != nil {
		t.Fatalf("encryption key: %v", err)
	}
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	return postgres.New(db, cipher), db
}

type fixture struct {
	application model.Application
	producer    model.Producer
	android     model.Device
	web         model.Device
	webToken    string
}

// seed provisions one application, one producer, and two devices subscribed to
// alerts: an Android device on the stream and a web device on Web Push.
func seed(t *testing.T, dataStore *postgres.Store) fixture {
	t.Helper()
	ctx := context.Background()
	admin := postgres.NewAdmin(dataStore)

	application, _, err := admin.CreateApplication(ctx, "sample", "Sample", "https://sample.example", "mailto:ops@sample.example")
	if err != nil {
		t.Fatalf("create application: %v", err)
	}
	for _, topic := range []string{"earthquake.jakarta", "flood.bandung", "volcano.merapi"} {
		if _, err := dataStore.Routing().CreateTopic(ctx, "sample", model.RoutingTopic{ID: topic, Label: topic, Channel: "alerts"}); err != nil {
			t.Fatalf("create topic %s: %v", topic, err)
		}
	}

	producer, producerToken, err := admin.CreateProducer(ctx, "sample", "sample-api")
	if err != nil {
		t.Fatalf("create producer: %v", err)
	}
	resolved, err := dataStore.Producers().ByToken(ctx, producerToken)
	if err != nil {
		t.Fatalf("resolve producer: %v", err)
	}
	if resolved.ID != producer.ID || resolved.ApplicationID != application.ID {
		t.Fatal("producer token resolved to the wrong producer")
	}

	android, _, err := dataStore.Devices().Create(ctx, application.ID, model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformAndroid, FCMFID: "test-fid",
	})
	if err != nil {
		t.Fatalf("register android device: %v", err)
	}

	web, webToken, err := dataStore.Devices().Create(ctx, application.ID, model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformWeb,
		WebPush: &model.WebPushSubscription{Endpoint: "https://push.example/x", P256DH: "p256", Auth: "auth"},
	})
	if err != nil {
		t.Fatalf("register web device: %v", err)
	}

	for _, device := range []model.Device{android, web} {
		if err := dataStore.Devices().ReplaceSubscriptions(ctx, device.ID, []model.Subscription{
			{Channel: "alerts", Topic: "earthquake.jakarta"},
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}

	return fixture{application: application, producer: resolved, android: android, web: web, webToken: webToken}
}

func publishRequest(key string) model.PublishRequest {
	return model.PublishRequest{
		IdempotencyKey: key,
		Channel:        "alerts",
		Topic:          "earthquake.jakarta",
		Priority:       model.PriorityHigh,
		Title:          "M5.1 earthquake",
		Body:           "Depth 10 km",
		Link:           "/events/8f21",
		Data:           json.RawMessage(`{"event_id":"8f21"}`),
		ExpiresAt:      time.Now().Add(10 * time.Minute),
	}
}

func TestPublishFansOutToSubscribedDevices(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	_, deduplicated, deviceCount, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if deduplicated {
		t.Fatal("first publish reported as deduplicated")
	}
	if deviceCount != 2 {
		t.Fatalf("device count: got %d want 2", deviceCount)
	}

	events, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("pending deliveries: got %d want 1", len(events))
	}
	if events[0].Sequence != 1 {
		t.Fatalf("sequence: got %d want 1", events[0].Sequence)
	}
	if events[0].Message.Link != "/events/8f21" {
		t.Fatalf("link: got %q", events[0].Message.Link)
	}
}

// A device subscribed to a different topic must not receive the message.
func TestPublishSkipsUnsubscribedDevices(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if err := dataStore.Devices().ReplaceSubscriptions(ctx, data.web.ID, []model.Subscription{
		{Channel: "alerts", Topic: "flood.bandung"},
	}); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}

	_, _, deviceCount, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if deviceCount != 1 {
		t.Fatalf("device count: got %d want 1", deviceCount)
	}
}

// An empty topic is a whole-channel subscription.
func TestChannelWideSubscriptionMatchesEveryTopic(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if err := dataStore.Devices().ReplaceSubscriptions(ctx, data.android.ID, []model.Subscription{
		{Channel: "alerts"},
	}); err != nil {
		t.Fatalf("resubscribe: %v", err)
	}

	request := publishRequest("key-1")
	request.Topic = "volcano.merapi"
	_, _, deviceCount, err := dataStore.Messages().Publish(ctx, data.producer, request)
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if deviceCount != 1 {
		t.Fatalf("device count: got %d want 1", deviceCount)
	}
}

func TestPublishIsIdempotent(t *testing.T) {
	dataStore, db := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	first, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}

	second, deduplicated, deviceCount, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1"))
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if !deduplicated {
		t.Fatal("replayed idempotency key was not reported as deduplicated")
	}
	if second.ID != first.ID {
		t.Fatalf("message id: got %s want %s", second.ID, first.ID)
	}
	if deviceCount != 2 {
		t.Fatalf("device count: got %d want 2", deviceCount)
	}

	var deliveries int
	if err := db.QueryRow(`SELECT count(*) FROM deliveries`).Scan(&deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveries != 2 {
		t.Fatalf("replay created extra deliveries: got %d want 2", deliveries)
	}
}

func TestSequencesAreMonotonicPerDevice(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	for _, key := range []string{"key-1", "key-2", "key-3"} {
		if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest(key)); err != nil {
			t.Fatalf("publish %s: %v", key, err)
		}
	}

	events, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(events) != 3 {
		t.Fatalf("pending: got %d want 3", len(events))
	}
	for index, event := range events {
		if event.Sequence != int64(index+1) {
			t.Fatalf("sequence %d: got %d", index, event.Sequence)
		}
	}
}

// Receiving is not acknowledging: a sent-but-unacked delivery replays.
func TestReplayReturnsSentButUnacknowledgedDeliveries(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	events, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if err := dataStore.Deliveries().MarkSent(ctx, events[0].DeliveryID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	replayed, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replayed) != 1 {
		t.Fatalf("a sent but unacknowledged delivery was dropped: got %d want 1", len(replayed))
	}
}

func TestAcknowledgementRemovesDeliveryFromReplay(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	events, _ := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)

	if _, err := dataStore.Deliveries().Acknowledge(ctx, data.android.ID, events[0].DeliveryID, model.AckDisplayed); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	replayed, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if err != nil {
		t.Fatalf("replay: %v", err)
	}
	if len(replayed) != 0 {
		t.Fatalf("acknowledged delivery replayed: got %d want 0", len(replayed))
	}
}

func TestAcknowledgeIsIdempotent(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	events, _ := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)

	first, err := dataStore.Deliveries().Acknowledge(ctx, data.android.ID, events[0].DeliveryID, model.AckDisplayed)
	if err != nil {
		t.Fatalf("first ack: %v", err)
	}

	second, err := dataStore.Deliveries().Acknowledge(ctx, data.android.ID, events[0].DeliveryID, model.AckDelivered)
	if err != nil {
		t.Fatalf("repeat ack returned an error: %v", err)
	}
	if second.Status != first.Status || !second.AcknowledgedAt.Equal(first.AcknowledgedAt) {
		t.Fatalf("repeat ack overwrote the record: got %+v want %+v", second, first)
	}
}

func TestAcknowledgeRejectsAnotherDevicesDelivery(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	events, _ := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)

	_, err := dataStore.Deliveries().Acknowledge(ctx, data.web.ID, events[0].DeliveryID, model.AckDelivered)
	if err == nil {
		t.Fatal("a device acknowledged another device's delivery")
	}
}

func TestExpiredMessagesAreNotReplayed(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	request := publishRequest("key-1")
	request.ExpiresAt = time.Now().Add(300 * time.Millisecond)
	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, request); err != nil {
		t.Fatalf("publish: %v", err)
	}

	deadline := time.Now().Add(5 * time.Second)
	for {
		if time.Now().After(deadline) {
			t.Fatal("expired message was still replayed")
		}
		time.Sleep(100 * time.Millisecond)

		events, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
		if err != nil {
			t.Fatalf("pending: %v", err)
		}
		if len(events) == 0 {
			break
		}
	}

	expired, err := dataStore.Deliveries().ExpireOverdue(ctx)
	if err != nil {
		t.Fatalf("expire: %v", err)
	}
	if expired != 2 {
		t.Fatalf("expired deliveries: got %d want 2", expired)
	}

	// The sweep is bookkeeping, so re-running it must settle at zero.
	again, err := dataStore.Deliveries().ExpireOverdue(ctx)
	if err != nil {
		t.Fatalf("second expire: %v", err)
	}
	if again != 0 {
		t.Fatalf("second sweep expired %d deliveries, want 0", again)
	}
}

func TestCollapseKeySupersedesUndeliveredMessages(t *testing.T) {
	dataStore, db := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	first := publishRequest("key-1")
	first.CollapseKey = "earthquake-8f21"
	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, first); err != nil {
		t.Fatalf("publish first: %v", err)
	}

	second := publishRequest("key-2")
	second.CollapseKey = "earthquake-8f21"
	survivor, _, _, err := dataStore.Messages().Publish(ctx, data.producer, second)
	if err != nil {
		t.Fatalf("publish second: %v", err)
	}

	events, err := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if err != nil {
		t.Fatalf("pending: %v", err)
	}
	if len(events) != 1 {
		t.Fatalf("collapse left %d pending deliveries, want 1", len(events))
	}
	if events[0].Message.ID != survivor.ID {
		t.Fatalf("the surviving delivery is %q, want the second message %q", events[0].Message.ID, survivor.ID)
	}

	var superseded int
	if err := db.QueryRow(`SELECT count(*) FROM deliveries WHERE state = 'superseded'`).Scan(&superseded); err != nil {
		t.Fatalf("count superseded: %v", err)
	}
	if superseded != 2 {
		t.Fatalf("superseded deliveries: got %d want 2", superseded)
	}
}

func TestClaimWebPushLeasesOnlyWebDevices(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	jobs, err := dataStore.Deliveries().ClaimWebPush(ctx, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(jobs) != 1 {
		t.Fatalf("claimed jobs: got %d want 1", len(jobs))
	}
	if jobs[0].DeviceID != data.web.ID {
		t.Fatal("claimed a job for a device that is not on web push")
	}
	if jobs[0].Endpoint != "https://push.example/x" {
		t.Fatalf("endpoint: got %q", jobs[0].Endpoint)
	}
	if jobs[0].VAPIDPrivateKey == "" {
		t.Fatal("vapid private key was not decrypted")
	}
	if jobs[0].Attempts != 1 {
		t.Fatalf("attempts: got %d want 1", jobs[0].Attempts)
	}

	// The lease pushed next_attempt_at forward, so an immediate second pass
	// must claim nothing.
	again, err := dataStore.Deliveries().ClaimWebPush(ctx, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("second claim: %v", err)
	}
	if len(again) != 0 {
		t.Fatalf("leased delivery was claimed twice: got %d want 0", len(again))
	}
}

// Rotation keeps the sequence so a reinstall does not replay its history.
func TestRotateKeepsSequenceAndIssuesNewToken(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	device, token, err := dataStore.Devices().Rotate(ctx, data.android.ID, model.RegisterDeviceRequest{
		Platform: model.PlatformAndroid, FCMFID: "rotated-fid",
	})
	if err != nil {
		t.Fatalf("rotate: %v", err)
	}
	if device.ID != data.android.ID {
		t.Fatal("rotation created a second device")
	}
	if device.LastSequence != 1 {
		t.Fatalf("last sequence: got %d want 1", device.LastSequence)
	}

	resolved, err := dataStore.Devices().ByToken(ctx, token)
	if err != nil {
		t.Fatalf("resolve rotated token: %v", err)
	}
	if resolved.ID != device.ID {
		t.Fatal("rotated token resolved to the wrong device")
	}
}

func TestUnknownTokensAreNotFound(t *testing.T) {
	dataStore, _ := newStore(t)
	seed(t, dataStore)
	ctx := context.Background()

	if _, err := dataStore.Producers().ByToken(ctx, "nonsense"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("producer: got %v want ErrNotFound", err)
	}
	if _, err := dataStore.Devices().ByToken(ctx, "nonsense"); !errors.Is(err, store.ErrNotFound) {
		t.Fatalf("device: got %v want ErrNotFound", err)
	}
}

// Concurrent publishes to overlapping device sets must not deadlock. The
// ordered lock in lockMatchingDevices is the only thing preventing it, and
// nothing else in the suite would notice its removal.
func TestConcurrentPublishesDoNotDeadlock(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	const devices = 8
	for index := 0; index < devices; index++ {
		device, _, err := dataStore.Devices().Create(ctx, data.application.ID, model.RegisterDeviceRequest{
			Application: "sample", Platform: model.PlatformAndroid, FCMFID: fmt.Sprintf("concurrent-fid-%d", index),
		})
		if err != nil {
			t.Fatalf("create device: %v", err)
		}
		if err := dataStore.Devices().ReplaceSubscriptions(ctx, device.ID, []model.Subscription{
			{Channel: "alerts", Topic: "earthquake.jakarta"},
		}); err != nil {
			t.Fatalf("subscribe: %v", err)
		}
	}

	const publishers = 8
	var (
		group  sync.WaitGroup
		errors = make(chan error, publishers)
	)
	for index := 0; index < publishers; index++ {
		group.Add(1)
		go func(index int) {
			defer group.Done()
			_, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest(fmt.Sprintf("key-%d", index)))
			errors <- err
		}(index)
	}
	group.Wait()
	close(errors)

	for err := range errors {
		if err != nil {
			t.Fatalf("concurrent publish: %v", err)
		}
	}
}

// device_count must mean the same thing on a first publish and on a replay,
// including after a device has gone away.
func TestDeviceCountSurvivesDeviceRemoval(t *testing.T) {
	dataStore, db := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	_, _, first, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1"))
	if err != nil {
		t.Fatalf("publish: %v", err)
	}
	if first != 2 {
		t.Fatalf("device count: got %d want 2", first)
	}

	if _, err := db.Exec(`DELETE FROM devices WHERE id = $1`, data.web.ID); err != nil {
		t.Fatalf("delete device: %v", err)
	}

	_, deduplicated, replayed, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1"))
	if err != nil {
		t.Fatalf("republish: %v", err)
	}
	if !deduplicated {
		t.Fatal("replay was not reported as deduplicated")
	}
	if replayed != first {
		t.Fatalf("replayed device count: got %d want %d", replayed, first)
	}
}

// A gone subscription must cost the device its push endpoint, not its history.
func TestDisableWebPushKeepsTheDevice(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	if err := dataStore.Devices().DisableWebPush(ctx, data.web.ID); err != nil {
		t.Fatalf("disable: %v", err)
	}

	device, err := dataStore.Devices().ByToken(ctx, data.webToken)
	if err != nil {
		t.Fatalf("device survived? %v", err)
	}
	if device.LastSequence != 1 {
		t.Fatalf("last sequence: got %d want 1", device.LastSequence)
	}

	jobs, err := dataStore.Deliveries().ClaimWebPush(ctx, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim: %v", err)
	}
	if len(jobs) != 0 {
		t.Fatalf("a disabled device was still claimed: got %d jobs", len(jobs))
	}
}

// Both transports are queued, and each dispatcher claims only its platform.
func TestPushQueuesStaySeparatedByPlatform(t *testing.T) {
	dataStore, db := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}

	var queued int
	if err := db.QueryRow(`SELECT count(*) FROM deliveries WHERE next_attempt_at IS NOT NULL`).Scan(&queued); err != nil {
		t.Fatalf("count queued: %v", err)
	}
	if queued != 2 {
		t.Fatalf("queued deliveries: got %d want 2", queued)
	}

	fcmJobs, err := dataStore.Deliveries().ClaimFCM(ctx, 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim fcm: %v", err)
	}
	if len(fcmJobs) != 1 || fcmJobs[0].DeviceID != data.android.ID || fcmJobs[0].FCMFID != "test-fid" {
		t.Fatalf("unexpected fcm jobs: %+v", fcmJobs)
	}
}

// AcknowledgedSequence, not last_sequence, is what a client may resume from.
func TestAcknowledgedSequenceTrailsAllocation(t *testing.T) {
	dataStore, _ := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	for _, key := range []string{"key-1", "key-2"} {
		if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest(key)); err != nil {
			t.Fatalf("publish %s: %v", key, err)
		}
	}

	acknowledged, err := dataStore.Devices().AcknowledgedSequence(ctx, data.android.ID)
	if err != nil {
		t.Fatalf("acknowledged sequence: %v", err)
	}
	if acknowledged != 0 {
		t.Fatalf("acknowledged sequence before any ack: got %d want 0", acknowledged)
	}

	events, _ := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)
	if _, err := dataStore.Deliveries().Acknowledge(ctx, data.android.ID, events[0].DeliveryID, model.AckDisplayed); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}

	acknowledged, err = dataStore.Devices().AcknowledgedSequence(ctx, data.android.ID)
	if err != nil {
		t.Fatalf("acknowledged sequence: %v", err)
	}
	if acknowledged != 1 {
		t.Fatalf("acknowledged sequence: got %d want 1", acknowledged)
	}
}

// An ack that lands while the dispatcher is mid-send must win.
func TestFailDoesNotClobberAnAcknowledgedDelivery(t *testing.T) {
	dataStore, db := newStore(t)
	data := seed(t, dataStore)
	ctx := context.Background()

	if _, _, _, err := dataStore.Messages().Publish(ctx, data.producer, publishRequest("key-1")); err != nil {
		t.Fatalf("publish: %v", err)
	}
	events, _ := dataStore.Deliveries().Pending(ctx, data.android.ID, 0, 10)

	if _, err := dataStore.Deliveries().Acknowledge(ctx, data.android.ID, events[0].DeliveryID, model.AckDisplayed); err != nil {
		t.Fatalf("acknowledge: %v", err)
	}
	if err := dataStore.Deliveries().Fail(ctx, events[0].DeliveryID, "test failure"); err != nil {
		t.Fatalf("fail: %v", err)
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM deliveries WHERE id = $1`, events[0].DeliveryID).Scan(&state); err != nil {
		t.Fatalf("read state: %v", err)
	}
	if state != "acknowledged" {
		t.Fatalf("state: got %q want acknowledged", state)
	}
}

// Migrate records what it applied, so a second run is a no-op.
func TestMigrateIsRerunnable(t *testing.T) {
	_, db := newStore(t)

	applied, err := postgres.Migrate(context.Background(), db)
	if err != nil {
		t.Fatalf("re-migrate: %v", err)
	}
	if len(applied) != 0 {
		t.Fatalf("re-running applied %v, want nothing", applied)
	}
}
