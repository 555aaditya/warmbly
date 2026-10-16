package advanced

import (
	"context"
	"reflect"
	"testing"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/inboxtag"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/repository"
)

// Issue #974: a reply inbox tagging read as a meeting request reached
// automations as the keyword classifier's "neutral", so a filter on interested
// replies missed it. The stored verdict now rides on the event.
func TestProcessIncomingReplyCarriesTheTaggingVerdict(t *testing.T) {
	orgID, accountID, contactID := uuid.New(), uuid.New(), uuid.New()
	account := &models.Email{ID: accountID, OrganizationID: &orgID, Email: "sender@example.test"}
	service, _ := newIncomingReplyService(account, &models.Contact{
		ID: contactID, Email: "recipient@example.test",
	}, contactID)
	service.inboxTags = verdictStore{res: &repository.InboxTagResult{
		Kind: inboxtag.KindHumanReply, KindConfidence: 0.97,
		Intent: inboxtag.IntentScheduling, IntentConfidence: 0.91,
		Labels: []string{"Meeting"}, Relevance: 82,
	}}
	events := &capturedEvents{}
	service.dispatcher = events

	xerr := service.ProcessIncomingReply(context.Background(), accountID, &models.EmailMessageStoreData{
		EmailID:   accountID,
		MessageID: "<reply@example.test>",
		Folder:    models.FolderInbox,
		FromAddr:  []string{"Recipient <recipient@example.test>"},
		ToAddr:    []string{"sender@example.test"},
		InReplyTo: []string{"<opener@example.test>"},
		Subject:   "Re: Hello",
		Snippet:   "Would Tuesday at 2pm work for a call?",
	})
	if xerr != nil {
		t.Fatal(xerr)
	}
	reply := events.data[models.WebhookEventCampaignReplyReceived]
	if reply == nil {
		t.Fatal("no campaign.reply_received event")
	}
	want := map[string]any{
		"ai_kind":              inboxtag.KindHumanReply,
		"ai_intent":            inboxtag.IntentScheduling,
		"ai_intent_confidence": 0.91,
		"ai_labels":            []string{"Meeting"},
		"ai_relevance":         82,
		"ai_needs_review":      false,
	}
	for k, v := range want {
		if !reflect.DeepEqual(reply[k], v) {
			t.Errorf("%s = %#v, want %#v", k, reply[k], v)
		}
	}
}

func TestAddTagVerdictHoldsTheConfidenceFloors(t *testing.T) {
	for _, tc := range []struct {
		name       string
		res        *repository.InboxTagResult
		wantKeys   bool
		wantKind   string
		wantIntent string
	}{
		{name: "never tagged"},
		{
			name:     "uncertain kind",
			res:      &repository.InboxTagResult{Kind: inboxtag.KindHumanReply, KindConfidence: 0.3, NeedsReview: true, ReviewReason: "kind", Labels: []string{inboxtag.LabelNeedsReview}},
			wantKeys: true,
		},
		{
			name:     "unreadable intent",
			res:      &repository.InboxTagResult{Kind: inboxtag.KindHumanReply, KindConfidence: 0.95, Intent: inboxtag.IntentWantsInfo, IntentConfidence: 0.5, NeedsReview: true, ReviewReason: "intent"},
			wantKeys: true, wantKind: inboxtag.KindHumanReply,
		},
		{
			name:     "intent on an auto-reply",
			res:      &repository.InboxTagResult{Kind: inboxtag.KindAutoReplyOOO, KindConfidence: 1, Intent: inboxtag.IntentAgreed, IntentConfidence: 0.9},
			wantKeys: true, wantKind: inboxtag.KindAutoReplyOOO,
		},
		{
			name:     "confident human reply",
			res:      &repository.InboxTagResult{Kind: inboxtag.KindHumanReply, KindConfidence: 0.95, Intent: inboxtag.IntentAgreed, IntentConfidence: 0.9, Labels: []string{"Interested"}},
			wantKeys: true, wantKind: inboxtag.KindHumanReply, wantIntent: inboxtag.IntentAgreed,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload := map[string]any{}
			addTagVerdict(payload, tc.res)
			if !tc.wantKeys {
				if len(payload) != 0 {
					t.Fatalf("untagged message got keys %v", payload)
				}
				return
			}
			if payload["ai_kind"] != tc.wantKind || payload["ai_intent"] != tc.wantIntent {
				t.Fatalf("kind %v intent %v, want %q %q", payload["ai_kind"], payload["ai_intent"], tc.wantKind, tc.wantIntent)
			}
			if tc.wantIntent == "" && payload["ai_intent_confidence"] != 0.0 {
				t.Fatalf("untrusted intent carried confidence %v", payload["ai_intent_confidence"])
			}
			if _, ok := payload["ai_labels"].([]string); !ok {
				t.Fatalf("ai_labels = %#v, want a list", payload["ai_labels"])
			}
		})
	}
}
