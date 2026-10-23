package middleware

import (
	"time"

	"github.com/gin-gonic/gin"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/observability/errs"
)

const (
	codeLookupLimit  = 30
	codeLookupWindow = time.Minute
)

var errCodeLookupLimited = errx.New(errx.TooManyRequests, "Too many pairing code lookups. Wait a minute and check the code.")

// CodeLookupBudget charges each pairing-code lookup to the signed-in user
// before the code is read. A cache error refuses with a retryable 503.
func (h *Handler) CodeLookupBudget(name string) gin.HandlerFunc {
	return func(c *gin.Context) {
		userID := GetUserID(c)
		if h.Cache == nil || userID == "" {
			errx.Handle(c, errx.ErrServiceDown)
			c.Abort()
			return
		}
		ok, err := h.Cache.ReserveAttempt(c.Request.Context(), "code_lookup:"+name+":"+userID, codeLookupLimit, codeLookupWindow)
		if err != nil {
			errs.CaptureException(err)
			errx.Handle(c, errx.ErrServiceDown)
			c.Abort()
			return
		}
		if !ok {
			errx.Handle(c, errCodeLookupLimited)
			c.Abort()
			return
		}
		c.Next()
	}
}
