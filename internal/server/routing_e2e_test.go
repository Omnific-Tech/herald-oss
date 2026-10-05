package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/model"
)

func TestRoutingCatalogEndToEnd(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)
	requireAPIError(t, h.request(t, http.MethodGet, "/api/applications/sample/routing", adminOrigin, nil, nil), http.StatusUnauthorized, "unauthenticated")
	if response := h.login(t); response.status != http.StatusOK {
		t.Fatalf("login: %d %s", response.status, response.body)
	}
	for _, slug := range []string{"sample", "other"} {
		response := h.request(t, http.MethodPost, "/api/applications", adminOrigin, map[string]string{
			"slug": slug, "display_name": slug, "origin": "https://" + slug + ".example.com", "vapid_subject": "mailto:ops@example.com",
		}, nil)
		if response.status != http.StatusCreated {
			t.Fatalf("create %s: %d %s", slug, response.status, response.body)
		}
	}
	initial := h.request(t, http.MethodGet, "/api/applications/sample/routing", adminOrigin, nil, nil)
	if initial.status != http.StatusOK {
		t.Fatalf("routing: %d %s", initial.status, initial.body)
	}
	initialCatalog := decodeJSON[struct {
		Channels []struct {
			ID string `json:"id"`
		} `json:"channels"`
		Topics []model.RoutingTopic `json:"topics"`
	}](t, initial.body)
	if len(initialCatalog.Channels) != 2 || len(initialCatalog.Topics) != 0 {
		t.Fatalf("initial catalog: %+v", initialCatalog)
	}
	requireAPIError(t, h.request(t, http.MethodGet, "/api/applications/unknown/routing", adminOrigin, nil, nil), http.StatusNotFound, "application_not_found")

	createPath := "/api/applications/sample/topics"
	input := map[string]string{"id": "earthquake.jakarta", "label": "Jakarta earthquakes", "channel": "alerts"}
	created := h.request(t, http.MethodPost, createPath, adminOrigin, input, nil)
	if created.status != http.StatusCreated {
		t.Fatalf("create topic: %d %s", created.status, created.body)
	}
	topic := decodeJSON[struct {
		Topic model.RoutingTopic `json:"topic"`
	}](t, created.body).Topic
	if topic.ID != input["id"] || topic.Channel != "alerts" || topic.ArchivedAt != nil {
		t.Fatalf("created topic: %+v", topic)
	}
	requireAPIError(t, h.request(t, http.MethodPost, createPath, adminOrigin, input, nil), http.StatusConflict, "topic_exists")
	requireAPIError(t, h.request(t, http.MethodPost, createPath, adminOrigin, map[string]string{"id": "bad", "label": "Bad", "channel": "unknown"}, nil), http.StatusUnprocessableEntity, "invalid_fields")
	requireAPIError(t, h.request(t, http.MethodPost, createPath, adminOrigin, map[string]string{"id": "BAD", "label": "Bad", "channel": "alerts"}, nil), http.StatusUnprocessableEntity, "invalid_fields")
	requireAPIError(t, h.request(t, http.MethodPost, createPath, "https://evil.example", input, nil), http.StatusForbidden, "origin_rejected")

	other := h.request(t, http.MethodGet, "/api/applications/other/routing", adminOrigin, nil, nil)
	otherCatalog := decodeJSON[struct {
		Topics []model.RoutingTopic `json:"topics"`
	}](t, other.body)
	if other.status != http.StatusOK || len(otherCatalog.Topics) != 0 {
		t.Fatalf("other topic leak: %d %s", other.status, other.body)
	}

	path := createPath + "/earthquake.jakarta"
	edited := h.request(t, http.MethodPatch, path, adminOrigin, map[string]string{"label": "Jakarta area earthquakes"}, nil)
	if edited.status != http.StatusOK || decodeJSON[struct {
		Topic model.RoutingTopic `json:"topic"`
	}](t, edited.body).Topic.Label != "Jakarta area earthquakes" {
		t.Fatalf("edit topic: %d %s", edited.status, edited.body)
	}
	requireAPIError(t, h.request(t, http.MethodPatch, path, adminOrigin, map[string]string{"label": ""}, nil), http.StatusUnprocessableEntity, "invalid_fields")
	requireAPIError(t, h.request(t, http.MethodPatch, "/api/applications/other/topics/earthquake.jakarta", adminOrigin, map[string]string{"label": "Wrong app"}, nil), http.StatusNotFound, "topic_not_found")

	registration := h.request(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{Application: "sample", Platform: model.PlatformAndroid, FCMFID: "routing-e2e-fid"}, nil)
	if registration.status != http.StatusOK {
		t.Fatalf("register: %d %s", registration.status, registration.body)
	}
	device := decodeJSON[model.RegisterDeviceResponse](t, registration.body)
	subscribe := func(topicID string) adminHTTPResponse {
		return h.request(t, http.MethodPut, "/v1/subscriptions", "", model.UpdateSubscriptionsRequest{Subscriptions: []model.Subscription{{Channel: "alerts", Topic: topicID}}}, map[string]string{"Authorization": "Bearer " + device.DeviceToken})
	}
	if response := subscribe("earthquake.jakarta"); response.status != http.StatusOK {
		t.Fatalf("subscribe: %d %s", response.status, response.body)
	}
	if response := subscribe("earthquake.unknown"); response.status != http.StatusBadRequest {
		t.Fatalf("unknown subscription: %d %s", response.status, response.body)
	}
	var retained int
	if err := h.db.QueryRow(`SELECT count(*) FROM subscriptions WHERE device_id = $1 AND topic = 'earthquake.jakarta'`, device.DeviceID).Scan(&retained); err != nil || retained != 1 {
		t.Fatalf("invalid update replaced existing subscription: count=%d err=%v", retained, err)
	}

	message := alert("routing-e2e")
	message.Topic = "earthquake.jakarta"
	published := h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, message, nil)
	if published.status != http.StatusAccepted || decodeJSON[model.PublishResponse](t, published.body).DeviceCount != 1 {
		t.Fatalf("publish: %d %s", published.status, published.body)
	}
	message.IdempotencyKey = "routing-unknown"
	message.Topic = "earthquake.unknown"
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, message, nil), http.StatusUnprocessableEntity, "invalid_fields")
	producerResponse := h.request(t, http.MethodPost, "/api/applications/sample/producers", adminOrigin, map[string]string{"name": "sample-api"}, nil)
	if producerResponse.status != http.StatusCreated {
		t.Fatalf("create producer: %d %s", producerResponse.status, producerResponse.body)
	}
	producer := decodeJSON[struct {
		Token string `json:"token"`
	}](t, producerResponse.body)
	producerPublish := h.request(t, http.MethodPost, "/v1/messages", "", message, map[string]string{"Authorization": "Bearer " + producer.Token})
	if producerPublish.status != http.StatusBadRequest {
		t.Fatalf("producer accepted unknown topic: %d %s", producerPublish.status, producerPublish.body)
	}
	message.IdempotencyKey = "routing-bad-channel"
	message.Channel = "unknown"
	message.Topic = ""
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, message, nil), http.StatusUnprocessableEntity, "invalid_fields")

	archived := h.request(t, http.MethodPost, path+"/archive", adminOrigin, map[string]any{}, nil)
	if archived.status != http.StatusOK || decodeJSON[struct {
		Topic model.RoutingTopic `json:"topic"`
	}](t, archived.body).Topic.ArchivedAt == nil {
		t.Fatalf("archive: %d %s", archived.status, archived.body)
	}
	requireAPIError(t, h.request(t, http.MethodPatch, path, adminOrigin, map[string]string{"label": "New label"}, nil), http.StatusConflict, "already_archived")
	if response := subscribe("earthquake.jakarta"); response.status != http.StatusBadRequest {
		t.Fatalf("archived subscription: %d %s", response.status, response.body)
	}
	message.IdempotencyKey = "routing-archived"
	message.Channel = "alerts"
	message.Topic = "earthquake.jakarta"
	requireAPIError(t, h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, message, nil), http.StatusUnprocessableEntity, "invalid_fields")
	requireAPIError(t, h.request(t, http.MethodPost, path+"/archive", adminOrigin, map[string]any{}, nil), http.StatusConflict, "already_archived")
	final := h.request(t, http.MethodGet, "/api/applications/sample/routing", adminOrigin, nil, nil)
	finalCatalog := decodeJSON[struct {
		Topics []model.RoutingTopic `json:"topics"`
	}](t, final.body)
	if len(finalCatalog.Topics) != 1 || finalCatalog.Topics[0].ArchivedAt == nil {
		t.Fatalf("archived topic missing from catalog: %s", final.body)
	}
}
