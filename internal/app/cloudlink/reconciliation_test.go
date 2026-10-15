package cloudlink

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/errx"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

type enrollmentFaultRepo struct {
	*stubLinkRepo
	beginErr   error
	confirmErr error
}

func (r *enrollmentFaultRepo) BeginEnrollment(_ context.Context, account, remote, instance uuid.UUID) error {
	if r.beginErr != nil {
		return r.beginErr
	}
	if r.mailbox != nil && r.mailbox.EnrollmentState == "pending_remove" {
		return errors.New("removal pending")
	}
	r.mailbox = &models.CloudLinkMailbox{EmailAccountID: account, RemoteID: remote, InstanceID: instance, EnrollmentState: "pending_enroll"}
	return nil
}

func (r *enrollmentFaultRepo) Enroll(_ context.Context, _, _, _ uuid.UUID, _ bool) (*models.CloudLinkMailbox, error) {
	if r.confirmErr != nil {
		return nil, r.confirmErr
	}
	r.mailbox.EnrollmentState = "active"
	return r.mailbox, nil
}

func (r *enrollmentFaultRepo) List(context.Context) ([]models.CloudLinkMailbox, error) {
	if r.mailbox == nil {
		return nil, nil
	}
	return []models.CloudLinkMailbox{*r.mailbox}, nil
}

func (r *enrollmentFaultRepo) Unenroll(ctx context.Context, account uuid.UUID) error {
	if err := r.stubLinkRepo.Unenroll(ctx, account); err != nil {
		return err
	}
	r.mailbox = nil
	return nil
}

func (r *enrollmentFaultRepo) InvalidateStanding(context.Context, uuid.UUID) error { return nil }
func (r *enrollmentFaultRepo) SetSyncResult(context.Context, uuid.UUID, time.Time, string) error {
	return nil
}

func (r *enrollmentFaultRepo) ListLinks(context.Context) ([]models.CloudLink, error) {
	return []models.CloudLink{*r.link}, nil
}

func (r *enrollmentFaultRepo) ListForOrg(ctx context.Context, _ uuid.UUID, _ *uuid.UUID) ([]models.CloudLinkMailbox, error) {
	return r.List(ctx)
}

type enrollmentEmails struct{ stubEmails }

type independentRetryRepo struct {
	*listedStandingRepo
	order []uuid.UUID
}

func (r *independentRetryRepo) List(context.Context) ([]models.CloudLinkMailbox, error) {
	rows := make([]models.CloudLinkMailbox, 0, len(r.order))
	for _, id := range r.order {
		rows = append(rows, *r.mailboxes[id])
	}
	return rows, nil
}

func (r *independentRetryRepo) BeginEnrollment(_ context.Context, account, remote, instance uuid.UUID) error {
	m := r.mailboxes[account]
	if m == nil || m.RemoteID != remote || m.InstanceID != instance || m.EnrollmentState == "pending_remove" {
		return errors.New("enrollment identity changed")
	}
	m.EnrollmentState = "pending_enroll"
	return nil
}

func (r *independentRetryRepo) Enroll(_ context.Context, account, _, _ uuid.UUID, _ bool) (*models.CloudLinkMailbox, error) {
	m := r.mailboxes[account]
	m.EnrollmentState = "active"
	return m, nil
}

func (r *independentRetryRepo) BeginRemoval(_ context.Context, id uuid.UUID) error {
	r.mailboxes[id].EnrollmentState = "pending_remove"
	return nil
}

type independentRetryEmails struct {
	repository.EmailRepository
	accounts map[uuid.UUID]*models.Email
}

func (e independentRetryEmails) GetByID(_ context.Context, id uuid.UUID) (*models.Email, *errx.Error) {
	return e.accounts[id], nil
}

func (e independentRetryEmails) GetAllActiveInScope(context.Context, repository.AccountScope) ([]models.Email, *errx.Error) {
	var out []models.Email
	for _, a := range e.accounts {
		out = append(out, *a)
	}
	return out, nil
}

func (e independentRetryEmails) GetSMTPCredentials(context.Context, uuid.UUID) (*repository.SMTPCredentials, *errx.Error) {
	return &repository.SMTPCredentials{}, nil
}

