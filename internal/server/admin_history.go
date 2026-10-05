package server

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
)

type historyCursor struct {
	CreatedAt time.Time `json:"created_at"`
	ID        string    `json:"id"`
}

func (a *api) listAdminMessageHistory(c *gin.Context) {
	application, err := a.store.Applications().BySlug(c.Request.Context(), c.Param("slug"))
	if errors.Is(err, store.ErrNotFound) {
		a.adminError(c, http.StatusNotFound, "application_not_found", "application was not found", nil)
		return
	}
	if err != nil {
		a.internalAdminError(c, err, "read history application")
		return
	}

	limit := 20
	if raw := c.Query("limit"); raw != "" {
		limit, err = strconv.Atoi(raw)
		if err != nil || limit < 1 || limit > 100 {
			a.adminError(c, http.StatusBadRequest, "invalid_query", "limit must be between 1 and 100", nil)
			return
		}
	}
	var beforeAt *time.Time
	var beforeID string
	if raw := c.Query("cursor"); raw != "" {
		cursor, err := decodeHistoryCursor(raw)
		if err != nil {
			a.adminError(c, http.StatusBadRequest, "invalid_query", "invalid history cursor", nil)
			return
		}
		beforeAt, beforeID = &cursor.CreatedAt, cursor.ID
	}

	messages, err := a.store.Messages().ListHistory(c.Request.Context(), application.ID, beforeAt, beforeID, limit+1)
	if err != nil {
		a.internalAdminError(c, err, "list admin message history")
		return
	}
	var nextCursor *string
	if len(messages) > limit {
		messages = messages[:limit]
		last := messages[len(messages)-1]
		cursor, err := encodeHistoryCursor(historyCursor{CreatedAt: last.CreatedAt, ID: last.ID})
		if err != nil {
			a.internalAdminError(c, err, "encode history cursor")
			return
		}
		nextCursor = &cursor
	}
	c.JSON(http.StatusOK, gin.H{"messages": messages, "next_cursor": nextCursor})
}

func encodeHistoryCursor(cursor historyCursor) (string, error) {
	payload, err := json.Marshal(cursor)
	if err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(payload), nil
}

func decodeHistoryCursor(raw string) (historyCursor, error) {
	if len(raw) > 512 {
		return historyCursor{}, errors.New("cursor is too long")
	}
	payload, err := base64.RawURLEncoding.DecodeString(raw)
	if err != nil {
		return historyCursor{}, err
	}
	var cursor historyCursor
	if err := json.Unmarshal(payload, &cursor); err != nil {
		return historyCursor{}, err
	}
	if cursor.CreatedAt.IsZero() {
		return historyCursor{}, errors.New("missing cursor timestamp")
	}
	if _, err := uuid.Parse(cursor.ID); err != nil {
		return historyCursor{}, err
	}
	return cursor, nil
}
