package server

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Omnific-Tech/herald/internal/middleware"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

// maxDataBytes caps the opaque product payload on a publish.
const maxDataBytes = 16 << 10

func (a *api) health(c *gin.Context) {
	status := gin.H{
		"status":  "ok",
		"service": a.cfg.Server.ServiceName,
		"version": a.version,
	}

	switch {
	case a.db == nil:
		status["status"] = "degraded"
		status["database"] = "not configured"
		c.JSON(http.StatusServiceUnavailable, status)
		return
	default:
		ctx, cancel := context.WithTimeout(c.Request.Context(), 2*time.Second)
		defer cancel()

		if err := a.db.PingContext(ctx); err != nil {
			status["status"] = "degraded"
			status["database"] = "unreachable"
			c.JSON(http.StatusServiceUnavailable, status)
			return
		}
		status["database"] = "ok"
	}

	c.JSON(http.StatusOK, status)
}

func (a *api) vapidPublicKey(c *gin.Context) {
	if !a.storageReady(c) {
		return
	}

	slug := c.Param("slug")
	key, err := a.store.Applications().VAPIDPublicKey(c.Request.Context(), slug)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Error: "unknown application"})
		return
	}
	if err != nil {
		a.internalError(c, err, "read vapid public key")
		return
	}

	c.JSON(http.StatusOK, model.VAPIDPublicKeyResponse{Application: slug, PublicKey: key})
}

// registerDevice creates a device, or rotates the credential of the device
// whose own token is presented. A caller can never name another device.
func (a *api) registerDevice(c *gin.Context) {
	if !a.storageReady(c) {
		return
	}

	var request model.RegisterDeviceRequest
	if !bindJSON(c, &request) {
		return
	}

	// The schema enforces this too, but rejecting here gives the PWA a usable
	// error instead of a constraint violation.
	if request.Platform == model.PlatformWeb && !hasWebPushKeys(request.WebPush) {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "webpush endpoint and keys are required for platform web"})
		return
	}
	if request.Platform == model.PlatformWeb && !model.SafeWebPushEndpoint(request.WebPush.Endpoint) {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "webpush endpoint must be an https URL without user information"})
		return
	}
	if request.Platform == model.PlatformAndroid {
		if request.FCMFID == "" {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "fcm_fid is required for platform android"})
			return
		}
		request.WebPush = nil
	} else {
		request.FCMFID = ""
	}

	if token, present := middleware.ParseBearer(c); present {
		a.rotateDevice(c, token, request)
		return
	}
	a.createDevice(c, request)
}

func (a *api) createDevice(c *gin.Context, request model.RegisterDeviceRequest) {
	if request.Application == "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "application is required"})
		return
	}

	application, err := a.store.Applications().BySlug(c.Request.Context(), request.Application)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Error: "unknown application"})
		return
	}
	if err != nil {
		a.internalError(c, err, "read application")
		return
	}

	device, token, err := a.store.Devices().Create(c.Request.Context(), application.ID, request)
	if err != nil {
		a.internalError(c, err, "create device")
		return
	}

	c.JSON(http.StatusOK, model.RegisterDeviceResponse{
		DeviceID:    device.ID,
		InstallID:   device.InstallID,
		DeviceToken: token,
	})
}

func (a *api) rotateDevice(c *gin.Context, token string, request model.RegisterDeviceRequest) {
	existing, err := a.store.Devices().ByToken(c.Request.Context(), token)
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: "invalid device token"})
		return
	}
	if err != nil {
		a.internalError(c, err, "resolve device")
		return
	}

	device, rotated, err := a.store.Devices().Rotate(c.Request.Context(), existing.ID, request)
	if err != nil {
		a.internalError(c, err, "rotate device")
		return
	}

	acknowledged, err := a.store.Devices().AcknowledgedSequence(c.Request.Context(), device.ID)
	if err != nil {
		a.internalError(c, err, "read acknowledged sequence")
		return
	}

	c.JSON(http.StatusOK, model.RegisterDeviceResponse{
		DeviceID:             device.ID,
		InstallID:            device.InstallID,
		DeviceToken:          rotated,
		AcknowledgedSequence: acknowledged,
	})
}

func (a *api) publishMessage(c *gin.Context) {
	producer, ok := a.producerFromToken(c)
	if !ok {
		return
	}

	var request model.PublishRequest
	if !bindJSON(c, &request) {
		return
	}
	if _, message := validatePublishRequest(request); message != "" {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: message})
		return
	}

	message, deduplicated, deviceCount, err := a.store.Messages().Publish(c.Request.Context(), producer, request)
	if errors.Is(err, store.ErrInvalidChannel) {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "unknown channel"})
		return
	}
	if errors.Is(err, store.ErrInvalidTopic) {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "unknown or archived topic"})
		return
	}
	if err != nil {
		a.internalError(c, err, "publish message")
		return
	}

	c.JSON(http.StatusAccepted, model.PublishResponse{
		MessageID:    message.ID,
		Deduplicated: deduplicated,
		DeviceCount:  deviceCount,
	})
}

