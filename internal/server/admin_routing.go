package server

import (
	"errors"
	"net/http"
	"regexp"
	"strings"

	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/gin-gonic/gin"
)

var topicIDPattern = regexp.MustCompile(`^[a-z0-9]+(?:[._-][a-z0-9]+)*$`)

type createTopicRequest struct {
	ID      string `json:"id"`
	Label   string `json:"label"`
	Channel string `json:"channel"`
}

type renameTopicRequest struct {
	Label string `json:"label"`
}

func (a *api) adminRouting(c *gin.Context) {
	application, err := a.store.Applications().BySlug(c.Request.Context(), c.Param("slug"))
	if errors.Is(err, store.ErrNotFound) {
		a.adminError(c, http.StatusNotFound, "application_not_found", "application was not found", nil)
		return
	}
	if err != nil {
		a.internalAdminError(c, err, "read routing application")
		return
	}
	topics, err := a.store.Routing().ListTopics(c.Request.Context(), application.ID)
	if err != nil {
		a.internalAdminError(c, err, "list routing topics")
		return
	}
	c.JSON(http.StatusOK, gin.H{"channels": model.RoutingChannels, "topics": topics})
}

func (a *api) createAdminTopic(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	var request createTopicRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "topic fields are invalid", map[string]string{"request": "valid JSON is required"})
		return
	}
	request.ID = strings.TrimSpace(request.ID)
	request.Label = strings.TrimSpace(request.Label)
	if fields := validateTopic(request); len(fields) != 0 {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "topic fields are invalid", fields)
		return
	}
	topic, err := a.store.Routing().CreateTopic(c.Request.Context(), c.Param("slug"), model.RoutingTopic{
		ID: request.ID, Label: request.Label, Channel: request.Channel,
	})
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.adminError(c, http.StatusNotFound, "application_not_found", "application was not found", nil)
	case errors.Is(err, store.ErrConflict):
		a.adminError(c, http.StatusConflict, "topic_exists", "topic ID is already in use", nil)
	case err != nil:
		a.internalAdminError(c, err, "create routing topic")
	default:
		c.JSON(http.StatusCreated, gin.H{"topic": topic})
	}
}

func (a *api) renameAdminTopic(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	var request renameTopicRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "topic fields are invalid", map[string]string{"request": "valid JSON is required"})
		return
	}
	request.Label = strings.TrimSpace(request.Label)
	if request.Label == "" || len(request.Label) > 100 {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "topic fields are invalid", map[string]string{"label": "must be between 1 and 100 characters"})
		return
	}
	topic, err := a.store.Routing().RenameTopic(c.Request.Context(), c.Param("slug"), c.Param("id"), request.Label)
	if errors.Is(err, store.ErrNotFound) {
		a.adminError(c, http.StatusNotFound, "topic_not_found", "topic was not found", nil)
	} else if errors.Is(err, store.ErrAlreadyArchived) {
		a.adminError(c, http.StatusConflict, "already_archived", "topic is already archived", nil)
	} else if err != nil {
		a.internalAdminError(c, err, "rename routing topic")
	} else {
		c.JSON(http.StatusOK, gin.H{"topic": topic})
	}
}

func (a *api) archiveAdminTopic(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	topic, err := a.store.Routing().ArchiveTopic(c.Request.Context(), c.Param("slug"), c.Param("id"))
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.adminError(c, http.StatusNotFound, "topic_not_found", "topic was not found", nil)
	case errors.Is(err, store.ErrAlreadyArchived):
		a.adminError(c, http.StatusConflict, "already_archived", "topic is already archived", nil)
	case err != nil:
		a.internalAdminError(c, err, "archive routing topic")
	default:
		c.JSON(http.StatusOK, gin.H{"topic": topic})
	}
}

func validateTopic(topic createTopicRequest) map[string]string {
	fields := map[string]string{}
	if len(topic.ID) > 200 || !topicIDPattern.MatchString(topic.ID) {
		fields["id"] = "must use lowercase letters, numbers, dots, underscores, and hyphens"
	}
	if topic.Label == "" || len(topic.Label) > 100 {
		fields["label"] = "must be between 1 and 100 characters"
	}
	if !model.ValidChannel(topic.Channel) {
		fields["channel"] = "unknown channel"
	}
	return fields
}
