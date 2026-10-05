package server_test

import (
	"net/http"
	"testing"
	"time"

	"github.com/Omnific-Tech/herald/internal/model"
)

type historyPage struct {
	Messages []struct {
		ID          string `json:"id"`
		Title       string `json:"title"`
		Channel     string `json:"channel"`
		Topic       string `json:"topic"`
		Priority    string `json:"priority"`
		DeviceCount int    `json:"device_count"`
		CreatedAt   string `json:"created_at"`
		ExpiresAt   string `json:"expires_at"`
		Publisher   struct {
			Type string `json:"type"`
			Name string `json:"name"`
		} `json:"publisher"`
		DeliveryCounts struct {
			Pending    int `json:"pending"`
			Sent       int `json:"sent"`
			Displayed  int `json:"displayed"`
			Delivered  int `json:"delivered"`
			Suppressed int `json:"suppressed"`
			Failed     int `json:"failed"`
			Expired    int `json:"expired"`
			Superseded int `json:"superseded"`
		} `json:"delivery_counts"`
		DeliveryErrors []struct {
			State  string `json:"state"`
			Reason string `json:"reason"`
			Count  int    `json:"count"`
		} `json:"delivery_errors"`
	} `json:"messages"`
	NextCursor *string `json:"next_cursor"`
}