func TestStandingSyncContinuesAfterIndependentEnrollmentAndRemovalConflicts(t *testing.T) {
	org, instance := uuid.New(), uuid.New()
	conflict, removed, success, active := uuid.New(), uuid.New(), uuid.New(), uuid.New()
	ids := []uuid.UUID{conflict, removed, success, active}
	attempts := map[uuid.UUID]int{}
	var standingReads int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Header.Get("Authorization") != "Bearer owning" {
			t.Error("retry used a different link")
		}
		switch req.Method {
		case http.MethodPost:
			var body models.PoolLinkEnrollRequest
			if err := json.NewDecoder(req.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			attempts[body.RemoteID]++
			if body.RemoteID == conflict {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"code":"pool_link_mailbox_conflict","message":"The address exists in this Cloud workspace."}`))
				return
			}
			if body.RemoteID != success {
				t.Error("enrolled an unrelated or removing mailbox")
			}
			_ = json.NewEncoder(w).Encode(models.PoolLinkMailboxState{RemoteID: success, Health: &models.WarmupHealthInfo{State: "healthy"}})
		case http.MethodDelete:
			if req.URL.Path != "/v1/pool-link/instance/mailboxes/"+removed.String() {
				t.Error("removed a different mailbox")
			}
			attempts[removed]++
			w.WriteHeader(http.StatusConflict)
		case http.MethodGet:
			if req.URL.Path == "/v1/pool-link/instance/standing" {
				standingReads++
				var states []models.PoolLinkMailboxStanding
				for _, id := range ids {
					h := &models.WarmupHealthInfo{State: "healthy"}
					if id == active {
						h = &models.WarmupHealthInfo{State: "quarantined", Reason: "complaints"}
					}
					states = append(states, models.PoolLinkMailboxStanding{RemoteID: id, Health: h})
				}
				_ = json.NewEncoder(w).Encode(states)
				return
			}
			_ = json.NewEncoder(w).Encode([]models.PoolLinkMailboxState{{RemoteID: success, Health: &models.WarmupHealthInfo{State: "healthy"}}})
		}
	}))
	defer srv.Close()
	l := &models.CloudLink{InstanceID: instance, OrganizationID: &org, CloudURL: srv.URL, Token: "owning"}
	r := &independentRetryRepo{listedStandingRepo: &listedStandingRepo{workspaceLinkRepo: &workspaceLinkRepo{
		links: map[uuid.UUID]*models.CloudLink{instance: l}, mailboxes: map[uuid.UUID]*models.CloudLinkMailbox{},
	}}, order: ids}
	emails := independentRetryEmails{accounts: map[uuid.UUID]*models.Email{}}
	old := time.Now().Add(-time.Hour)
	for _, id := range ids {
		r.mailboxes[id] = &models.CloudLinkMailbox{EmailAccountID: id, RemoteID: id, InstanceID: instance, EnrollmentState: "pending_enroll", Standing: &models.WarmupHealthInfo{State: "healthy"}, StandingObservedAt: &old}
		emails.accounts[id] = &models.Email{ID: id, OrganizationID: &org, Status: "active", Provider: string(models.InboxProviderSMTPIMAP)}
	}
	r.mailboxes[removed].EnrollmentState = "pending_remove"
	r.mailboxes[active].EnrollmentState = "active"
	s := &service{repo: r, emails: emails}
	changes, xerr := s.SyncStanding(context.Background())
	if xerr == nil || standingReads != 1 || attempts[conflict] != 1 || attempts[removed] != 1 || attempts[success] != 1 {
		t.Fatalf("one refusal stopped independent retries or status reads: attempts=%v reads=%d error=%v", attempts, standingReads, xerr)
	}
	if len(changes) != 1 || changes[0].EmailAccountID != active || changes[0].Current != models.WarmupHealthQuarantined {
		t.Fatalf("failed retry lost an independently persisted transition: %+v", changes)
	}
	if r.mailboxes[success].EnrollmentState != "active" || r.mailboxes[active].StandingObservedAt == nil || !r.mailboxes[active].StandingObservedAt.After(old) {
		t.Fatal("healthy enrollment or existing standing did not recover")
	}
	for _, id := range []uuid.UUID{conflict, removed} {
		m := r.mailboxes[id]
		if m.EnrollmentState == "active" || m.EffectiveStanding(time.Now()).State != "blocked" {
			t.Fatal("fresh standing admitted a mailbox with pending work")
		}
	}
}

func (e enrollmentEmails) GetSMTPCredentials(context.Context, uuid.UUID) (*repository.SMTPCredentials, *errx.Error) {
	return &repository.SMTPCredentials{}, nil
}

func (e enrollmentEmails) GetAllActiveInScope(context.Context, repository.AccountScope) ([]models.Email, *errx.Error) {
	return []models.Email{*e.account}, nil
}

type existingEnrollmentRepo struct {
	*enrollmentFaultRepo
	workspace *models.CloudLink
}

func (r *existingEnrollmentRepo) Get(_ context.Context, org *uuid.UUID) (*models.CloudLink, error) {
	if org != nil && r.workspace != nil {
		return r.workspace, nil
	}
	return r.link, nil
}

func TestExistingServerConnectionEnrollsAndRefreshesWithoutMovingMailboxes(t *testing.T) {
	t.Setenv("APP_ENV", "dev")
	org, account, instance := uuid.New(), uuid.New(), uuid.New()
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer original" {
			t.Error("mailbox used a different connection")
		}
		if r.Method == http.MethodPost {
			posts++
		}
		_, _ = w.Write([]byte(`{}`))
	}))
	defer srv.Close()
	r := &existingEnrollmentRepo{enrollmentFaultRepo: &enrollmentFaultRepo{stubLinkRepo: &stubLinkRepo{
		link: &models.CloudLink{InstanceID: instance, CloudURL: srv.URL, Token: "original"},
	}}}
	s := NewService(r, enrollmentEmails{stubEmails{account: &models.Email{
		ID: account, OrganizationID: &org, Status: "active", Provider: "smtp_imap",
	}}}, nil).(*service)
	if _, xerr := s.Enroll(context.Background(), org, account); xerr != nil {
		t.Fatal(xerr)
	}
	r.workspace = &models.CloudLink{InstanceID: uuid.New(), OrganizationID: &org, CloudURL: "http://127.0.0.1:1"}
	if xerr := s.RefreshCredentials(context.Background(), org, account); xerr != nil {
		t.Fatal(xerr)
	}
	if posts != 2 || r.mailbox == nil || r.mailbox.InstanceID != instance {
		t.Fatalf("enrollment moved or was not refreshed: %+v, posts=%d", r.mailbox, posts)
	}
}

func TestEnrollmentRecordsIntentBeforeRemoteAndRetriesAmbiguousConfirmation(t *testing.T) {
	for _, failure := range []string{"lost_ack", "local_confirmation", "intent_write"} {
		t.Run(failure, func(t *testing.T) {
			org, account := uuid.New(), uuid.New()
			posts, deletes := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				switch r.Method {
				case http.MethodPost:
					posts++
					if failure == "lost_ack" && posts == 1 {
						conn, _, err := w.(http.Hijacker).Hijack()
						if err == nil {
							_ = conn.Close()
						}
						return
					}
					_, _ = w.Write([]byte(`{}`))
				case http.MethodDelete:
					deletes++
				case http.MethodGet:
					_, _ = w.Write([]byte(`[]`))
				}
			}))
			t.Cleanup(srv.Close)
			r := &enrollmentFaultRepo{stubLinkRepo: &stubLinkRepo{link: &models.CloudLink{CloudURL: srv.URL, OrganizationID: &org}}}
			if failure == "local_confirmation" {
				r.confirmErr = errors.New("write unavailable")
			}
			if failure == "intent_write" {
				r.beginErr = errors.New("write unavailable")
			}
			s := NewService(r, enrollmentEmails{stubEmails{account: &models.Email{ID: account, OrganizationID: &org, Status: "active", Provider: "smtp_imap"}}}, nil).(*service)
			if _, xerr := s.Enroll(context.Background(), org, account); xerr == nil {
				t.Fatal("ambiguous or unsaved enrollment reported success")
			}
			if failure == "intent_write" {
				if posts != 0 {
					t.Fatal("remote enrollment preceded durable intent")
				}
				return
			}
			if r.mailbox == nil || r.mailbox.EnrollmentState != "pending_enroll" || deletes != 0 {
				t.Fatalf("durable confirmation intent replaced by compensation: %+v, deletes=%d", r.mailbox, deletes)
			}
			r.confirmErr = nil
			// A new service represents a restart, without the previous service's memory.
			s = NewService(r, s.emails, nil).(*service)
			if _, xerr := s.SyncStanding(context.Background()); xerr != nil {
				t.Fatal(xerr)
			}
			if posts != 2 || r.mailbox.EnrollmentState != "active" || deletes != 0 {
				t.Fatalf("enrollment retry not idempotent: %+v, posts=%d, deletes=%d", r.mailbox, posts, deletes)
			}
		})
	}
}

func TestRemovalRetainsOptOutAfterLostAcknowledgmentAndCleanupFailure(t *testing.T) {
	f := newRevokeFixture(t, http.StatusNoContent)
	r := &enrollmentFaultRepo{stubLinkRepo: f.repo}
	s := NewService(r, f.svc.emails, nil).(*service)
	deletes := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		if req.Method == http.MethodDelete {
			deletes++
			if deletes == 1 {
				conn, _, err := w.(http.Hijacker).Hijack()
				if err == nil {
					_ = conn.Close()
				}
				return
			}
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"code":"pool_link_mailbox_not_found"}`))
		}
	}))
	t.Cleanup(srv.Close)
	r.link.CloudURL = srv.URL
	if xerr := s.Unenroll(context.Background(), f.org, f.account); xerr == nil {
		t.Fatal("lost acknowledgment reported completed removal")
	}
	if r.mailbox.EnrollmentState != "pending_remove" {
		t.Fatal("lost acknowledgment discarded opt-out")
	}
	r.unenrollErr = errors.New("local cleanup unavailable")
	if xerr := s.reconcileEnrollments(context.Background(), r.link, []models.CloudLinkMailbox{*r.mailbox}); xerr == nil {
		t.Fatal("failed local cleanup reported completed removal")
	}
	if r.mailbox.EnrollmentState != "pending_remove" {
		t.Fatal("failed cleanup discarded opt-out")
	}
	r.unenrollErr = nil
	if xerr := s.reconcileEnrollments(context.Background(), r.link, []models.CloudLinkMailbox{*r.mailbox}); xerr != nil {
		t.Fatal(xerr)
	}
	if r.mailbox != nil || deletes != 3 {
		t.Fatalf("removal was not retried: %+v, deletes=%d", r.mailbox, deletes)
	}
}
