package server_test

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/server"
	"github.com/Omnific-Tech/herald/internal/store/postgres"
	"github.com/Omnific-Tech/herald/internal/testsupport"
	"github.com/rs/zerolog"
)

const (
	adminOrigin   = "https://admin.herald.example.com"
	adminUsername = "operator"
	adminPassword = "test-admin-password"
)

type adminHarness struct {
	server *httptest.Server
	client *http.Client
	db     *sql.DB
	store  *postgres.Store
}

type adminHTTPResponse struct {
	status int
	header http.Header
	body   []byte
}

func newAdminHarness(t *testing.T, sessionTTL time.Duration, maxLoginAttempts int) *adminHarness {
	t.Helper()

	db := testsupport.Database(t, "server_admin")
	key, err := crypto.NewEncryptionKey()
	if err != nil {
		t.Fatalf("encryption key: %v", err)
	}
	cipher, err := crypto.NewCipher(key)
	if err != nil {
		t.Fatalf("cipher: %v", err)
	}
	passwordHash, err := crypto.HashPassword(adminPassword)
	if err != nil {
		t.Fatalf("password hash: %v", err)
	}

	cfg := testConfig()
	cfg.Admin = config.Admin{
		Origin:           adminOrigin,
		Username:         adminUsername,
		PasswordHash:     passwordHash,
		SessionTTL:       sessionTTL,
		LoginMaxAttempts: maxLoginAttempts,
		LoginWindow:      time.Minute,
	}

	dataStore := postgres.New(db, cipher)
	httpServer := httptest.NewTLSServer(server.New(cfg, "admin-e2e", db, dataStore, zerolog.Nop()).Handler())
	t.Cleanup(httpServer.Close)

	jar, err := cookiejar.New(nil)
	if err != nil {
		t.Fatalf("cookie jar: %v", err)
	}
	client := httpServer.Client()
	client.Jar = jar

	return &adminHarness{server: httpServer, client: client, db: db, store: dataStore}
}

func (h *adminHarness) request(t *testing.T, method, path, origin string, body any, headers map[string]string) adminHTTPResponse {
	t.Helper()

	var reader io.Reader
	if body != nil {
		payload, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("encode request: %v", err)
		}
		reader = bytes.NewReader(payload)
	}
	request, err := http.NewRequest(method, h.server.URL+path, reader)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	if origin != "" {
		request.Header.Set("Origin", origin)
	}
	for key, value := range headers {
		request.Header.Set(key, value)
	}

	response, err := h.client.Do(request)
	if err != nil {
		t.Fatalf("%s %s: %v", method, path, err)
	}
	defer func() { _ = response.Body.Close() }()
	payload, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read response: %v", err)
	}
	return adminHTTPResponse{status: response.StatusCode, header: response.Header, body: payload}
}

func (h *adminHarness) login(t *testing.T) adminHTTPResponse {
	t.Helper()
	return h.request(t, http.MethodPost, "/api/session", adminOrigin, map[string]string{
		"username": adminUsername,
		"password": adminPassword,
	}, nil)
}

func decodeJSON[T any](t *testing.T, body []byte) T {
	t.Helper()
	var value T
	if err := json.Unmarshal(body, &value); err != nil {
		t.Fatalf("decode %s: %v", body, err)
	}
	return value
}

func requireAPIError(t *testing.T, response adminHTTPResponse, status int, code string) {
	t.Helper()
	if response.status != status {
		t.Fatalf("status: got %d want %d, body %s", response.status, status, response.body)
	}
	errorBody := decodeJSON[struct {
		Error string `json:"error"`
		Code  string `json:"code"`
	}](t, response.body)
	if errorBody.Error == "" || errorBody.Code != code {
		t.Fatalf("error: got %+v want code %q", errorBody, code)
	}
}

