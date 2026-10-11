package handler

import (
	"errors"
	"io"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/clay"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

func (h *Handler) clayReady(c *gin.Context) (orgID, userID, connID uuid.UUID, ok bool) {
	if h.ClayService == nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "Clay is not available on this instance"))
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	orgID, userID, ok = h.requireIntegrationActor(c, false)
	if !ok {
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	connID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid connection id"))
		return uuid.Nil, uuid.Nil, uuid.Nil, false
	}
	return orgID, userID, connID, true
}

func clayFail(c *gin.Context, err error, what string) {
	var xe *errx.Error
	if errors.As(err, &xe) {
		errx.JSON(c, xe)
		return
	}
	log.Error().Err(err).Str("op", what).Msg("clay request failed")
	errx.JSON(c, errx.InternalError())
}

// GetClaySettings is everything the Clay page shows.
func (h *Handler) GetClaySettings(c *gin.Context) {
	orgID, _, connID, ok := h.clayReady(c)
	if !ok {
		return
	}
	out, err := h.ClayService.Settings(c.Request.Context(), orgID, connID)
	if err != nil {
		clayFail(c, err, "settings")
		return
	}
	c.JSON(http.StatusOK, out)
}

// UpdateClaySettings saves the inbound defaults, the outbound table and the
// events sent to it. Every field replaces its value, so a retry converges.
func (h *Handler) UpdateClaySettings(c *gin.Context) {
	orgID, userID, connID, ok := h.clayReady(c)
	if !ok {
		return
	}
	var in models.ClaySettingsUpdate
	if err := c.ShouldBindJSON(&in); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	out, err := h.ClayService.UpdateSettings(c.Request.Context(), orgID, connID, in)
	if err != nil {
		clayFail(c, err, "update settings")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionUpdate, connID, "clay:settings")
	c.JSON(http.StatusOK, out)
}

// InboundClay takes rows from a Clay HTTP API column. The secret in the path
// is the credential. Rows are upserted by address, so Clay's retries converge.
func (h *Handler) InboundClay(c *gin.Context) {
	if h.ClayService == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"status": "failed", "error": "Clay is not available on this instance"})
		return
	}
	secret := strings.TrimSpace(c.Param("secret"))
	if secret == "" {
		c.JSON(http.StatusNotFound, gin.H{"status": "failed", "error": "unknown Clay inbound URL"})
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, clay.MaxBody+1))
	if err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"status": "failed", "error": "could not read the body"})
		return
	}
	if len(body) > clay.MaxBody {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"status": "failed", "error": "the body is larger than 1 MB"})
		return
	}
	out, err := h.ClayService.Receive(c.Request.Context(), secret, body)
	if err != nil {
		var xe *errx.Error
		switch {
		case errors.Is(err, clay.ErrUnknownURL):
			c.JSON(http.StatusNotFound, gin.H{"status": "failed", "error": err.Error()})
		case errors.Is(err, clay.ErrNotActive):
			c.JSON(http.StatusForbidden, gin.H{"status": "failed", "error": err.Error()})
		case errors.Is(err, clay.ErrRateLimited):
			c.Header("Retry-After", "60")
			c.JSON(http.StatusTooManyRequests, gin.H{"status": "failed", "error": err.Error()})
		case errors.As(err, &xe) && xe.Code < errx.Internal:
			c.JSON(int(xe.Code), gin.H{"status": "failed", "error": xe.Message})
		default:
			log.Error().Err(err).Msg("clay inbound failed")
			c.JSON(http.StatusInternalServerError, gin.H{"status": "failed", "error": "Warmbly could not save these rows. Run them again."})
		}
		return
	}
	c.JSON(http.StatusOK, out)
}
