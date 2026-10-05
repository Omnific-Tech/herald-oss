package model

import "time"

type AdminSession struct {
	Username  string    `json:"username"`
	ExpiresAt time.Time `json:"expires_at"`
}

type AdminErrorResponse struct {
	Error  string            `json:"error"`
	Code   string            `json:"code"`
	Fields map[string]string `json:"fields,omitempty"`
}

type AdminLoginRequest struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type AdminCreateApplicationRequest struct {
	Slug         string `json:"slug"`
	DisplayName  string `json:"display_name"`
	Origin       string `json:"origin"`
	VAPIDSubject string `json:"vapid_subject"`
}

type AdminCreateApplicationResponse struct {
	Application    Application `json:"application"`
	VAPIDPublicKey string      `json:"vapid_public_key"`
}

type AdminApplicationsResponse struct {
	Applications []Application `json:"applications"`
}

type AdminCreateProducerRequest struct {
	Name string `json:"name"`
}

type AdminCreateProducerResponse struct {
	Producer AdminProducer `json:"producer"`
	Token    string        `json:"token"`
}

type AdminProducersResponse struct {
	Producers []AdminProducer `json:"producers"`
}

type AdminProducerResponse struct {
	Producer AdminProducer `json:"producer"`
}

type AdminProducer struct {
	ID        string     `json:"id"`
	Name      string     `json:"name"`
	RevokedAt *time.Time `json:"revoked_at"`
	CreatedAt time.Time  `json:"created_at"`
}

func NewAdminProducer(producer Producer) AdminProducer {
	return AdminProducer{
		ID: producer.ID, Name: producer.Name, RevokedAt: producer.RevokedAt, CreatedAt: producer.CreatedAt,
	}
}

type AdminMessagePublisher struct {
	Type string `json:"type"`
	Name string `json:"name"`
}

type AdminDeliveryCounts struct {
	Pending    int `json:"pending"`
	Sent       int `json:"sent"`
	Displayed  int `json:"displayed"`
	Delivered  int `json:"delivered"`
	Suppressed int `json:"suppressed"`
	Failed     int `json:"failed"`
	Expired    int `json:"expired"`
	Superseded int `json:"superseded"`
}

type AdminDeliveryError struct {
	State  string `json:"state"`
	Reason string `json:"reason"`
	Count  int    `json:"count"`
}

type AdminMessageHistory struct {
	ID             string                `json:"id"`
	Status         string                `json:"status"`
	Channel        string                `json:"channel"`
	Topic          string                `json:"topic"`
	Priority       Priority              `json:"priority"`
	Title          string                `json:"title"`
	Body           string                `json:"body"`
	Link           string                `json:"link"`
	DeviceCount    int                   `json:"device_count"`
	Publisher      AdminMessagePublisher `json:"publisher"`
	DeliveryCounts AdminDeliveryCounts   `json:"delivery_counts"`
	DeliveryErrors []AdminDeliveryError  `json:"delivery_errors"`
	CreatedAt      time.Time             `json:"created_at"`
	ExpiresAt      time.Time             `json:"expires_at"`
}