// This is the full admin control-plane path over HTTP and a real database:
// authenticate, provision an app, mint and use a producer, revoke it, and log out.
func TestAdminControlPlaneEndToEnd(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)

	requireAPIError(t, h.request(t, http.MethodGet, "/api/session", adminOrigin, nil, nil), http.StatusUnauthorized, "unauthenticated")

	login := h.login(t)
	if login.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", login.status, login.body)
	}
	if login.header.Get("Access-Control-Allow-Origin") != adminOrigin || login.header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("credentialed CORS headers: %v", login.header)
	}
	if login.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("login cache control: %q", login.header.Get("Cache-Control"))
	}
	cookie := login.header.Get("Set-Cookie")
	for _, attribute := range []string{"__Host-herald_admin=", "Path=/", "HttpOnly", "Secure", "SameSite=Strict"} {
		if !strings.Contains(cookie, attribute) {
			t.Fatalf("session cookie missing %q: %s", attribute, cookie)
		}
	}

	session := h.request(t, http.MethodGet, "/api/session", adminOrigin, nil, nil)
	if session.status != http.StatusOK || !strings.Contains(string(session.body), `"username":"operator"`) {
		t.Fatalf("session: status %d body %s", session.status, session.body)
	}

	createApplication := h.request(t, http.MethodPost, "/api/applications", adminOrigin, map[string]string{
		"slug":          "sample",
		"display_name":  "Sample",
		"origin":        "https://sample.example.com",
		"vapid_subject": "mailto:ops@example.com",
	}, nil)
	if createApplication.status != http.StatusCreated {
		t.Fatalf("create application: status %d body %s", createApplication.status, createApplication.body)
	}
	createdApplication := decodeJSON[struct {
		Application    model.Application `json:"application"`
		VAPIDPublicKey string            `json:"vapid_public_key"`
	}](t, createApplication.body)
	if createdApplication.Application.Slug != "sample" || createdApplication.VAPIDPublicKey == "" {
		t.Fatalf("created application: %+v", createdApplication)
	}

	publicKey := h.request(t, http.MethodGet, "/v1/applications/sample/vapid-public-key", "", nil, nil)
	if publicKey.status != http.StatusOK || !strings.Contains(string(publicKey.body), createdApplication.VAPIDPublicKey) {
		t.Fatalf("public key: status %d body %s", publicKey.status, publicKey.body)
	}
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications", adminOrigin, map[string]string{
		"slug": "sample", "display_name": "Duplicate", "origin": "https://sample.example.com", "vapid_subject": "mailto:ops@example.com",
	}, nil), http.StatusConflict, "slug_taken")

	applications := h.request(t, http.MethodGet, "/api/applications", adminOrigin, nil, nil)
	if applications.status != http.StatusOK || !strings.Contains(string(applications.body), `"slug":"sample"`) {
		t.Fatalf("applications: status %d body %s", applications.status, applications.body)
	}

	createProducer := h.request(t, http.MethodPost, "/api/applications/sample/producers", adminOrigin, map[string]string{"name": "sample-api"}, nil)
	if createProducer.status != http.StatusCreated || createProducer.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("create producer: status %d headers %v body %s", createProducer.status, createProducer.header, createProducer.body)
	}
	createdProducer := decodeJSON[struct {
		Producer model.AdminProducer `json:"producer"`
		Token    string              `json:"token"`
	}](t, createProducer.body)
	if createdProducer.Producer.ID == "" || createdProducer.Token == "" {
		t.Fatalf("created producer: %+v", createdProducer)
	}
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/sample/producers", adminOrigin, map[string]string{"name": "sample-api"}, nil), http.StatusConflict, "name_taken")

	producers := h.request(t, http.MethodGet, "/api/applications/sample/producers", adminOrigin, nil, nil)
	if producers.status != http.StatusOK || !strings.Contains(string(producers.body), createdProducer.Producer.ID) {
		t.Fatalf("producers: status %d body %s", producers.status, producers.body)
	}

	producerMessage := alert("admin-e2e")
	producerMessage.Topic = ""
	publish := h.request(t, http.MethodPost, "/v1/messages", "", producerMessage, map[string]string{
		"Authorization": "Bearer " + createdProducer.Token,
	})
	if publish.status != http.StatusAccepted {
		t.Fatalf("publish with minted token: status %d body %s", publish.status, publish.body)
	}

	revokePath := "/api/applications/sample/producers/" + createdProducer.Producer.ID + "/revoke"
	revoke := h.request(t, http.MethodPost, revokePath, adminOrigin, map[string]any{}, nil)
	if revoke.status != http.StatusOK || !strings.Contains(string(revoke.body), `"revoked_at"`) {
		t.Fatalf("revoke: status %d body %s", revoke.status, revoke.body)
	}
	requireAPIError(t, h.request(t, http.MethodPost, revokePath, adminOrigin, map[string]any{}, nil), http.StatusConflict, "already_revoked")

	producerMessage.IdempotencyKey = "admin-e2e-revoked"
	revokedPublish := h.request(t, http.MethodPost, "/v1/messages", "", producerMessage, map[string]string{
		"Authorization": "Bearer " + createdProducer.Token,
	})
	if revokedPublish.status != http.StatusUnauthorized {
		t.Fatalf("revoked token publish: status %d body %s", revokedPublish.status, revokedPublish.body)
	}

	// A revoked name can be reused, while the immutable producer ids remain distinct.
	replacement := h.request(t, http.MethodPost, "/api/applications/sample/producers", adminOrigin, map[string]string{"name": "sample-api"}, nil)
	if replacement.status != http.StatusCreated || strings.Contains(string(replacement.body), createdProducer.Producer.ID) {
		t.Fatalf("replacement producer: status %d body %s", replacement.status, replacement.body)
	}

	logout := h.request(t, http.MethodDelete, "/api/session", adminOrigin, nil, nil)
	if logout.status != http.StatusNoContent || !strings.Contains(logout.header.Get("Set-Cookie"), "Max-Age=0") {
		t.Fatalf("logout: status %d headers %v body %s", logout.status, logout.header, logout.body)
	}
	requireAPIError(t, h.request(t, http.MethodGet, "/api/session", adminOrigin, nil, nil), http.StatusUnauthorized, "unauthenticated")
	if secondLogout := h.request(t, http.MethodDelete, "/api/session", adminOrigin, nil, nil); secondLogout.status != http.StatusNoContent {
		t.Fatalf("idempotent logout: status %d body %s", secondLogout.status, secondLogout.body)
	}
}

