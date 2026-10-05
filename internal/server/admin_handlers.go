package server

import (
	"crypto/subtle"
	"errors"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Omnific-Tech/herald/internal/crypto"
	"github.com/Omnific-Tech/herald/internal/model"
	"github.com/Omnific-Tech/herald/internal/store"
	"github.com/gin-gonic/gin"
	"github.com/go-playground/validator/v10"
	"github.com/google/uuid"
)

const (
	adminCookieName = "__Host-herald_admin"
	adminSessionKey = "admin_session"
)

var applicationSlugPattern = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)

type loginLimiter struct {
	mu       sync.Mutex
	failures []time.Time
}

func newLoginLimiter() *loginLimiter { return &loginLimiter{} }

// begin reserves one attempt before Argon2 work starts. Reserving first keeps a
// parallel burst from passing the limit while every request is still hashing.
func (l *loginLimiter) begin(now time.Time, maximum int, window time.Duration) (bool, time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.prune(now.Add(-window))
	if len(l.failures) >= maximum {
		return false, l.failures[0].Add(window).Sub(now)
	}
	l.failures = append(l.failures, now)
	return true, 0
}

func (l *loginLimiter) success() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.failures = nil
}

func (l *loginLimiter) prune(cutoff time.Time) {
	first := 0
	for first < len(l.failures) && !l.failures[first].After(cutoff) {
		first++
	}
	l.failures = l.failures[first:]
}

func (a *api) createAdminSession(c *gin.Context) {
	if !a.adminReady(c) || !a.requireAdminMutation(c, true) {
		return
	}
	now := time.Now()
	allowed, retryAfter := a.loginLimiter.begin(now, a.cfg.Admin.LoginMaxAttempts, a.cfg.Admin.LoginWindow)
	if !allowed {
		seconds := int(retryAfter.Seconds())
		if seconds < 1 {
			seconds = 1
		}
		c.Header("Retry-After", strconv.Itoa(seconds))
		a.adminError(c, http.StatusTooManyRequests, "rate_limited", "too many login attempts", nil)
		return
	}

	var request model.AdminLoginRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		a.adminError(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password", nil)
		return
	}
	if len(request.Username) > 200 || len(request.Password) > 1024 {
		a.adminError(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password", nil)
		return
	}
	passwordMatches, err := crypto.VerifyPassword(request.Password, a.cfg.Admin.PasswordHash)
	if err != nil {
		a.internalAdminError(c, err, "verify admin password")
		return
	}
	usernameMatches := subtle.ConstantTimeCompare([]byte(request.Username), []byte(a.cfg.Admin.Username)) == 1
	if !passwordMatches || !usernameMatches {
		a.adminError(c, http.StatusUnauthorized, "invalid_credentials", "invalid username or password", nil)
		return
	}

	token, err := crypto.NewToken()
	if err != nil {
		a.internalAdminError(c, err, "generate admin session")
		return
	}
	session, err := a.store.Administration().CreateSession(c.Request.Context(), a.cfg.Admin.Username, crypto.HashToken(token), a.cfg.Admin.SessionTTL)
	if err != nil {
		a.internalAdminError(c, err, "create admin session")
		return
	}
	a.loginLimiter.success()
	a.setAdminCookie(c, token, session.ExpiresAt)
	c.JSON(http.StatusOK, session)
}

func (a *api) getAdminSession(c *gin.Context) {
	c.JSON(http.StatusOK, c.MustGet(adminSessionKey).(model.AdminSession))
}

func (a *api) deleteAdminSession(c *gin.Context) {
	if !a.adminReady(c) || !a.requireAdminMutation(c, false) {
		return
	}
	if token, err := c.Cookie(adminCookieName); err == nil && token != "" {
		if err := a.store.Administration().DeleteSession(c.Request.Context(), crypto.HashToken(token)); err != nil {
			a.internalAdminError(c, err, "delete admin session")
			return
		}
	}
	a.clearAdminCookie(c)
	c.Status(http.StatusNoContent)
}

func (a *api) listAdminApplications(c *gin.Context) {
	applications, err := a.store.Administration().ListApplications(c.Request.Context())
	if err != nil {
		a.internalAdminError(c, err, "list admin applications")
		return
	}
	c.JSON(http.StatusOK, model.AdminApplicationsResponse{Applications: applications})
}

func (a *api) createAdminApplication(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	var request model.AdminCreateApplicationRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "application fields are invalid", map[string]string{"request": "valid JSON is required"})
		return
	}
	request.Slug = strings.TrimSpace(request.Slug)
	request.DisplayName = strings.TrimSpace(request.DisplayName)
	request.Origin = strings.TrimSpace(request.Origin)
	request.VAPIDSubject = strings.TrimSpace(request.VAPIDSubject)
	if fields := validateAdminApplication(request); len(fields) != 0 {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "application fields are invalid", fields)
		return
	}

	application, publicKey, err := a.store.Administration().CreateApplication(
		c.Request.Context(), request.Slug, request.DisplayName, request.Origin, request.VAPIDSubject,
	)
	if errors.Is(err, store.ErrConflict) {
		a.adminError(c, http.StatusConflict, "slug_taken", "application slug is already in use", nil)
		return
	}
	if err != nil {
		a.internalAdminError(c, err, "create admin application")
		return
	}
	c.JSON(http.StatusCreated, model.AdminCreateApplicationResponse{Application: application, VAPIDPublicKey: publicKey})
}

