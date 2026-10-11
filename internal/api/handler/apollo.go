package handler

import (
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/rs/zerolog/log"

	"github.com/warmbly/warmbly/internal/app/apollo"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
)

// apolloReady resolves the org, the actor and the connection id, or answers
// the request itself.
func (h *Handler) apolloReady(c *gin.Context) (orgID, userID, connID uuid.UUID, ok bool) {
	if h.ApolloService == nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "Apollo is not available on this instance"))
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

// apolloFail answers an Apollo error: the service's own refusals as they are,
// anything else as an internal error with the cause logged.
func apolloFail(c *gin.Context, err error, what string) {
	var xe *errx.Error
	if errors.As(err, &xe) {
		errx.JSON(c, xe)
		return
	}
	log.Error().Err(err).Str("op", what).Msg("apollo request failed")
	errx.JSON(c, errx.InternalError())
}

// apolloSource loads an import source and checks it belongs to the connection.
func (h *Handler) apolloSource(c *gin.Context, orgID, connID uuid.UUID) (uuid.UUID, bool) {
	id, err := uuid.Parse(c.Param("sourceId"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid import source id"))
		return uuid.Nil, false
	}
	src, err := h.ApolloService.Repo.GetSource(c.Request.Context(), orgID, id)
	if err != nil || src == nil || src.ConnectionID != connID {
		errx.JSON(c, errx.New(errx.NotFound, "import source not found"))
		return uuid.Nil, false
	}
	return id, true
}

// ApolloOverview is the Apollo page header.
func (h *Handler) ApolloOverview(c *gin.Context) {
	orgID, _, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	out, err := h.ApolloService.Overview(c.Request.Context(), orgID, connID)
	if err != nil {
		apolloFail(c, err, "overview")
		return
	}
	c.JSON(http.StatusOK, out)
}

// ReplaceApolloKey checks a new API key and swaps it in. A PUT, so a retry
// converges on the same key.
func (h *Handler) ReplaceApolloKey(c *gin.Context) {
	orgID, userID, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	var in struct {
		APIKey string `json:"api_key"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	out, err := h.ApolloService.ReplaceKey(c.Request.Context(), orgID, connID, in.APIKey)
	if err != nil {
		apolloFail(c, err, "replace key")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionUpdate, connID, "apollo:key")
	c.JSON(http.StatusOK, out)
}

// ApolloLists returns the account's saved contact lists.
func (h *Handler) ApolloLists(c *gin.Context) {
	orgID, _, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	out, err := h.ApolloService.Lists(c.Request.Context(), orgID, connID)
	if err != nil {
		apolloFail(c, err, "lists")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// PreviewApolloImport reads the first people of a list. Read-only, so retries
// are naturally safe.
func (h *Handler) PreviewApolloImport(c *gin.Context) {
	orgID, _, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	var in struct {
		ListID       string `json:"list_id"`
		VerifiedOnly *bool  `json:"verified_only"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	out, err := h.ApolloService.Preview(c.Request.Context(), orgID, connID, in.ListID, in.VerifiedOnly == nil || *in.VerifiedOnly)
	if err != nil {
		apolloFail(c, err, "preview")
		return
	}
	c.JSON(http.StatusOK, out)
}

// ListApolloImportSources lists a connection's saved list imports.
func (h *Handler) ListApolloImportSources(c *gin.Context) {
	orgID, _, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	out, err := h.ApolloService.Repo.ListSources(c.Request.Context(), orgID, connID)
	if err != nil {
		apolloFail(c, err, "list sources")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// CreateApolloImportSource saves a list import and starts its first run. A
// retried create makes a second source whose run imports nothing new: people
// are deduplicated by address, so the retry is harmless.
func (h *Handler) CreateApolloImportSource(c *gin.Context) {
	orgID, userID, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	var in apollo.SourceInput
	if err := c.ShouldBindJSON(&in); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	out, err := h.ApolloService.CreateSource(c.Request.Context(), orgID, connID, userID, in)
	if err != nil {
		apolloFail(c, err, "create source")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionCreate, connID, "apollo:import:"+out.ID.String())
	c.JSON(http.StatusCreated, out)
}

// UpdateApolloImportSource edits a list import's targets, filter and schedule.
func (h *Handler) UpdateApolloImportSource(c *gin.Context) {
	orgID, userID, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	id, ok := h.apolloSource(c, orgID, connID)
	if !ok {
		return
	}
	raw, err := c.GetRawData()
	if err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	var in apollo.SourceInput
	if err := json.Unmarshal(raw, &in); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	// campaign_id: null clears the target; absent leaves it.
	var probe map[string]json.RawMessage
	if json.Unmarshal(raw, &probe) == nil {
		if v, ok := probe["campaign_id"]; ok && strings.TrimSpace(string(v)) == "null" {
			in.ClearCampaign = true
		}
	}
	out, err := h.ApolloService.UpdateSource(c.Request.Context(), orgID, id, in)
	if err != nil {
		apolloFail(c, err, "update source")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionUpdate, connID, "apollo:import:"+id.String())
	c.JSON(http.StatusOK, out)
}

// RunApolloImportSource runs a list import now. A second request while one
// runs is refused with 409, so retries cannot double-run it.
func (h *Handler) RunApolloImportSource(c *gin.Context) {
	orgID, userID, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	id, ok := h.apolloSource(c, orgID, connID)
	if !ok {
		return
	}
	out, err := h.ApolloService.StartRun(c.Request.Context(), orgID, id)
	if err != nil {
		apolloFail(c, err, "run source")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionUpdate, connID, "apollo:import-run:"+id.String())
	c.JSON(http.StatusOK, out)
}

// DeleteApolloImportSource removes a list import; contacts it brought in stay.
func (h *Handler) DeleteApolloImportSource(c *gin.Context) {
	orgID, userID, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	id, ok := h.apolloSource(c, orgID, connID)
	if !ok {
		return
	}
	if _, err := h.ApolloService.Repo.DeleteSource(c.Request.Context(), orgID, id); err != nil {
		apolloFail(c, err, "delete source")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionDelete, connID, "apollo:import:"+id.String())
	c.Status(http.StatusNoContent)
}

// ListApolloEnrichments lists a connection's recent enrichment runs.
func (h *Handler) ListApolloEnrichments(c *gin.Context) {
	orgID, _, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	limit := 20
	if v := c.Query("limit"); v != "" {
		n, err := strconv.Atoi(v)
		if err != nil || n < 1 || n > 100 {
			errx.JSON(c, errx.New(errx.BadRequest, "limit must be between 1 and 100"))
			return
		}
		limit = n
	}
	out, err := h.ApolloService.Repo.ListEnrichmentRuns(c.Request.Context(), orgID, connID, limit)
	if err != nil {
		apolloFail(c, err, "list enrichments")
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": out})
}

// GetApolloEnrichment returns one enrichment run's progress.
func (h *Handler) GetApolloEnrichment(c *gin.Context) {
	orgID, _, connID, ok := h.apolloReady(c)
	if !ok {
		return
	}
	id, err := uuid.Parse(c.Param("runId"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid enrichment run id"))
		return
	}
	run, err := h.ApolloService.Repo.GetEnrichmentRun(c.Request.Context(), orgID, id)
	if err != nil {
		apolloFail(c, err, "get enrichment")
		return
	}
	if run == nil || run.ConnectionID != connID {
		errx.JSON(c, errx.New(errx.NotFound, "enrichment run not found"))
		return
	}
	c.JSON(http.StatusOK, run)
}

type apolloEnrichPayload struct {
	models.ContactSelection
	ContactIDs []string `json:"contact_ids"`
	Overwrite  bool     `json:"overwrite"`
}

// StartApolloEnrichment queues an enrichment run over a contact selection.
// Supports Idempotency-Key through the API middleware; without one a retry
// queues a second run, which only rewrites the same fields again.
func (h *Handler) StartApolloEnrichment(c *gin.Context) {
	if h.ApolloService == nil {
		errx.JSON(c, errx.New(errx.ServiceUnavailable, "Apollo is not available on this instance"))
		return
	}
	orgID, userID, ok := h.requireIntegrationActor(c, true)
	if !ok {
		return
	}
	connID, err := uuid.Parse(c.Param("id"))
	if err != nil {
		errx.JSON(c, errx.New(errx.BadRequest, "invalid connection id"))
		return
	}
	var p apolloEnrichPayload
	if err := c.ShouldBindJSON(&p); err != nil {
		errx.JSON(c, errx.InvalidBody(err))
		return
	}
	sel := p.ContactSelection
	if !sel.All && len(sel.Contacts) == 0 {
		sel.Contacts = p.ContactIDs
	}
	resolved, ok := h.resolveContactSelection(c, orgID, sel)
	if !ok {
		return
	}
	if len(resolved) > apollo.EnrichCap {
		errx.JSON(c, errx.NewWithIdentifier(errx.BadRequest, "apollo_enrich_too_many", "Enrich at most 5,000 contacts at a time"))
		return
	}
	ids := make([]uuid.UUID, 0, len(resolved))
	for _, raw := range resolved {
		id, perr := uuid.Parse(strings.TrimSpace(raw))
		if perr != nil {
			errx.JSON(c, errx.New(errx.BadRequest, "invalid contact id: "+raw))
			return
		}
		ids = append(ids, id)
	}
	run, err := h.ApolloService.StartEnrichment(c.Request.Context(), orgID, connID, userID, ids, p.Overwrite)
	if err != nil {
		apolloFail(c, err, "start enrichment")
		return
	}
	h.auditIntegration(c, userID, models.AuditActionCreate, connID, "apollo:enrich:"+run.ID.String())
	c.JSON(http.StatusAccepted, run)
}
