package poollink

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/app/analytics"
	"github.com/warmbly/warmbly/internal/config"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type listMailboxRepo struct {
	repository.PoolLinkRepository
	inst *models.PoolLinkInstance
	rows []models.PoolLinkMailbox
	err  error
}

func (r *listMailboxRepo) ListMailboxes(_ context.Context, instance uuid.UUID) ([]models.PoolLinkMailbox, error) {
	if instance != r.inst.ID {
		panic("wrong instance")
	}
	return r.rows, r.err
}

func (r *listMailboxRepo) ListStanding(_ context.Context, instance uuid.UUID) ([]models.PoolLinkMailboxStanding, error) {
	if instance != r.inst.ID {
		panic("wrong standing instance")
	}
	return []models.PoolLinkMailboxStanding{{RemoteID: r.rows[0].RemoteID, Health: &models.WarmupHealthInfo{State: "quarantined"}}}, nil
}

type listMailboxEmails struct {
	repository.EmailRepository
	org      uuid.UUID
	accounts map[uuid.UUID]models.Email
	calls    int
	err      *errx.Error
}

func (r *listMailboxEmails) Search(_ context.Context, org, search string, cursor, tag *string, limit int32, ids []uuid.UUID) (*models.EmailsResult, *errx.Error) {
	if org != r.org.String() || search != "" || cursor != nil || tag != nil || len(ids) > int(limit) || len(ids) == 0 {
		panic("list must be org-scoped and bounded to its nonempty association batch")
	}
	r.calls++
	if r.err != nil {
		return nil, r.err
	}
	out := &models.EmailsResult{}
	for i := len(ids) - 1; i >= 0; i-- {
		if a, ok := r.accounts[ids[i]]; ok {
			out.Data = append(out.Data, a)
		}
	}
	return out, nil
}

type listMailboxAnalytics struct {
	analytics.AnalyticsService
	org      uuid.UUID
	statuses map[uuid.UUID]models.EmailAccountStatus
	calls    int
	err      *errx.Error
}

func (a *listMailboxAnalytics) GetAccountStatusDetailsPage(_ context.Context, org uuid.UUID, ids []uuid.UUID, cursor string, limit int32) (*models.EmailAccountStatusesResult, *errx.Error) {
	if org != a.org || len(ids) > int(limit) || len(ids) == 0 || cursor != "" {
		panic("status page must be scoped to this instance's association batch")
	}
	a.calls++
	if a.err != nil {
		return nil, a.err
	}
	out := &models.EmailAccountStatusesResult{}
	for _, id := range ids {
		if status, ok := a.statuses[id]; ok {
			out.Data = append(out.Data, status)
		}
	}
	return out, nil
}

type listMailboxWarmup struct {
	repository.WarmupRepository
	calls int
	err   error
}

func (r *listMailboxWarmup) GetWarmupSummaries(_ context.Context, ids []uuid.UUID, from, to time.Time) (map[uuid.UUID]repository.WarmupSummary, error) {
	if len(ids) == 0 || len(ids) > config.AccountStatusLimitMax || to.Sub(from) != 7*24*time.Hour {
		panic("unexpected summary batch/window")
	}
	r.calls++
	out := make(map[uuid.UUID]repository.WarmupSummary)
	for _, id := range ids {
		out[id] = repository.WarmupSummary{Sent: 42, Replied: 6, SpamPlaced: 2}
	}
	return out, r.err
}

func newMailboxList(n int) (*service, *listMailboxRepo, *listMailboxEmails, *listMailboxAnalytics, *listMailboxWarmup) {
	inst := &models.PoolLinkInstance{ID: uuid.New(), OrganizationID: uuid.New()}
	r := &listMailboxRepo{inst: inst}
	e := &listMailboxEmails{org: inst.OrganizationID, accounts: make(map[uuid.UUID]models.Email)}
	a := &listMailboxAnalytics{org: inst.OrganizationID, statuses: make(map[uuid.UUID]models.EmailAccountStatus)}
	w := &listMailboxWarmup{}
	for i := range n {
		id := uuid.New()
		provider := "gmail"
		if i%3 == 0 {
			provider = "smtp_imap"
		}
		enrolled := time.Now().Add(-time.Duration(i) * time.Hour)
		r.rows = append(r.rows, models.PoolLinkMailbox{InstanceID: inst.ID, RemoteID: uuid.New(), EmailAccountID: id, EnrolledAt: enrolled, Managed: provider == "gmail"})
		mode := models.TestParticipationDiagnostic
		e.accounts[id] = models.Email{ID: id, OrganizationID: &inst.OrganizationID, Email: fmt.Sprintf("box-%d@example.test", i), Name: "Mailbox", Provider: provider, Status: "active", TestMode: &mode, TestSendEnabled: true, TestReceiveEnabled: true, WarmupBase: 3, WarmupMax: 30, WarmupIncrease: 2, Timezone: "Europe/Budapest", AuthState: "pass"}
		a.statuses[id] = models.EmailAccountStatus{ID: id, DailyUsage: models.AccountDailyUsage{WarmupSent: i}, WarmupHealth: &models.WarmupHealthInfo{State: "quarantined", Reason: "placement"}, WarmupStatus: &models.WarmupStatusInfo{TargetVolume: 5, PartnerLimit: &models.WarmupPartnerLimit{Reachable: 5, RampTarget: 20}}, Errors: []models.AccountError{{Title: "sync"}}}
	}
	return &service{repo: r, emails: e, analytics: a, warmup: w}, r, e, a, w
}

