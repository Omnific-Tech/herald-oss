package server_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/server"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
	"github.com/Omnific-Tech/herald/internal/testsupport"
	"github.com/rs/zerolog"
)

// harness is a live Herald over a real database.
type harness struct {
	server        *httptest.Server
	producerToken string
	client        *http.Client
	store         *postgres.Store
	nextDevice    int
}

func newHarness(t *testing.T) *harness {
	t.Helper()

	db := testsupport.Database(t, "server")
	ctx := context.Background()

	key, _ := crypto.NewEncryptionKey()
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}

	dataStore := postgres.New(db, cipher)
	admin := postgres.NewAdmin(dataStore)
	if _, _, err := admin.CreateApplication(ctx, "sample", "Sample", "https://sample.example", "mailto:ops@sample.example"); err != nil {
		t.Fatalf("create application: %v", err)
	}
	if _, err := dataStore.Routing().CreateTopic(ctx, "sample", model.RoutingTopic{ID: "earthquake.jakarta", Label: "Jakarta earthquakes", Channel: "alerts"}); err != nil {
		t.Fatalf("create topic: %v", err)
	}
	_, producerToken, err := admin.CreateProducer(ctx, "sample", "sample-api")
	if err != nil {
		t.Fatalf("create producer: %v", err)
	}

	httpServer := httptest.NewServer(server.New(testConfig(), "test", db, dataStore, zerolog.Nop()).Handler())
	t.Cleanup(httpServer.Close)

	return &harness{server: httpServer, producerToken: producerToken, client: httpServer.Client(), store: dataStore}
}

func (h *harness) do(t *testing.T, method, path, token string, body any) (int, []byte) {
	t.Helper()

	var reader io.Reader
	if body != nil {
		encoded, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode body: %v", err)
		}
		reader = bytes.NewReader(encoded)
	}

	request, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	if token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
	}

	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()

	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read body: %v", err)
	}
	return response.StatusCode, payload
}

func (h *harness) registerAndroid(t *testing.T) model.RegisterDeviceResponse {
	t.Helper()
	h.nextDevice++

	status, body := h.do(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformAndroid, FCMFID: fmt.Sprintf("test-fid-%d", h.nextDevice),
	})
	if status != http.StatusOK {
		t.Fatalf("register: status %d body %s", status, body)
	}

	var registration model.RegisterDeviceResponse
	if err := json.Unmarshal(body, &registration); err != nil {
		t.Fatalf("decode registration: %v", err)
	}
	return registration
}

func (h *harness) subscribe(t *testing.T, token string, subscriptions []model.Subscription) {
	t.Helper()
	status, body := h.do(t, http.MethodPut, "/v1/subscriptions", token, model.UpdateSubscriptionsRequest{Subscriptions: subscriptions})
	if status != http.StatusOK {
		t.Fatalf("subscribe: status %d body %s", status, body)
	}
}

func (h *harness) publish(t *testing.T, request model.PublishRequest) model.PublishResponse {
	t.Helper()

	status, body := h.do(t, http.MethodPost, "/v1/messages", h.producerToken, request)
	if status != http.StatusAccepted {
		t.Fatalf("publish: status %d body %s", status, body)
	}

	var published model.PublishResponse
	if err := json.Unmarshal(body, &published); err != nil {
		t.Fatalf("decode publish response: %v", err)
	}
	return published
}

func (h *harness) claimFCM(t *testing.T) []store.FCMJob {
	t.Helper()
	jobs, err := h.store.Deliveries().ClaimFCM(context.Background(), 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim fcm: %v", err)
	}
	return jobs
}

func alert(key string) model.PublishRequest {
	return model.PublishRequest{
		IdempotencyKey: key,
		Channel:        "alerts",
		Topic:          "earthquake.jakarta",
		Priority:       model.PriorityHigh,
		Title:          "M5.1 earthquake",
		Body:           "Depth 10 km",
		Link:           "/events/8f21",
		ExpiresAt:      time.Now().Add(10 * time.Minute),
	}
}