func (a *api) listAdminProducers(c *gin.Context) {
	producers, err := a.store.Administration().ListApplicationProducers(c.Request.Context(), c.Param("slug"))
	if errors.Is(err, store.ErrNotFound) {
		a.adminError(c, http.StatusNotFound, "application_not_found", "application was not found", nil)
		return
	}
	if err != nil {
		a.internalAdminError(c, err, "list admin producers")
		return
	}
	response := make([]model.AdminProducer, 0, len(producers))
	for _, producer := range producers {
		response = append(response, model.NewAdminProducer(producer))
	}
	c.JSON(http.StatusOK, model.AdminProducersResponse{Producers: response})
}

func (a *api) createAdminProducer(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	var request model.AdminCreateProducerRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "producer fields are invalid", map[string]string{"request": "valid JSON is required"})
		return
	}
	request.Name = strings.TrimSpace(request.Name)
	if request.Name == "" || len(request.Name) > 100 {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "producer fields are invalid", map[string]string{"name": "must be between 1 and 100 characters"})
		return
	}

	producer, token, err := a.store.Administration().CreateProducer(c.Request.Context(), c.Param("slug"), request.Name)
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.adminError(c, http.StatusNotFound, "application_not_found", "application was not found", nil)
		return
	case errors.Is(err, store.ErrConflict):
		a.adminError(c, http.StatusConflict, "name_taken", "an active producer already uses this name", nil)
		return
	case err != nil:
		a.internalAdminError(c, err, "create admin producer")
		return
	}
	c.JSON(http.StatusCreated, model.AdminCreateProducerResponse{Producer: model.NewAdminProducer(producer), Token: token})
}

func (a *api) revokeAdminProducer(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	producerID := c.Param("id")
	if _, err := uuid.Parse(producerID); err != nil {
		a.adminError(c, http.StatusNotFound, "producer_not_found", "producer was not found", nil)
		return
	}
	producer, err := a.store.Administration().RevokeProducerByID(c.Request.Context(), c.Param("slug"), producerID)
	switch {
	case errors.Is(err, store.ErrNotFound):
		a.adminError(c, http.StatusNotFound, "producer_not_found", "producer was not found", nil)
		return
	case errors.Is(err, store.ErrAlreadyRevoked):
		a.adminError(c, http.StatusConflict, "already_revoked", "producer is already revoked", nil)
		return
	case err != nil:
		a.internalAdminError(c, err, "revoke admin producer")
		return
	}
	c.JSON(http.StatusOK, model.AdminProducerResponse{Producer: model.NewAdminProducer(producer)})
}

func (a *api) publishAdminMessage(c *gin.Context) {
	if !a.requireAdminMutation(c, true) {
		return
	}
	var request model.PublishRequest
	if err := c.ShouldBindJSON(&request); err != nil {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "message fields are invalid", publishBindingFields(err))
		return
	}
	if field, message := validatePublishRequest(request); message != "" {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "message fields are invalid", map[string]string{field: message})
		return
	}

	application, err := a.store.Applications().BySlug(c.Request.Context(), c.Param("slug"))
	if errors.Is(err, store.ErrNotFound) {
		a.adminError(c, http.StatusNotFound, "application_not_found", "application was not found", nil)
		return
	}
	if err != nil {
		a.internalAdminError(c, err, "read admin message application")
		return
	}
	session := c.MustGet(adminSessionKey).(model.AdminSession)
	message, deduplicated, deviceCount, err := a.store.Messages().PublishAsAdmin(c.Request.Context(), application, session.Username, request)
	if errors.Is(err, store.ErrInvalidChannel) {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "message fields are invalid", map[string]string{"channel": "unknown channel"})
		return
	}
	if errors.Is(err, store.ErrInvalidTopic) {
		a.adminError(c, http.StatusUnprocessableEntity, "invalid_fields", "message fields are invalid", map[string]string{"topic": "unknown or archived topic"})
		return
	}
	if err != nil {
		a.internalAdminError(c, err, "publish admin message")
		return
	}
	c.JSON(http.StatusAccepted, model.PublishResponse{
		MessageID: message.ID, Deduplicated: deduplicated, DeviceCount: deviceCount,
	})
}

