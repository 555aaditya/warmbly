package wmail

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// What Graph's /$value returns for an Outlook.com feedback-loop report.
var graphFeedbackReportMIME = strings.ReplaceAll(`From: staff@hotmail.com
Subject: complaint about message from 203.0.113.9
Message-ID: <report-envelope@hotmail.com>
MIME-Version: 1.0
Content-Type: multipart/report; report-type=feedback-report; boundary="b1"

--b1
Content-Type: text/plain

This is an email abuse report for an email message received from IP 203.0.113.9.

--b1
Content-Type: message/feedback-report

Feedback-Type: abuse
User-Agent: Hotmail FBL
Version: 1
Original-Rcpt-To: <recipient@hotmail.com>

--b1
Content-Type: message/rfc822

From: box@outlook.test
To: recipient@hotmail.com
Subject: Quick question
Message-ID: <the-original-send@outlook.test>

Hi there
--b1--
`, "\n", "\r\n")

// What Graph's delta sync hands the worker for the same message: the rendered notice only.
func graphRenderedReport() *models.EmailMessageData {
	return &models.EmailMessageData{
		GmailID:  "report-1",
		From:     []string{"staff@hotmail.com"},
		Subject:  "complaint about message from 203.0.113.9",
		Flags:    []string{`Content-Type:multipart/report; report-type=feedback-report; boundary="b1"`},
		BodyHTML: "<p>This is an email abuse report for an email message received from IP 203.0.113.9.</p>",
	}
}

func rawMIMEServer(t *testing.T, status int, fetched *[]string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		*fetched = append(*fetched, r.URL.Path)
		if r.URL.Path != "/v1.0/me/messages/report-1/$value" || status != http.StatusOK {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(graphFeedbackReportMIME))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestGraphComplaintIsReadFromTheRawMessage(t *testing.T) {
	var fetched []string
	var events []captured
	var states []models.SyncState
	w := newGraphTestMail(t, rawMIMEServer(t, http.StatusOK, &fetched), &events, &states)

	got := w.complaintReport(t.Context(), graphRenderedReport())
	if got == nil {
		t.Fatalf("no complaint from a Graph feedback report (fetched %v)", fetched)
	}
	if got.OriginalMessageID != "the-original-send@outlook.test" || got.ComplainedRecipient != "recipient@hotmail.com" || got.Provider != "Hotmail FBL" {
		t.Errorf("complaint = %+v", got)
	}
	if len(fetched) != 1 {
		t.Errorf("raw fetches = %v, want one", fetched)
	}
}

func TestGraphComplaintFetchesOnlyWhenTheSyncedBodyCannotTell(t *testing.T) {
	t.Run("a body that already carries the report parts", func(t *testing.T) {
		var fetched []string
		var events []captured
		var states []models.SyncState
		w := newGraphTestMail(t, rawMIMEServer(t, http.StatusOK, &fetched), &events, &states)
		msg := graphRenderedReport()
		msg.BodyPlain = "Feedback-Type: abuse\r\nMessage-ID: <the-original-send@outlook.test>\r\n"
		if got := w.complaintReport(t.Context(), msg); got == nil || got.OriginalMessageID != "the-original-send@outlook.test" {
			t.Errorf("complaint = %+v", got)
		}
		if len(fetched) != 0 {
			t.Errorf("fetched %v for a body that already parsed", fetched)
		}
	})
	t.Run("a mailbox that is not Graph", func(t *testing.T) {
		w := &WMail{EmailType: models.InboxProviderGoogle}
		if got := w.complaintReport(t.Context(), graphRenderedReport()); got != nil {
			t.Errorf("complaint = %+v from a rendered body alone", got)
		}
	})
	t.Run("ordinary mail", func(t *testing.T) {
		var fetched []string
		var events []captured
		var states []models.SyncState
		w := newGraphTestMail(t, rawMIMEServer(t, http.StatusOK, &fetched), &events, &states)
		msg := &models.EmailMessageData{GmailID: "report-1", From: []string{"ada@acme.com"}, Subject: "Re: your note", BodyPlain: "thanks"}
		if got := w.complaintReport(t.Context(), msg); got != nil {
			t.Errorf("complaint = %+v", got)
		}
		if len(fetched) != 0 {
			t.Errorf("fetched %v for ordinary mail", fetched)
		}
	})
	t.Run("a raw fetch that fails", func(t *testing.T) {
		var fetched []string
		var events []captured
		var states []models.SyncState
		w := newGraphTestMail(t, rawMIMEServer(t, http.StatusInternalServerError, &fetched), &events, &states)
		if got := w.complaintReport(t.Context(), graphRenderedReport()); got != nil {
			t.Errorf("complaint = %+v after a failed fetch", got)
		}
	})
}