func validatePublishRequest(request model.PublishRequest) (field, message string) {
	if !request.ExpiresAt.After(time.Now()) {
		return "expires_at", "expires_at must be in the future"
	}
	if len(request.Data) > maxDataBytes {
		return "data", "data exceeds 16 KiB"
	}
	pushBytes, err := model.PushDeliverySize(request)
	if err != nil {
		return "data", "data must be valid JSON"
	}
	if pushBytes > model.MaxPushDeliveryBytes {
		return "request", "message exceeds the 3500-byte push delivery limit"
	}
	return "", ""
}

func (a *api) updateSubscriptions(c *gin.Context) {
	device, ok := a.deviceFromToken(c)
	if !ok {
		return
	}

	var request model.UpdateSubscriptionsRequest
	if !bindJSON(c, &request) {
		return
	}
	for _, subscription := range request.Subscriptions {
		if subscription.Channel == "" {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "every subscription needs a channel"})
			return
		}
	}

	if err := a.store.Devices().ReplaceSubscriptions(c.Request.Context(), device.ID, request.Subscriptions); err != nil {
		if errors.Is(err, store.ErrInvalidChannel) || errors.Is(err, store.ErrInvalidTopic) {
			c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "unknown channel or topic"})
			return
		}
		a.internalError(c, err, "replace subscriptions")
		return
	}

	subscriptions, err := a.store.Devices().Subscriptions(c.Request.Context(), device.ID)
	if err != nil {
		a.internalError(c, err, "read subscriptions")
		return
	}

	c.JSON(http.StatusOK, model.UpdateSubscriptionsRequest{Subscriptions: subscriptions})
}

func (a *api) updateFCM(c *gin.Context) {
	device, ok := a.deviceFromToken(c)
	if !ok {
		return
	}
	if device.Platform != model.PlatformAndroid {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: "fcm is only available for android devices"})
		return
	}
	var request model.UpdateFCMRequest
	if !bindJSON(c, &request) {
		return
	}
	if err := a.store.Devices().UpdateFCM(c.Request.Context(), device.ID, request.FCMFID); err != nil {
		a.internalError(c, err, "update fcm installation")
		return
	}
	c.Status(http.StatusNoContent)
}

func (a *api) acknowledgeDelivery(c *gin.Context) {
	device, ok := a.deviceFromToken(c)
	if !ok {
		return
	}

	deliveryID := c.Param("delivery_id")
	// Handing a non-UUID to the query raises a type error, not "no rows", which
	// would surface as a 500 and distinguish malformed ids from unknown ones.
	if _, err := uuid.Parse(deliveryID); err != nil {
		c.JSON(http.StatusNotFound, model.ErrorResponse{Error: "unknown delivery"})
		return
	}

	var request model.AckRequest
	if !bindJSON(c, &request) {
		return
	}

	acknowledgement, err := a.store.Deliveries().Acknowledge(c.Request.Context(), device.ID, deliveryID, request.Status)
	if errors.Is(err, store.ErrNotFound) {
		// Also the answer when the delivery belongs to another device: the
		// caller must not learn that it exists.
		c.JSON(http.StatusNotFound, model.ErrorResponse{Error: "unknown delivery"})
		return
	}
	if err != nil {
		a.internalError(c, err, "acknowledge delivery")
		return
	}

	c.JSON(http.StatusOK, acknowledgement)
}

func hasWebPushKeys(subscription *model.WebPushSubscription) bool {
	return subscription != nil &&
		subscription.Endpoint != "" &&
		subscription.P256DH != "" &&
		subscription.Auth != ""
}

func bindJSON(c *gin.Context, target any) bool {
	if err := c.ShouldBindJSON(target); err != nil {
		c.JSON(http.StatusBadRequest, model.ErrorResponse{Error: err.Error()})
		return false
	}
	return true
}

// storageReady guards the routes that cannot answer without a database.
func (a *api) storageReady(c *gin.Context) bool {
	if a.store == nil {
		c.JSON(http.StatusServiceUnavailable, model.ErrorResponse{Error: "storage is not configured"})
		return false
	}
	return true
}

func (a *api) producerFromToken(c *gin.Context) (model.Producer, bool) {
	if !a.storageReady(c) {
		return model.Producer{}, false
	}

	producer, err := a.store.Producers().ByToken(c.Request.Context(), middleware.BearerToken(c))
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: "invalid producer token"})
		return model.Producer{}, false
	}
	if err != nil {
		a.internalError(c, err, "resolve producer")
		return model.Producer{}, false
	}
	return producer, true
}

func (a *api) deviceFromToken(c *gin.Context) (model.Device, bool) {
	if !a.storageReady(c) {
		return model.Device{}, false
	}

	device, err := a.store.Devices().ByToken(c.Request.Context(), middleware.BearerToken(c))
	if errors.Is(err, store.ErrNotFound) {
		c.JSON(http.StatusUnauthorized, model.ErrorResponse{Error: "invalid device token"})
		return model.Device{}, false
	}
	if err != nil {
		a.internalError(c, err, "resolve device")
		return model.Device{}, false
	}
	return device, true
}

func (a *api) internalError(c *gin.Context, err error, action string) {
	a.logger.Error().Err(err).
		Str("request_id", c.GetString("request_id")).
		Msg(action)
	c.JSON(http.StatusInternalServerError, model.ErrorResponse{Error: "internal server error"})
}

func parseSequence(raw string) (int64, error) {
	if raw == "" {
		return 0, nil
	}
	return strconv.ParseInt(raw, 10, 64)
}