func TestAdminMessageHistoryEndToEnd(t *testing.T) {
	h := newAdminHarness(t, 8*time.Hour, 5)
	requireAPIError(t, h.request(t, http.MethodGet, "/api/applications/sample/messages", adminOrigin, nil, nil), http.StatusUnauthorized, "unauthenticated")
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
	if empty := h.request(t, http.MethodGet, "/api/applications/sample/messages", adminOrigin, nil, nil); empty.status != http.StatusOK || len(decodeJSON[historyPage](t, empty.body).Messages) != 0 {
		t.Fatalf("empty history: %d %s", empty.status, empty.body)
	}

	devices := make([]model.RegisterDeviceResponse, 2)
	for i := range devices {
		registration := h.request(t, http.MethodPost, "/v1/devices/register", "", model.RegisterDeviceRequest{
			Application: "sample", Platform: model.PlatformAndroid, FCMFID: "history-fid-" + string(rune('a'+i)),
		}, nil)
		if registration.status != http.StatusOK {
			t.Fatalf("register: %d %s", registration.status, registration.body)
		}
		devices[i] = decodeJSON[model.RegisterDeviceResponse](t, registration.body)
		response := h.request(t, http.MethodPut, "/v1/subscriptions", "", model.UpdateSubscriptionsRequest{
			Subscriptions: []model.Subscription{{Channel: "alerts"}},
		}, map[string]string{"Authorization": "Bearer " + devices[i].DeviceToken})
		if response.status != http.StatusOK {
			t.Fatalf("subscribe: %d %s", response.status, response.body)
		}
	}

	firstRequest := alert("history-admin")
	firstRequest.Topic = ""
	first := h.request(t, http.MethodPost, "/api/applications/sample/messages", adminOrigin, firstRequest, nil)
	if first.status != http.StatusAccepted {
		t.Fatalf("admin publish: %d %s", first.status, first.body)
	}
	firstMessage := decodeJSON[model.PublishResponse](t, first.body)
	var deliveryID string
	if err := h.db.QueryRow(`SELECT id FROM deliveries WHERE message_id = $1 AND device_id = $2`, firstMessage.MessageID, devices[0].DeviceID).Scan(&deliveryID); err != nil {
		t.Fatalf("read delivery: %v", err)
	}
	ack := h.request(t, http.MethodPost, "/v1/deliveries/"+deliveryID+"/ack", "", model.AckRequest{Status: model.AckDisplayed}, map[string]string{
		"Authorization": "Bearer " + devices[0].DeviceToken,
	})
	if ack.status != http.StatusOK {
		t.Fatalf("ack: %d %s", ack.status, ack.body)
	}
	if _, err := h.db.Exec(`UPDATE deliveries SET state = 'failed', last_error = 'FCM 400: invalid target' WHERE message_id = $1 AND device_id = $2`, firstMessage.MessageID, devices[1].DeviceID); err != nil {
		t.Fatalf("record failed delivery: %v", err)
	}

	producer := h.request(t, http.MethodPost, "/api/applications/sample/producers", adminOrigin, map[string]string{"name": "sample-api"}, nil)
	if producer.status != http.StatusCreated {
		t.Fatalf("producer: %d %s", producer.status, producer.body)
	}
	token := decodeJSON[struct {
		Token string `json:"token"`
	}](t, producer.body).Token
	secondRequest := alert("history-producer")
	secondRequest.Topic = ""
	second := h.request(t, http.MethodPost, "/v1/messages", "", secondRequest, map[string]string{"Authorization": "Bearer " + token})
	if second.status != http.StatusAccepted {
		t.Fatalf("producer publish: %d %s", second.status, second.body)
	}
	secondMessage := decodeJSON[model.PublishResponse](t, second.body)
	otherRequest := alert("history-other")
	otherRequest.Topic = ""
	if response := h.request(t, http.MethodPost, "/api/applications/other/messages", adminOrigin, otherRequest, nil); response.status != http.StatusAccepted {
		t.Fatalf("other publish: %d %s", response.status, response.body)
	}

	firstPageResponse := h.request(t, http.MethodGet, "/api/applications/sample/messages?limit=1", adminOrigin, nil, nil)
	if firstPageResponse.status != http.StatusOK || firstPageResponse.header.Get("Cache-Control") != "no-store" {
		t.Fatalf("history first page: %d %s", firstPageResponse.status, firstPageResponse.body)
	}
	firstPage := decodeJSON[historyPage](t, firstPageResponse.body)
	if len(firstPage.Messages) != 1 || firstPage.Messages[0].ID != secondMessage.MessageID || firstPage.NextCursor == nil {
		t.Fatalf("first page: %+v", firstPage)
	}
	if firstPage.Messages[0].Publisher.Type != "producer" || firstPage.Messages[0].Publisher.Name != "sample-api" || firstPage.Messages[0].DeviceCount != 2 {
		t.Fatalf("producer history: %+v", firstPage.Messages[0])
	}
	secondPageResponse := h.request(t, http.MethodGet, "/api/applications/sample/messages?limit=1&cursor="+*firstPage.NextCursor, adminOrigin, nil, nil)
	if secondPageResponse.status != http.StatusOK {
		t.Fatalf("history second page: %d %s", secondPageResponse.status, secondPageResponse.body)
	}
	secondPage := decodeJSON[historyPage](t, secondPageResponse.body)
	if len(secondPage.Messages) != 1 || secondPage.Messages[0].ID != firstMessage.MessageID || secondPage.NextCursor != nil {
		t.Fatalf("second page: %+v", secondPage)
	}
	item := secondPage.Messages[0]
	if item.Publisher.Type != "admin" || item.Publisher.Name != adminUsername || item.DeviceCount != 2 || item.DeliveryCounts.Displayed != 1 || item.DeliveryCounts.Pending != 0 || item.DeliveryCounts.Failed != 1 || item.DeliveryCounts.Sent != 0 || item.Title == "" || item.CreatedAt == "" || item.ExpiresAt == "" {
		t.Fatalf("admin history counts: %+v", item)
	}
	if len(item.DeliveryErrors) != 1 || item.DeliveryErrors[0].State != "failed" || item.DeliveryErrors[0].Reason != "FCM 400: invalid target" || item.DeliveryErrors[0].Count != 1 {
		t.Fatalf("admin history errors: %+v", item.DeliveryErrors)
	}

	requireAPIError(t, h.request(t, http.MethodGet, "/api/applications/unknown/messages", adminOrigin, nil, nil), http.StatusNotFound, "application_not_found")
	for _, query := range []string{"limit=0", "limit=101", "cursor=not-a-cursor"} {
		requireAPIError(t, h.request(t, http.MethodGet, "/api/applications/sample/messages?"+query, adminOrigin, nil, nil), http.StatusBadRequest, "invalid_query")
	}
}
