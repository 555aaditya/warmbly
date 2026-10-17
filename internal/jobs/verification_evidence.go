package jobs

import (
	"context"
	"time"

	emailverifyapp "github.com/warmbly/warmbly/internal/app/emailverify"
	"github.com/warmbly/warmbly/internal/jobrun"
)

const (
	// deliveryEvidenceLookback bounds a regular run to steps that came due recently.
	deliveryEvidenceLookback = 7 * 24 * time.Hour
	// deliveryEvidenceFullEvery is how often a run walks every sent step instead.
	deliveryEvidenceFullEvery = 24 * time.Hour
)

// DeliveryEvidenceJob turns campaign sends that never bounced into
// verification evidence: a delivery the recipient's server kept is the
// strongest proof the mailbox exists, and it costs nothing to observe.
type DeliveryEvidenceJob struct {
	evidence *emailverifyapp.Evidence
	interval time.Duration
	batch    int
	lastFull time.Time
}

func NewDeliveryEvidenceJob(evidence *emailverifyapp.Evidence, interval time.Duration, batch int) *DeliveryEvidenceJob {
	if batch <= 0 {
		batch = 2000
	}
	return &DeliveryEvidenceJob{evidence: evidence, interval: interval, batch: batch}
}

// Start runs the job on its interval until ctx ends. A full batch repeats
// at once so a backlog drains.
func (j *DeliveryEvidenceJob) Start(ctx context.Context) {
	jobrun.Loop(ctx, "delivery_evidence", j.interval, false, j.run)
}

// run credits recent steps, and every step on the first run and once a day after.
func (j *DeliveryEvidenceJob) run(ctx context.Context) error {
	full := j.lastFull.IsZero() || time.Since(j.lastFull) >= deliveryEvidenceFullEvery
	lookback := deliveryEvidenceLookback
	if full {
		lookback = 0
	}
	started := time.Now()
	for {
		n, err := j.evidence.CreditCleanDeliveries(ctx, j.batch, lookback)
		if err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}
		if n < j.batch {
			break
		}
	}
	if full {
		j.lastFull = started
	}
	return nil
}
