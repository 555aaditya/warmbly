package handler

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/google/uuid"
	"github.com/warmbly/warmbly/internal/repository"
)

type internalBatchMessageMap struct {
	repository.EmailMessageMapRepository
	calls int
	err   error
}

func (r *internalBatchMessageMap) GetMany(_ context.Context, userID, emailID uuid.UUID, ids []string) (map[string]*repository.EmailMessageData, error) {
	r.calls++
	return map[string]*repository.EmailMessageData{"known": {UserID: userID.String(), EmailID: emailID.String(), MessageID: "known", ID: uuid.NewString(), ThreadID: "thread"}}, r.err
}

func TestInternalMessageMapBatchValidatesBeforeQueryAndDoesNotExposeErrors(t *testing.T) {
	user, email := uuid.NewString(), uuid.NewString()
	for _, tc := range []struct {
		name          string
		payload       any
		status, calls int
		err           error
	}{
		{"valid", map[string]any{"user_id": user, "email_id": email, "message_ids": []string{"known", "missing"}}, 200, 1, nil},
		{"bad-user", map[string]any{"user_id": "invalid", "email_id": email, "message_ids": []string{"known"}}, 400, 0, nil},
		{"bad-mailbox", map[string]any{"user_id": user, "email_id": uuid.Nil.String(), "message_ids": []string{"known"}}, 400, 0, nil},
		{"empty-id", map[string]any{"user_id": user, "email_id": email, "message_ids": []string{""}}, 400, 0, nil},
		{"too-many", map[string]any{"user_id": user, "email_id": email, "message_ids": make([]string, repository.EmailMessageMapBatchSize+1)}, 400, 0, nil},
		{"too-large", map[string]any{"user_id": user, "email_id": email, "message_ids": []string{strings.Repeat("x", 1<<20)}}, 400, 0, nil},
		{"failure", map[string]any{"user_id": user, "email_id": email, "message_ids": []string{"known"}}, 500, 1, errors.New("private database detail")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			repo := &internalBatchMessageMap{err: tc.err}
			h := &Handler{EmailMessageMap: repo}
			r := gin.New()
			r.POST("/lookup", h.InternalLookupEmailMessageMaps)
			body, _ := json.Marshal(tc.payload)
			resp := httptest.NewRecorder()
			r.ServeHTTP(resp, httptest.NewRequest(http.MethodPost, "/lookup", strings.NewReader(string(body))))
			if resp.Code != tc.status || repo.calls != tc.calls || strings.Contains(resp.Body.String(), "private database detail") {
				t.Fatalf("status=%d calls=%d body=%s", resp.Code, repo.calls, resp.Body.String())
			}
			if tc.status == 200 {
				var payload struct {
					Mappings []emailMessageMapPayload `json:"mappings"`
				}
				if err := json.Unmarshal(resp.Body.Bytes(), &payload); err != nil || len(payload.Mappings) != 1 || payload.Mappings[0].MessageID != "known" {
					t.Fatalf("invalid mappings: %+v %v", payload, err)
				}
			}
		})
	}
}