func publishBindingFields(err error) map[string]string {
	var validationErrors validator.ValidationErrors
	if !errors.As(err, &validationErrors) {
		return map[string]string{"request": "valid JSON is required"}
	}
	fields := make(map[string]string, len(validationErrors))
	for _, validationError := range validationErrors {
		name := map[string]string{
			"IdempotencyKey": "idempotency_key",
			"Channel":        "channel",
			"Topic":          "topic",
			"Priority":       "priority",
			"Title":          "title",
			"Body":           "body",
			"Link":           "link",
			"CollapseKey":    "collapse_key",
			"ExpiresAt":      "expires_at",
		}[validationError.Field()]
		if name == "" {
			name = "request"
		}
		switch validationError.Tag() {
		case "required":
			fields[name] = "is required"
		case "max":
			fields[name] = "exceeds the maximum length"
		case "oneof":
			fields[name] = "must be normal or high"
		case "herald_path":
			fields[name] = "must be an application-relative path"
		default:
			fields[name] = "is invalid"
		}
	}
	return fields
}

func (a *api) requireAdminSession() gin.HandlerFunc {
	return func(c *gin.Context) {
		if !a.adminReady(c) {
			c.Abort()
			return
		}
		token, err := c.Cookie(adminCookieName)
		if err != nil || token == "" {
			a.adminError(c, http.StatusUnauthorized, "unauthenticated", "admin session is missing or expired", nil)
			c.Abort()
			return
		}
		session, err := a.store.Administration().SessionByTokenHash(c.Request.Context(), crypto.HashToken(token))
		if errors.Is(err, store.ErrNotFound) {
			a.clearAdminCookie(c)
			a.adminError(c, http.StatusUnauthorized, "unauthenticated", "admin session is missing or expired", nil)
			c.Abort()
			return
		}
		if err != nil {
			a.internalAdminError(c, err, "resolve admin session")
			c.Abort()
			return
		}
		c.Set(adminSessionKey, session)
		c.Next()
	}
}

func (a *api) adminReady(c *gin.Context) bool {
	if a.store == nil || !a.cfg.Admin.Enabled() {
		a.adminError(c, http.StatusServiceUnavailable, "admin_unavailable", "admin control plane is not configured", nil)
		return false
	}
	return true
}

func (a *api) requireAdminMutation(c *gin.Context, jsonBody bool) bool {
	if c.GetHeader("Origin") != a.cfg.Admin.Origin {
		a.adminError(c, http.StatusForbidden, "origin_rejected", "request origin is not allowed", nil)
		return false
	}
	if jsonBody && c.ContentType() != "application/json" {
		a.adminError(c, http.StatusUnsupportedMediaType, "unsupported_media_type", "Content-Type must be application/json", nil)
		return false
	}
	return true
}

func (a *api) setAdminCookie(c *gin.Context, token string, expiresAt time.Time) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: adminCookieName, Value: token, Path: "/", Expires: expiresAt,
		MaxAge: int(time.Until(expiresAt).Seconds()), HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func (a *api) clearAdminCookie(c *gin.Context) {
	http.SetCookie(c.Writer, &http.Cookie{
		Name: adminCookieName, Path: "/", Expires: time.Unix(1, 0),
		MaxAge: -1, HttpOnly: true, Secure: true, SameSite: http.SameSiteStrictMode,
	})
}

func (a *api) adminError(c *gin.Context, status int, code, message string, fields map[string]string) {
	c.JSON(status, model.AdminErrorResponse{Error: message, Code: code, Fields: fields})
}

func (a *api) internalAdminError(c *gin.Context, err error, action string) {
	a.logger.Error().Err(err).Str("request_id", c.GetString("request_id")).Msg(action)
	a.adminError(c, http.StatusInternalServerError, "internal_error", "internal server error", nil)
}

func validateAdminApplication(request model.AdminCreateApplicationRequest) map[string]string {
	fields := map[string]string{}
	if len(request.Slug) > 64 || !applicationSlugPattern.MatchString(request.Slug) {
		fields["slug"] = "must use lowercase letters, numbers, and single hyphens"
	}
	if request.DisplayName == "" || len(request.DisplayName) > 200 {
		fields["display_name"] = "must be between 1 and 200 characters"
	}
	origin, err := url.Parse(request.Origin)
	if err != nil || origin.Scheme != "https" || origin.Hostname() == "" || origin.User != nil || (origin.Path != "" && origin.Path != "/") || origin.RawQuery != "" || origin.Fragment != "" {
		fields["origin"] = "must be an HTTPS origin without a path, query, or fragment"
	}
	subject, err := url.Parse(request.VAPIDSubject)
	if err != nil || !validVAPIDSubject(subject) {
		fields["vapid_subject"] = "must be a mailto: or HTTPS URL"
	}
	return fields
}

func validVAPIDSubject(subject *url.URL) bool {
	switch subject.Scheme {
	case "mailto":
		return subject.Opaque != "" && strings.Contains(subject.Opaque, "@")
	case "https":
		return subject.Hostname() != "" && subject.User == nil
	default:
		return false
	}
}