func TestListMailboxesBatchesAllProvidersAndPreservesState(t *testing.T) {
	for _, n := range []int{0, 15, config.AccountStatusLimitMax + 1} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			s, r, e, a, w := newMailboxList(n)
			before := make(map[uuid.UUID]models.Email)
			for id, acc := range e.accounts {
				before[id] = acc
			}
			states, xerr := s.ListMailboxes(t.Context(), r.inst)
			if xerr != nil || len(states) != n {
				t.Fatalf("len=%d error=%v", len(states), xerr)
			}
			batches := (n + config.AccountStatusLimitMax - 1) / config.AccountStatusLimitMax
			if e.calls != batches || a.calls != batches || w.calls != batches {
				t.Fatalf("calls=%d/%d/%d want %d batch reads", e.calls, a.calls, w.calls, batches)
			}
			for i, st := range states {
				m := r.rows[i]
				acc := e.accounts[m.EmailAccountID]
				if st.RemoteID != m.RemoteID || st.EmailAccountID != acc.ID || st.Email != acc.Email || st.Provider != acc.Provider || st.Managed != m.Managed || !st.EnrolledAt.Equal(m.EnrolledAt) || st.AuthState != acc.AuthState || st.Settings.Max != acc.WarmupMax || st.Settings.Timezone != acc.Timezone {
					t.Fatalf("mailbox metadata/order changed: %+v", st)
				}
				if st.Participation.Mode != *acc.TestMode || !st.Participation.Send || !st.Participation.Receive || st.SentToday != i || st.Sent7d != 42 || st.Replied7d != 6 || st.SpamPlaced7d != 2 || st.Health.State != "quarantined" || st.Warmup.PartnerLimit.Reachable != 5 || len(st.Errors) != 1 {
					t.Fatalf("mailbox status changed: %+v", st)
				}
			}
			if !reflect.DeepEqual(before, e.accounts) {
				t.Fatal("listing changed mailbox data")
			}
		})
	}
}

func TestListMailboxesFailsInsteadOfReturningIncompleteAssociations(t *testing.T) {
	for _, fail := range []string{"associations", "accounts", "missing account", "analytics", "missing status", "summary"} {
		t.Run(fail, func(t *testing.T) {
			s, r, e, a, w := newMailboxList(15)
			switch fail {
			case "associations":
				r.err = errors.New("unavailable")
			case "accounts":
				e.err = errx.InternalError()
			case "missing account":
				delete(e.accounts, r.rows[0].EmailAccountID)
			case "analytics":
				a.err = errx.InternalError()
			case "missing status":
				delete(a.statuses, r.rows[0].EmailAccountID)
			case "summary":
				w.err = errors.New("unavailable")
			}
			if states, xerr := s.ListMailboxes(t.Context(), r.inst); xerr == nil || states != nil {
				t.Fatalf("incomplete success: %+v %v", states, xerr)
			}
		})
	}
}

func TestListStandingDoesNotDependOnMailboxDetails(t *testing.T) {
	s, r, e, a, w := newMailboxList(15)
	e.err, a.err, w.err = errx.InternalError(), errx.InternalError(), errors.New("unavailable")
	states, xerr := s.ListStanding(t.Context(), r.inst)
	if xerr != nil || len(states) != 1 || states[0].Health.State != "quarantined" || e.calls != 0 || a.calls != 0 || w.calls != 0 {
		t.Fatalf("standing depended on full telemetry: %+v %v", states, xerr)
	}
}