// The full HTTP-to-FCM path: register, subscribe, publish, claim and acknowledge.
func TestEndToEndFCMDelivery(t *testing.T) {
	h := newHarness(t)

	device := h.registerAndroid(t)
	h.subscribe(t, device.DeviceToken, []model.Subscription{{Channel: "alerts", Topic: "earthquake.jakarta"}})

	published := h.publish(t, alert("key-1"))
	if published.DeviceCount != 1 {
		t.Fatalf("device count: got %d want 1", published.DeviceCount)
	}

	jobs := h.claimFCM(t)
	if len(jobs) != 1 {
		t.Fatalf("fcm jobs: got %d want 1", len(jobs))
	}
	if jobs[0].FCMFID != "test-fid-1" || jobs[0].Message.Title != "M5.1 earthquake" || jobs[0].Message.Link != "/events/8f21" {
		t.Fatalf("unexpected job: %+v", jobs[0])
	}
	if err := h.store.Deliveries().MarkSent(context.Background(), jobs[0].DeliveryID); err != nil {
		t.Fatalf("mark sent: %v", err)
	}

	status, body := h.do(t, http.MethodPost, "/v1/deliveries/"+jobs[0].DeliveryID+"/ack", device.DeviceToken, model.AckRequest{Status: model.AckDisplayed})
	if status != http.StatusOK {
		t.Fatalf("ack: status %d body %s", status, body)
	}
}

// Publishing the same idempotency key twice must surface one notification.
func TestDuplicatePublicationDeliversOnce(t *testing.T) {
	h := newHarness(t)

	device := h.registerAndroid(t)
	h.subscribe(t, device.DeviceToken, []model.Subscription{{Channel: "alerts"}})

	first := h.publish(t, alert("key-1"))
	second := h.publish(t, alert("key-1"))

	if !second.Deduplicated {
		t.Fatal("replayed publication was not reported as deduplicated")
	}
	if second.MessageID != first.MessageID {
		t.Fatal("replayed publication created a second message")
	}

	jobs := h.claimFCM(t)
	if len(jobs) != 1 {
		t.Fatalf("duplicate publication produced %d deliveries, want 1", len(jobs))
	}
}

// An expired message is recorded but never dispatched.
func TestExpiredMessageIsNotDispatched(t *testing.T) {
	h := newHarness(t)

	device := h.registerAndroid(t)
	h.subscribe(t, device.DeviceToken, []model.Subscription{{Channel: "alerts"}})

	request := alert("key-1")
	request.ExpiresAt = time.Now().Add(400 * time.Millisecond)
	h.publish(t, request)

	time.Sleep(700 * time.Millisecond)

	jobs := h.claimFCM(t)
	if len(jobs) != 0 {
		t.Fatalf("an expired message was dispatched: %+v", jobs)
	}
}

// A producer's token binds it to one application, so another application's
// devices must never receive its messages.
func TestProducerCannotReachAnotherApplication(t *testing.T) {
	h := newHarness(t)

	status, body := h.do(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{
		Application: "other", Platform: model.PlatformAndroid, FCMFID: "other-fid",
	})
	if status != http.StatusNotFound {
		t.Fatalf("registering against an unknown application: status %d body %s", status, body)
	}

	device := h.registerAndroid(t)
	h.subscribe(t, device.DeviceToken, []model.Subscription{{Channel: "alerts"}})

	// There is no application field to forge, and the token decides the target.
	published := h.publish(t, alert("key-1"))
	if published.DeviceCount != 1 {
		t.Fatalf("device count: got %d want 1", published.DeviceCount)
	}
}

// A producer must not be able to point a device at an untrusted origin.
// Checking only the first character misses the forms browsers resolve
// off-origin, which is what this endpoint previously did.
func TestUntrustedLinkIsRejected(t *testing.T) {
	h := newHarness(t)

	for index, link := range []string{
		"https://evil.example/steal",
		"//evil.example/steal",
		"//evil.example@sample.example/steal",
		`/\evil.example/steal`,
		"javascript:alert(1)",
	} {
		request := alert(fmt.Sprintf("key-%d", index))
		request.Link = link

		status, body := h.do(t, http.MethodPost, "/v1/messages", h.producerToken, request)
		if status != http.StatusBadRequest {
			t.Errorf("link %q: status %d want 400, body %s", link, status, body)
		}
	}
}

func TestInvalidCredentialsAreRejected(t *testing.T) {
	h := newHarness(t)

	if status, _ := h.do(t, http.MethodPost, "/v1/messages", "nonsense", alert("key-1")); status != http.StatusUnauthorized {
		t.Fatalf("producer token: got %d want 401", status)
	}
	if status, _ := h.do(t, http.MethodPut, "/v1/subscriptions", "nonsense", model.UpdateSubscriptionsRequest{}); status != http.StatusUnauthorized {
		t.Fatalf("device token: got %d want 401", status)
	}
}

