package repository

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/google/uuid"
)

type batchMessageMapSpy struct {
	EmailMessageMapRepository
	batches [][]string
	gets    int
	err     error
}

func (r *batchMessageMapSpy) GetMany(_ context.Context, _, _ uuid.UUID, ids []string) (map[string]*EmailMessageData, error) {
	r.batches = append(r.batches, append([]string(nil), ids...))
	return map[string]*EmailMessageData{ids[0]: {MessageID: ids[0]}}, r.err
}

func (r *batchMessageMapSpy) Get(_ context.Context, _, _ uuid.UUID, id string) (*EmailMessageData, error) {
	r.gets++
	return &EmailMessageData{MessageID: id}, nil
}

func TestMessageMapBatchBoundsDeduplicatesAndFallsBackOnlyWhenUnsupported(t *testing.T) {
	ids := make([]string, 0, 405)
	for i := 0; i < 401; i++ {
		ids = append(ids, fmt.Sprintf("msg-%d", i))
	}
	ids = append(ids, ids[0], ids[1])
	for _, tc := range []struct {
		name           string
		err            error
		gets, mappings int
	}{
		{"batch", nil, 0, 3}, {"legacy", ErrMessageMapBatchUnsupported, 401, 401}, {"failure", errors.New("unavailable"), 0, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &batchMessageMapSpy{err: tc.err}
			got, err := GetEmailMessageMaps(t.Context(), repo, uuid.New(), uuid.New(), ids)
			if (err != nil) != (tc.name == "failure") || len(got) != tc.mappings || repo.gets != tc.gets {
				t.Fatalf("mappings=%d gets=%d err=%v", len(got), repo.gets, err)
			}
			for _, batch := range repo.batches {
				if len(batch) > EmailMessageMapBatchSize {
					t.Fatal("unbounded batch")
				}
			}
		})
	}
	spy := &batchMessageMapSpy{}
	if _, err := GetEmailMessageMaps(t.Context(), spy, uuid.New(), uuid.New(), []string{""}); err == nil || len(spy.batches) != 0 {
		t.Fatal("empty message id accepted")
	}
	if got, err := GetEmailMessageMaps(t.Context(), spy, uuid.New(), uuid.New(), nil); err != nil || len(got) != 0 || len(spy.batches) != 0 {
		t.Fatal("empty batch issued a request")
	}
}

func TestHTTPMessageMapBatchUpgradeAndFailure(t *testing.T) {
	for _, status := range []int{200, 404, 405, 401, 403, 500, 503} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			userID, emailID, id := uuid.New(), uuid.New(), uuid.New()
			batchCalls, gets := 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.Header.Get("Authorization") != "Bearer fixture-token" {
					t.Error("missing auth")
				}
				if r.Method == http.MethodPost && r.URL.Path == "/api/v1/internal/email-message-map/lookup" {
					batchCalls++
					var p struct {
						UserID     string   `json:"user_id"`
						EmailID    string   `json:"email_id"`
						MessageIDs []string `json:"message_ids"`
					}
					if err := json.NewDecoder(r.Body).Decode(&p); err != nil || p.UserID != userID.String() || p.EmailID != emailID.String() || len(p.MessageIDs) != 2 {
						t.Errorf("invalid batch: %+v %v", p, err)
					}
					w.WriteHeader(status)
					if status == 200 {
						_ = json.NewEncoder(w).Encode(map[string]any{"mappings": []emailMessageMapPayload{{UserID: userID.String(), EmailID: emailID.String(), MessageID: "known", ID: id.String(), ThreadID: "thread"}}})
					}
					return
				}
				gets++
				if r.URL.Query().Get("message_id") == "missing" {
					w.WriteHeader(404)
					return
				}
				_ = json.NewEncoder(w).Encode(emailMessageMapPayload{UserID: userID.String(), EmailID: emailID.String(), MessageID: "known", ID: id.String(), ThreadID: "thread"})
			}))
			defer srv.Close()
			repo, err := NewHTTPEmailMessageMapRepository(srv.URL, "fixture-token")
			if err != nil {
				t.Fatal(err)
			}
			for i := 0; i < 2; i++ {
				got, err := GetEmailMessageMaps(t.Context(), repo, userID, emailID, []string{"known", "missing"})
				success := status == 200 || status == 404 || status == 405
				if (err == nil) != success {
					t.Fatalf("unexpected error: %v", err)
				}
				if success && (len(got) != 1 || got["known"].ID != id.String() || got["known"].ThreadID != "thread") {
					t.Fatalf("unexpected mappings: %+v", got)
				}
			}
			if status == 404 || status == 405 {
				if batchCalls != 1 || gets != 4 {
					t.Fatalf("legacy fallback: batch=%d get=%d", batchCalls, gets)
				}
				repo.(*httpEmailMessageMapRepository).batchUnsupportedUntil.Store(0)
				_, _ = GetEmailMessageMaps(t.Context(), repo, userID, emailID, []string{"known", "missing"})
				if batchCalls != 2 {
					t.Fatal("backend upgrade was not re-probed")
				}
			} else if batchCalls != 2 || gets != 0 {
				t.Fatalf("batch=%d gets=%d", batchCalls, gets)
			}
		})
	}
}

func TestHTTPMessageMapBatchNeverCachesAbsenceOrAcceptsInvalidMapping(t *testing.T) {
	userID, emailID, id := uuid.New(), uuid.New(), uuid.New()
	payload := `{"mappings":[]}`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(payload)) }))
	defer srv.Close()
	repo, _ := NewHTTPEmailMessageMapRepository(srv.URL, "fixture-token")
	lookup := func() (map[string]*EmailMessageData, error) {
		return GetEmailMessageMaps(t.Context(), repo, userID, emailID, []string{"new"})
	}
	got, err := lookup()
	if err != nil || len(got) != 0 {
		t.Fatal("empty result failed")
	}
	valid := emailMessageMapPayload{UserID: userID.String(), EmailID: emailID.String(), ID: id.String(), MessageID: "new"}
	encode := func(items []emailMessageMapPayload) {
		b, _ := json.Marshal(map[string]any{"mappings": items})
		payload = string(b)
	}
	encode([]emailMessageMapPayload{valid})
	if got, err = lookup(); err != nil || got["new"] == nil {
		t.Fatal("negative result cached")
	}
	for _, invalid := range []string{`{}`, `{"mappings":null}`, `truncated`} {
		payload = invalid
		if _, err = lookup(); err == nil {
			t.Fatalf("accepted invalid payload %q", invalid)
		}
	}
	for _, mutate := range []func(*emailMessageMapPayload){
		func(p *emailMessageMapPayload) { p.UserID = uuid.NewString() },
		func(p *emailMessageMapPayload) { p.EmailID = uuid.NewString() },
		func(p *emailMessageMapPayload) { p.MessageID = "foreign" },
		func(p *emailMessageMapPayload) { p.ID = "bad-id" },
	} {
		p := valid
		mutate(&p)
		encode([]emailMessageMapPayload{p})
		if _, err = lookup(); err == nil {
			t.Fatal("accepted invalid mapping")
		}
	}
	encode([]emailMessageMapPayload{valid, valid})
	if _, err = lookup(); err == nil {
		t.Fatal("accepted duplicate mapping")
	}
}