func TestAdminMessagePublishingEndToEnd(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)
	if response := h.login(t); response.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", response.status, response.body)
	}
	if response := h.request(t, http.MethodPost, "/api/applications", adminOrigin, map[string]string{
		"slug": "sample", "display_name": "Sample", "origin": "https://sample.example.com", "vapid_subject": "mailto:ops@example.com",
	}, nil); response.status != http.StatusCreated {
		t.Fatalf("create application: status %d body %s", response.status, response.body)
	}

	registration := h.request(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{
		Application: "sample", Platform: model.PlatformAndroid, FCMFID: "admin-publish-fid",
	}, nil)
	if registration.status != http.StatusOK {
		t.Fatalf("register: status %d body %s", registration.status, registration.body)
	}
	device := decodeJSON[model.RegisterDeviceResponse](t, registration.body)
	if response := h.request(t, http.MethodPut, "/v1/subscriptions", "", model.UpdateSubscriptionsRequest{
		Subscriptions: []model.Subscription{{Channel: "alerts"}},
	}, map[string]string{"Authorization": "Bearer " + device.DeviceToken}); response.status != http.StatusOK {
		t.Fatalf("subscribe: status %d body %s", response.status, response.body)
	}

	message := alert("admin-message-e2e")
	message.Topic = ""
	published := h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, message, nil)
	if published.status != http.StatusAccepted {
		t.Fatalf("publish: status %d body %s", published.status, published.body)
	}
	first := decodeJSON[model.PublishResponse](t, published.body)
	if first.Deduplicated || first.DeviceCount != 1 || first.MessageID == "" {
		t.Fatalf("publish response: %+v", first)
	}

	var producerID, recordedAdmin sql.NullString
	if err := h.db.QueryRow(`SELECT producer_id, admin_username FROM messages WHERE id = $1`, first.MessageID).Scan(&producerID, &recordedAdmin); err != nil {
		t.Fatalf("read publisher attribution: %v", err)
	}
	if producerID.Valid || !recordedAdmin.Valid || recordedAdmin.String != adminUsername {
		t.Fatalf("publisher attribution: producer=%+v admin=%+v", producerID, recordedAdmin)
	}

	jobs, err := h.store.Deliveries().ClaimFCM(context.Background(), 10, 30*time.Second)
	if err != nil {
		t.Fatalf("claim FCM: %v", err)
	}
	if len(jobs) != 1 || jobs[0].FCMFID != "admin-publish-fid" || jobs[0].Message.ID != first.MessageID {
		t.Fatalf("FCM jobs: %+v", jobs)
	}

	replayed := h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, message, nil)
	if replayed.status != http.StatusAccepted {
		t.Fatalf("replay: status %d body %s", replayed.status, replayed.body)
	}
	second := decodeJSON[model.PublishResponse](t, replayed.body)
	if !second.Deduplicated || second.MessageID != first.MessageID || second.DeviceCount != 1 {
		t.Fatalf("replay response: %+v", second)
	}
	var deliveries int
	if err := h.db.QueryRow(`SELECT count(*) FROM deliveries WHERE message_id = $1`, first.MessageID).Scan(&deliveries); err != nil {
		t.Fatalf("count deliveries: %v", err)
	}
	if deliveries != 1 {
		t.Fatalf("deduplicated publish created %d deliveries", deliveries)
	}
}