// A device must not be able to acknowledge another device's delivery.
func TestAcknowledgingAnotherDevicesDeliveryIsNotFound(t *testing.T) {
	h := newHarness(t)

	first := h.registerAndroid(t)
	second := h.registerAndroid(t)
	h.subscribe(t, first.DeviceToken, []model.Subscription{{Channel: "alerts"}})

	h.publish(t, alert("key-1"))
	jobs := h.claimFCM(t)
	if len(jobs) != 1 {
		t.Fatalf("fcm jobs: got %d want 1", len(jobs))
	}

	status, _ := h.do(t, http.MethodPost, "/v1/deliveries/"+jobs[0].DeliveryID+"/ack", second.DeviceToken, model.AckRequest{Status: model.AckDelivered})
	if status != http.StatusNotFound {
		t.Fatalf("cross-device ack: got %d want 404", status)
	}
}

// Web platform registration is what supplies the Web Push subscription, and the
// PWA needs the application's public key before it can create one.
func TestWebRegistrationRequiresSubscriptionAndExposesVAPIDKey(t *testing.T) {
	h := newHarness(t)

	status, body := h.do(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformWeb,
	})
	if status != http.StatusBadRequest {
		t.Fatalf("web registration without keys: status %d body %s", status, body)
	}

	status, body = h.do(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformWeb,
		WebPush: &model.WebPushSubscription{Endpoint: "https://push.example/x", P256DH: "p256", Auth: "auth"},
	})
	if status != http.StatusOK {
		t.Fatalf("web registration: status %d body %s", status, body)
	}

	status, body = h.do(t, http.MethodGet, "/v1/applications/sample/vapid-public-key", "", nil)
	if status != http.StatusOK {
		t.Fatalf("vapid key: status %d body %s", status, body)
	}

	var key model.VAPIDPublicKeyResponse
	if err := json.Unmarshal(body, &key); err != nil {
		t.Fatalf("decode vapid response: %v", err)
	}
	if key.Application != "sample" || key.PublicKey == "" {
		t.Fatalf("unexpected vapid response: %+v", key)
	}

	if status, _ := h.do(t, http.MethodGet, "/v1/applications/unknown/vapid-public-key", "", nil); status != http.StatusNotFound {
		t.Fatalf("unknown application vapid key: got %d want 404", status)
	}
}

// A device identifier is never accepted from the caller, so no caller can name
// a device that is not theirs. Rotation is proved by presenting the live token.
func TestRegistrationCannotTargetAnotherDevice(t *testing.T) {
	h := newHarness(t)

	victim := h.registerAndroid(t)

	// The install id is public to its owner and useless to anyone else: an
	// unauthenticated register call always creates a fresh device.
	status, body := h.do(t, http.MethodPost, "/v1/devices/register", "", map[string]any{
		"application": "sample",
		"platform":    "android",
		"fcm_fid":     "attacker-fid",
		"install_id":  victim.InstallID,
	})
	if status != http.StatusOK {
		t.Fatalf("register: status %d body %s", status, body)
	}

	var attacker model.RegisterDeviceResponse
	if err := json.Unmarshal(body, &attacker); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if attacker.DeviceID == victim.DeviceID {
		t.Fatal("an unauthenticated caller was given another device's identity")
	}
	if attacker.DeviceToken == victim.DeviceToken {
		t.Fatal("an unauthenticated caller was given another device's token")
	}

	// The victim's token still works, so it was not rotated out from under it.
	if status, _ := h.do(t, http.MethodPut, "/v1/subscriptions", victim.DeviceToken, model.UpdateSubscriptionsRequest{}); status != http.StatusOK {
		t.Fatalf("victim token stopped working: status %d", status)
	}
}

// Rotating with the device's own token keeps its identity and its history, and
// reports the sequence it may actually resume from.
func TestRotationKeepsIdentityAndReportsAcknowledgedSequence(t *testing.T) {
	h := newHarness(t)

	device := h.registerAndroid(t)
	h.subscribe(t, device.DeviceToken, []model.Subscription{{Channel: "alerts"}})
	h.publish(t, alert("key-1"))

	jobs := h.claimFCM(t)
	if len(jobs) != 1 {
		t.Fatalf("fcm jobs: got %d want 1", len(jobs))
	}

	status, body := h.do(t, http.MethodPost, "/v1/devices/register", device.DeviceToken, model.RegisterDeviceRequest{
		Platform: model.PlatformAndroid, FCMFID: "rotated-fid",
	})
	if status != http.StatusOK {
		t.Fatalf("rotate: status %d body %s", status, body)
	}

	var rotated model.RegisterDeviceResponse
	if err := json.Unmarshal(body, &rotated); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if rotated.DeviceID != device.DeviceID || rotated.InstallID != device.InstallID {
		t.Fatalf("rotation changed identity: %+v want %+v", rotated, device)
	}
	if rotated.DeviceToken == device.DeviceToken {
		t.Fatal("rotation returned the same token")
	}
	// Nothing was acknowledged, so the server reports zero.
	if rotated.AcknowledgedSequence != 0 {
		t.Fatalf("acknowledged sequence: got %d want 0", rotated.AcknowledgedSequence)
	}

	if status, _ := h.do(t, http.MethodPut, "/v1/subscriptions", device.DeviceToken, model.UpdateSubscriptionsRequest{}); status != http.StatusUnauthorized {
		t.Fatalf("the old token still works: status %d", status)
	}
}

