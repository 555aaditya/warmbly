package repository

import (
	"context"
	"errors"
	"testing"

	"github.com/warmbly/warmbly/internal/observability/arrivaldiag"
)

func TestArrivalDiagnosticsBatchPreservesLimitFirstErrorAndStop(t *testing.T) {
	for _, observed := range []bool{false, true} {
		ctx := t.Context()
		if observed {
			ctx, _ = arrivaldiag.WithPass(ctx)
		}
		first, second := errors.New("first sensitive failure"), errors.New("second sensitive failure")
		calls := 0
		got := deliverArrivalBatch(ctx, func() (bool, error) {
			calls++
			if calls == 2 {
				return true, first
			}
			if calls == 3 {
				return true, second
			}
			return true, nil
		})
		if calls != 32 || got != first {
			t.Fatalf("observed=%v changed cap/error identity: calls=%d error=%v", observed, calls, got)
		}
		calls = 0
		got = deliverArrivalBatch(ctx, func() (bool, error) { calls++; return false, second })
		if calls != 1 || got != second {
			t.Fatal("empty/failing claim did not stop identically")
		}
		cancelCtx, cancel := context.WithCancel(ctx)
		calls = 0
		got = deliverArrivalBatch(cancelCtx, func() (bool, error) { calls++; cancel(); return true, first })
		if calls != 1 || got != first {
			t.Fatal("cancellation changed first error or retried extra rows")
		}
	}
}