func TestAdminMessagePublishingErrors(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)
	if response := h.login(t); response.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", response.status, response.body)
	}
	if response := h.request(t, http.MethodPost, "/api/applications", adminOrigin, map[string]string{
		"slug": "sample", "display_name": "Sample", "origin": "https://sample.example.com", "vapid_subject": "mailto:ops@example.com",
	}, nil); response.status != http.StatusCreated {
		t.Fatalf("create application: status %d body %s", response.status, response.body)
	}

	zeroMessage := alert("zero-recipients")
	zeroMessage.Topic = ""
	zeroRecipients := h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, zeroMessage, nil)
	if zeroRecipients.status != http.StatusAccepted {
		t.Fatalf("zero-recipient publish: status %d body %s", zeroRecipients.status, zeroRecipients.body)
	}
	if response := decodeJSON[model.PublishResponse](t, zeroRecipients.body); response.DeviceCount != 0 {
		t.Fatalf("zero-recipient response: %+v", response)
	}

	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/unknown/messages", adminOrigin, alert("unknown-app"), nil), http.StatusNotFound, "application_not_found")

	invalid := []model.PublishRequest{
		{},
		func() model.PublishRequest {
			request := alert("expired")
			request.ExpiresAt = time.Now().Add(-time.Minute)
			return request
		}(),
		func() model.PublishRequest {
			request := alert("bad-link")
			request.Link = "https://evil.example"
			return request
		}(),
		func() model.PublishRequest {
			request := alert("oversized")
			request.Body = strings.Repeat("b", 3500)
			return request
		}(),
	}
	for _, request := range invalid {
		response := h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, request, nil)
		requireAPIError(t, response, http.StatusUnprocessableEntity, "invalid_fields")
		errorBody := decodeJSON[model.AdminErrorResponse](t, response.body)
		if len(errorBody.Fields) == 0 {
			t.Fatalf("validation response omitted fields: %s", response.body)
		}
	}

	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/sample/messages", "https://evil.example", alert("wrong-origin"), nil), http.StatusForbidden, "origin_rejected")
}