func TestRotationRejectsAnUnknownToken(t *testing.T) {
	h := newHarness(t)

	status, _ := h.do(t, http.MethodPost, "/v1/devices/register", "nonsense", model.RegisterDeviceRequest{
		Platform: model.PlatformAndroid, FCMFID: "invalid-owner-fid",
	})
	if status != http.StatusUnauthorized {
		t.Fatalf("status: got %d want 401", status)
	}
}

func TestAndroidCanRefreshFCMInstallation(t *testing.T) {
	h := newHarness(t)
	device := h.registerAndroid(t)

	status, body := h.do(t, http.MethodPut, "/v1/device/fcm", device.DeviceToken, model.UpdateFCMRequest{FCMFID: "refreshed-fid"})
	if status != http.StatusNoContent {
		t.Fatalf("refresh fcm: status %d body %s", status, body)
	}
	h.subscribe(t, device.DeviceToken, []model.Subscription{{Channel: "alerts"}})
	h.publish(t, alert("refresh-fcm"))
	jobs := h.claimFCM(t)
	if len(jobs) != 1 || jobs[0].FCMFID != "refreshed-fid" {
		t.Fatalf("unexpected fcm jobs: %+v", jobs)
	}
}

// A malformed delivery id must read as unknown, not as a server error.
func TestMalformedDeliveryIDIsNotFound(t *testing.T) {
	h := newHarness(t)
	device := h.registerAndroid(t)

	status, body := h.do(t, http.MethodPost, "/v1/deliveries/not-a-uuid/ack", device.DeviceToken, model.AckRequest{Status: model.AckDelivered})
	if status != http.StatusNotFound {
		t.Fatalf("status: got %d want 404, body %s", status, body)
	}
}

func TestOversizedBodyIsRejected(t *testing.T) {
	h := newHarness(t)

	request := alert("key-1")
	request.Data = json.RawMessage(`{"blob":"` + strings.Repeat("x", 64<<10) + `"}`)

	status, _ := h.do(t, http.MethodPost, "/v1/messages", h.producerToken, request)
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", status)
	}
}

func TestOversizedPushEnvelopeIsRejected(t *testing.T) {
	h := newHarness(t)
	request := alert("key-1")
	request.Body = strings.Repeat("b", 2000)
	request.Data = json.RawMessage(`{"blob":"` + strings.Repeat("d", 1500) + `"}`)

	status, _ := h.do(t, http.MethodPost, "/v1/messages", h.producerToken, request)
	if status != http.StatusBadRequest {
		t.Fatalf("status: got %d want 400", status)
	}
}

// The documented route table and the wired routes must not drift apart.
func TestDocumentedRoutesMatchTheRouter(t *testing.T) {
	documented := map[string]bool{
		"GET /health":                                       true,
		"POST /api/session":                                 true,
		"GET /api/session":                                  true,
		"DELETE /api/session":                               true,
		"GET /api/health":                                   true,
		"GET /api/applications":                             true,
		"POST /api/applications":                            true,
		"GET /api/applications/:slug/producers":             true,
		"POST /api/applications/:slug/producers":            true,
		"POST /api/applications/:slug/producers/:id/revoke": true,
		"POST /api/applications/:slug/messages":             true,
		"GET /api/applications/:slug/messages":              true,
		"GET /api/applications/:slug/routing":               true,
		"POST /api/applications/:slug/topics":               true,
		"PATCH /api/applications/:slug/topics/:id":          true,
		"POST /api/applications/:slug/topics/:id/archive":   true,
		"GET /v1/applications/:slug/vapid-public-key":       true,
		"POST /v1/devices/register":                         true,
		"POST /v1/messages":                                 true,
		"PUT /v1/subscriptions":                             true,
		"PUT /v1/device/fcm":                                true,
		"POST /v1/deliveries/:delivery_id/ack":              true,
	}

	for _, route := range server.New(testConfig(), "test", nil, nil, zerolog.Nop()).Routes() {
		key := route.Method + " " + route.Path
		if !documented[key] {
			t.Errorf("route %s is wired but not in docs/protocol-v1.md", key)
		}
		delete(documented, key)
	}
	for key := range documented {
		t.Errorf("route %s is documented but not wired", key)
	}
}
