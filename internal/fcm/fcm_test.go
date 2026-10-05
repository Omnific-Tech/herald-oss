package fcm_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/fcm"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
	"github.com/Omnific-Tech/herald/internal/testsupport"
	"github.com/rs/zerolog"
)

type recorder struct {
	status  int
	body    []byte
	payload []byte
}

func (r *recorder) Send(_ context.Context, payload []byte) (int, []byte, error) {
	r.payload = append([]byte(nil), payload...)
	return r.status, r.body, nil
}

func TestDispatchSendsHighPriorityDeliveryToFID(t *testing.T) {
	db := testsupport.Database(t, "fcm")
	ctx := context.Background()
	key, _ := crypto.NewEncryptionKey()
	cipher, _ := crypto.NewCipher(key)
	dataStore := postgres.New(db, cipher)
	admin := postgres.NewAdmin(dataStore)
	application, _, err := admin.CreateApplication(ctx, "sample", "Sample", "https://sample.example", "mailto:ops@sample.example")
	if err != nil {
		t.Fatal(err)
	}
	_, producerToken, err := admin.CreateProducer(ctx, "sample", "sample-api")
	if err != nil {
		t.Fatal(err)
	}
	producer, err := dataStore.Producers().ByToken(ctx, producerToken)
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := dataStore.Devices().Create(ctx, application.ID, model.RegisterDeviceRequest{
		Platform: model.PlatformAndroid, FCMFID: "firebase-installation-id",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Devices().ReplaceSubscriptions(ctx, device.ID, []model.Subscription{{Channel: "alerts"}}); err != nil {
		t.Fatal(err)
	}
	_, _, _, err = dataStore.Messages().Publish(ctx, producer, model.PublishRequest{
		IdempotencyKey: "quake-1", Channel: "alerts", Priority: model.PriorityHigh,
		Title: "Earthquake", Body: "Take cover", Link: "/events/1",
		ExpiresAt: time.Now().Add(5 * time.Minute),
	})
	if err != nil {
		t.Fatal(err)
	}

	sender := &recorder{status: http.StatusOK}
	dispatcher := fcm.NewDispatcher(dataStore, config.FCM{
		BatchSize: 10, Lease: 30 * time.Second, MaxAttempts: 3,
	}, sender, zerolog.Nop())
	if err := dispatcher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}

	var request struct {
		Message struct {
			FID     string            `json:"fid"`
			Data    map[string]string `json:"data"`
			Android struct {
				Priority string `json:"priority"`
				TTL      string `json:"ttl"`
			} `json:"android"`
		} `json:"message"`
	}
	if err := json.Unmarshal(sender.payload, &request); err != nil {
		t.Fatalf("decode payload: %v", err)
	}
	if request.Message.FID != "firebase-installation-id" || request.Message.Android.Priority != "high" {
		t.Fatalf("unexpected fcm request: %+v", request)
	}
	if !regexp.MustCompile(`^[0-9]+s$`).MatchString(request.Message.Android.TTL) {
		t.Fatalf("FCM ttl must use seconds format, got %q", request.Message.Android.TTL)
	}
	var delivery struct {
		Message map[string]any `json:"message"`
	}
	if err := json.Unmarshal([]byte(request.Message.Data["herald_delivery"]), &delivery); err != nil {
		t.Fatalf("decode delivery payload: %v", err)
	}
	if delivery.Message["title"] == nil {
		t.Fatalf("delivery payload has no message: %s", request.Message.Data["herald_delivery"])
	}
	for _, field := range []string{"application_id", "producer_id", "idempotency_key"} {
		if _, present := delivery.Message[field]; present {
			t.Fatalf("payload exposes %s to the device", field)
		}
	}

	var state string
	if err := db.QueryRow(`SELECT state FROM deliveries`).Scan(&state); err != nil {
		t.Fatal(err)
	}
	if state != "sent" {
		t.Fatalf("state: got %q want sent", state)
	}
}

func TestDispatchErrorVisibilityEndToEnd(t *testing.T) {
	db := testsupport.Database(t, "fcm_errors")
	ctx := context.Background()
	key, _ := crypto.NewEncryptionKey()
	cipher, _ := crypto.NewCipher(key)
	dataStore := postgres.New(db, cipher)
	application, _, err := postgres.NewAdmin(dataStore).CreateApplication(ctx, "sample", "Sample", "https://sample.example", "mailto:ops@sample.example")
	if err != nil {
		t.Fatal(err)
	}
	device, _, err := dataStore.Devices().Create(ctx, application.ID, model.RegisterDeviceRequest{Platform: model.PlatformAndroid, FCMFID: "firebase-installation-id"})
	if err != nil {
		t.Fatal(err)
	}
	if err := dataStore.Devices().ReplaceSubscriptions(ctx, device.ID, []model.Subscription{{Channel: "alerts"}}); err != nil {
		t.Fatal(err)
	}
	publish := func(key string) string {
		t.Helper()
		message, _, _, err := dataStore.Messages().PublishAsAdmin(ctx, application, "operator", model.PublishRequest{
			IdempotencyKey: key, Channel: "alerts", Title: "Earthquake", Body: "Take cover", ExpiresAt: time.Now().Add(5 * time.Minute),
		})
		if err != nil {
			t.Fatal(err)
		}
		return message.ID
	}
	read := func(messageID string) (string, sql.NullString) {
		t.Helper()
		var state string
		var reason sql.NullString
		if err := db.QueryRow(`SELECT state, last_error FROM deliveries WHERE message_id = $1`, messageID).Scan(&state, &reason); err != nil {
			t.Fatal(err)
		}
		return state, reason
	}
	sender := &recorder{status: http.StatusBadRequest, body: []byte(`{"error":{"message":"Invalid value at message.android.ttl"}}`)}
	dispatcher := fcm.NewDispatcher(dataStore, config.FCM{BatchSize: 10, Lease: 30 * time.Second, MaxAttempts: 3}, sender, zerolog.Nop())
	badID := publish("bad-fcm")
	if err := dispatcher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if state, reason := read(badID); state != "failed" || !reason.Valid || !strings.Contains(reason.String, "FCM 400: Invalid value at message.android.ttl") {
		t.Fatalf("permanent rejection: state=%q reason=%+v", state, reason)
	}

	retryID := publish("retry-fcm")
	sender.status = http.StatusServiceUnavailable
	sender.body = []byte(`{"error":{"message":"FCM temporarily unavailable"}}`)
	if err := dispatcher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if state, reason := read(retryID); state != "pending" || !reason.Valid || !strings.Contains(reason.String, "FCM 503: FCM temporarily unavailable") {
		t.Fatalf("retrying delivery: state=%q reason=%+v", state, reason)
	}
	if _, err := db.Exec(`UPDATE deliveries SET next_attempt_at = now() WHERE message_id = $1`, retryID); err != nil {
		t.Fatal(err)
	}
	sender.status = http.StatusOK
	sender.body = nil
	if err := dispatcher.Dispatch(ctx); err != nil {
		t.Fatal(err)
	}
	if state, reason := read(retryID); state != "sent" || reason.Valid {
		t.Fatalf("recovered delivery: state=%q reason=%+v", state, reason)
	}
}