func TestAdminRejectsUntrustedOriginsAndInvalidInput(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)

	for _, origin := range []string{"", "https://evil.example", "https://sample.example.com"} {
		response := h.request(t, http.MethodPost, "/api/session", origin, map[string]string{
			"username": adminUsername, "password": adminPassword,
		}, nil)
		requireAPIError(t, response, http.StatusForbidden, "origin_rejected")
		if response.header.Get("Access-Control-Allow-Origin") != "" {
			t.Fatalf("untrusted origin %q received CORS access", origin)
		}
	}

	if response := h.login(t); response.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", response.status, response.body)
	}
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications", "https://evil.example", map[string]string{
		"slug": "sample", "display_name": "Sample", "origin": "https://sample.example.com", "vapid_subject": "mailto:ops@example.com",
	}, nil), http.StatusForbidden, "origin_rejected")

	invalidRequests := []map[string]string{
		{"slug": "SAMPLE", "display_name": "Sample", "origin": "https://sample.example.com", "vapid_subject": "mailto:ops@example.com"},
		{"slug": "sample", "display_name": "Sample", "origin": "http://sample.example.com", "vapid_subject": "mailto:ops@example.com"},
		{"slug": "sample", "display_name": "Sample", "origin": "https://sample.example.com", "vapid_subject": "invalid"},
	}
	for _, request := range invalidRequests {
		requireAPIError(t, h.request(t, http.MethodPost, "/api/applications", adminOrigin, request, nil), http.StatusUnprocessableEntity, "invalid_fields")
	}

	requireAPIError(t, h.request(t, http.MethodGet, "/api/applications/unknown/producers", adminOrigin, nil, nil), http.StatusNotFound, "application_not_found")
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/unknown/producers", adminOrigin, map[string]string{"name": "api"}, nil), http.StatusNotFound, "application_not_found")
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/sample/producers/not-a-uuid/revoke", adminOrigin, map[string]any{}, nil), http.StatusNotFound, "producer_not_found")
}

func TestAdminLoginRateLimitAndSessionExpiry(t *testing.T) {
	h := newAdminHarness(t, 50*time.Millisecond, 2)

	for attempt := 0; attempt < 2; attempt++ {
		requireAPIError(t, h.request(t, http.MethodPost, "/api/session", adminOrigin, map[string]string{
			"username": adminUsername, "password": "wrong",
		}, nil), http.StatusUnauthorized, "invalid_credentials")
	}
	rateLimited := h.request(t, http.MethodPost, "/api/session", adminOrigin, map[string]string{
		"username": adminUsername, "password": adminPassword,
	}, nil)
	requireAPIError(t, rateLimited, http.StatusTooManyRequests, "rate_limited")
	if rateLimited.header.Get("Retry-After") == "" {
		t.Fatal("rate-limited login omitted Retry-After")
	}

	// A separate server has a fresh limiter and proves database-backed expiry.
	expiryHarness := newAdminHarness(t, 50*time.Millisecond, 5)
	if response := expiryHarness.login(t); response.status != http.StatusOK {
		t.Fatalf("login: status %d body %s", response.status, response.body)
	}
	time.Sleep(100 * time.Millisecond)
	requireAPIError(t, expiryHarness.request(t, http.MethodGet, "/api/session", adminOrigin, nil, nil), http.StatusUnauthorized, "unauthenticated")
}

func TestAdminCORSPreflightIsStrict(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)

	allowed := h.request(t, http.MethodOptions, "/api/session", adminOrigin, nil, map[string]string{
		"Access-Control-Request-Method":  http.MethodPost,
		"Access-Control-Request-Headers": "content-type",
	})
	if allowed.status != http.StatusNoContent || allowed.header.Get("Access-Control-Allow-Origin") != adminOrigin || allowed.header.Get("Access-Control-Allow-Credentials") != "true" {
		t.Fatalf("allowed preflight: status %d headers %v", allowed.status, allowed.header)
	}

	denied := h.request(t, http.MethodOptions, "/api/session", "https://evil.example", nil, map[string]string{
		"Access-Control-Request-Method": http.MethodPost,
	})
	if denied.status != http.StatusForbidden || denied.header.Get("Access-Control-Allow-Origin") != "" {
		t.Fatalf("denied preflight: status %d headers %v", denied.status, denied.header)
	}
}

func TestAdminIsDisabledWithoutCredentials(t *testing.T) {
	db := testsupport.Database(t, "server_admin_disabled")
	dataStore := postgres.New(db, nil)
	httpServer := httptest.NewServer(server.New(testConfig(), "test", db, dataStore, zerolog.Nop()).Handler())
	t.Cleanup(httpServer.Close)

	request, err := http.NewRequestWithContext(context.Background(), http.MethodPost, httpServer.URL+"/api/session", strings.NewReader(`{"username":"operator","password":"secret"}`))
	if err != nil {
		t.Fatalf("request: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")
	response, err := httpServer.Client().Do(request)
	if err != nil {
		t.Fatalf("login: %v", err)
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusServiceUnavailable {
		t.Fatalf("status: got %d want 503", response.StatusCode)
	}
}
