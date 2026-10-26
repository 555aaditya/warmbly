package middleware

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/warmbly/warmbly/internal/app/token"
	"github.com/warmbly/warmbly/internal/models"
)

func TestRequireFreshAuth(t *testing.T) {
	gin.SetMode(gin.TestMode)
	fresh := time.Now().Add(-time.Minute)
	stale := time.Now().Add(-token.ReauthWindow - time.Minute)
	for _, tc := range []struct {
		name     string
		authType string
		session  *models.Session
		want     int
	}{
		{"api key", AuthTypeAPIKey, nil, http.StatusOK},
		{"oauth token", AuthTypeOAuth, nil, http.StatusOK},
		{"fresh session", AuthTypeJWT, &models.Session{ReauthAt: &fresh}, http.StatusOK},
		{"stale session", AuthTypeJWT, &models.Session{ReauthAt: &stale}, http.StatusForbidden},
		{"never confirmed", AuthTypeJWT, &models.Session{}, http.StatusForbidden},
		{"no session", AuthTypeJWT, nil, http.StatusUnauthorized},
	} {
		w := httptest.NewRecorder()
		c, r := gin.CreateTestContext(w)
		r.POST("/x", func(c *gin.Context) {
			c.Set(AuthTypeKey, tc.authType)
			if tc.session != nil {
				c.Set(SessionKey, tc.session)
			}
		}, RequireFreshAuth(), func(c *gin.Context) { c.Status(http.StatusOK) })
		c.Request = httptest.NewRequest(http.MethodPost, "/x", nil)
		r.HandleContext(c)
		if w.Code != tc.want || (tc.want != http.StatusOK && !strings.Contains(w.Body.String(), "reauth_required")) {
			t.Errorf("%s: %d %s", tc.name, w.Code, w.Body.String())
		}
	}
}
