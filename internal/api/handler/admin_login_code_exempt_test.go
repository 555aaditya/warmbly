package handler

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/api/middleware"
	"github.com/warmbly/warmbly/internal/repository"
)

type exemptionUsers struct {
	repository.UserRepository
	missing bool
	granted bool
	reason  string
	by      *uuid.UUID
	revoked bool
}

func (r *exemptionUsers) SetLoginCodeExempt(_ context.Context, _ uuid.UUID, exempt bool, reason string, by *uuid.UUID) error {
	if r.missing {
		return repository.ErrUserNotFound
	}
	r.granted, r.reason, r.by = exempt, reason, by
	return nil
}

func (r *exemptionUsers) RevokeTester(context.Context, uuid.UUID) (bool, error) {
	if r.missing {
		return false, repository.ErrUserNotFound
	}
	r.revoked = true
	return false, nil
}

func TestAdminSetLoginCodeExempt(t *testing.T) {
	gin.SetMode(gin.TestMode)
	actor, target := uuid.New(), uuid.New()
	cases := []struct {
		name    string
		id      string
		body    string
		missing bool
		status  int
		granted bool
		revoked bool
	}{
		{name: "grant", id: target.String(), body: `{"exempt":true,"reason":" App Store review "}`, status: http.StatusOK, granted: true},
		{name: "grant without reason", id: target.String(), body: `{"exempt":true,"reason":"  "}`, status: http.StatusBadRequest},
		{name: "reason too long", id: target.String(), body: `{"exempt":true,"reason":"` + strings.Repeat("x", loginCodeExemptReasonMax+1) + `"}`, status: http.StatusBadRequest},
		{name: "missing exempt", id: target.String(), body: `{"reason":"x"}`, status: http.StatusBadRequest},
		{name: "bad id", id: "nope", body: `{"exempt":true,"reason":"x"}`, status: http.StatusBadRequest},
		{name: "unknown account", id: target.String(), body: `{"exempt":true,"reason":"x"}`, missing: true, status: http.StatusNotFound},
		{name: "clear", id: target.String(), body: `{"exempt":false}`, status: http.StatusOK, revoked: true},
		{name: "clear unknown account", id: target.String(), body: `{"exempt":false}`, missing: true, status: http.StatusNotFound},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			users := &exemptionUsers{missing: tc.missing}
			h := &Handler{UserRepo: users}
			r := gin.New()
			r.Use(func(c *gin.Context) { c.Set(middleware.AdminUserIDKey, actor) })
			r.PUT("/users/:id/login-code-exempt", h.AdminSetLoginCodeExempt)
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/users/"+tc.id+"/login-code-exempt", strings.NewReader(tc.body)))
			if w.Code != tc.status {
				t.Fatalf("HTTP %d, want %d: %s", w.Code, tc.status, w.Body.String())
			}
			if users.granted != tc.granted || users.revoked != tc.revoked {
				t.Fatalf("granted=%v revoked=%v, want %v %v", users.granted, users.revoked, tc.granted, tc.revoked)
			}
			if tc.granted && (users.reason != "App Store review" || users.by == nil || *users.by != actor) {
				t.Fatalf("grant recorded reason %q by %v", users.reason, users.by)
			}
		})
	}
}

func TestAdminSetLoginCodeExemptNeedsAnActor(t *testing.T) {
	gin.SetMode(gin.TestMode)
	h := &Handler{UserRepo: &exemptionUsers{}}
	r := gin.New()
	r.PUT("/users/:id/login-code-exempt", h.AdminSetLoginCodeExempt)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, httptest.NewRequest(http.MethodPut, "/users/"+uuid.NewString()+"/login-code-exempt", strings.NewReader(`{"exempt":true,"reason":"x"}`)))
	if w.Code != http.StatusUnauthorized {
		t.Fatalf("HTTP %d, want 401", w.Code)
	}
}
