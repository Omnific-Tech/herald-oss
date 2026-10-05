package server

import (
	"database/sql"
	"net/http"

	"github.com/Omnific-Tech/herald/internal/config"
	"github.com/Omnific-Tech/herald/internal/middleware"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
)

type api struct {
	cfg          config.Config
	version      string
	db           *sql.DB
	store        store.Store
	logger       zerolog.Logger
	loginLimiter *loginLimiter
}

// register wires the public delivery API and private admin control plane.
func (a *api) register(router *gin.Engine) {
	router.GET("/health", a.health)

	admin := router.Group("/api")
	{
		admin.POST("/session", a.createAdminSession)
		admin.DELETE("/session", a.deleteAdminSession)

		authenticated := admin.Group("", a.requireAdminSession())
		authenticated.GET("/session", a.getAdminSession)
		authenticated.GET("/health", a.health)
		authenticated.GET("/applications", a.listAdminApplications)
		authenticated.POST("/applications", a.createAdminApplication)
		authenticated.GET("/applications/:slug/producers", a.listAdminProducers)
		authenticated.POST("/applications/:slug/producers", a.createAdminProducer)
		authenticated.POST("/applications/:slug/producers/:id/revoke", a.revokeAdminProducer)
		authenticated.POST("/applications/:slug/messages", a.publishAdminMessage)
		authenticated.GET("/applications/:slug/messages", a.listAdminMessageHistory)
		authenticated.GET("/applications/:slug/routing", a.adminRouting)
		authenticated.POST("/applications/:slug/topics", a.createAdminTopic)
		authenticated.PATCH("/applications/:slug/topics/:id", a.renameAdminTopic)
		authenticated.POST("/applications/:slug/topics/:id/archive", a.archiveAdminTopic)
	}

	v1 := router.Group("/v1")
	{
		v1.GET("/applications/:slug/vapid-public-key", a.vapidPublicKey)

		// Open, because this is how a client obtains its first token. A device
		// token may be presented to rotate that device's own credential.
		v1.POST("/devices/register", a.registerDevice)

		producer := v1.Group("", middleware.RequireBearer())
		producer.POST("/messages", a.publishMessage)

		device := v1.Group("", middleware.RequireBearer())
		device.PUT("/subscriptions", a.updateSubscriptions)
		device.PUT("/device/fcm", a.updateFCM)
		device.POST("/deliveries/:delivery_id/ack", a.acknowledgeDelivery)
	}

	router.NoRoute(func(c *gin.Context) {
		c.JSON(http.StatusNotFound, gin.H{"error": "not found"})
	})
}
