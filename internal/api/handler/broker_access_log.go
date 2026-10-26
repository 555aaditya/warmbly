package handler

import (
	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog/log"
)

// Access logging for the two internal endpoints that hand out something worth
// more than a record: the data-key decrypt broker and the blob presigner.
//
// Every access to these server-side secrets is logged. They are also the two where a
// refusal is the clearest probe signal the instance produces: a node asks for
// keys it owns and prefixes it uses, so a rejected key or a decrypt that fails
// is either a misconfigured node or somebody holding the internal token and
// looking around. Either way an operator should be able to see it.
//
// Deliberately not logged: the ciphertext, the plaintext key, and the signed
// URL. What is recorded is who asked, for what shape of thing, and whether it
// was allowed, which is what makes a pattern visible without the log itself
// becoming the leak.
func logBrokerAccess(c *gin.Context, operation, subject string, allowed bool, reason string) {
	ev := log.Info()
	if !allowed {
		ev = log.Warn()
	}
	ev.
		Str("event", "broker_access").
		Str("operation", operation).
		Str("subject", subject).
		Bool("allowed", allowed).
		Str("client_ip", c.ClientIP()).
		Str("request_id", c.GetHeader("X-Request-Id")).
		Str("reason", reason).
		Msg("internal broker access")
}
