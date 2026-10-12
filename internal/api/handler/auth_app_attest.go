package handler

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"github.com/warmbly/warmbly/internal/errx"
)

var errAppAttestOff = errx.NewWithIdentifier(errx.NotFound, "app_attest_unavailable", "This server does not accept app verification.")

// AppAttestChallenge mints the single-use challenge an attestation or assertion is made over.
func (h *Handler) AppAttestChallenge(c *gin.Context) {
	if !h.AppAttestService.Enabled() {
		errx.Handle(c, errAppAttestOff)
		return
	}
	challenge, xerr := h.AppAttestService.Challenge(c.Request.Context())
	if xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.JSON(http.StatusOK, gin.H{"challenge": challenge})
}

type appAttestKeyRequest struct {
	KeyID       string `json:"key_id"`
	Attestation string `json:"attestation"`
	Challenge   string `json:"challenge"`
}

// AppAttestRegisterKey stores an install's key once Apple's attestation of it verifies.
func (h *Handler) AppAttestRegisterKey(c *gin.Context) {
	if !h.AppAttestService.Enabled() {
		errx.Handle(c, errAppAttestOff)
		return
	}
	var req appAttestKeyRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		errx.Handle(c, errx.InvalidBody(err))
		return
	}
	if xerr := h.AppAttestService.RegisterKey(c.Request.Context(), req.KeyID, req.Attestation, req.Challenge); xerr != nil {
		errx.Handle(c, xerr)
		return
	}
	c.Status(http.StatusNoContent)
}
