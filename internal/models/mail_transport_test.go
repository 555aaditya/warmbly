package models

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/iskorotkov/avro/v2"
)

func TestMailTransportLegacyJSONAndAvroStayUnknown(t *testing.T) {
	var legacy EmailErrorEvent
	if err := json.Unmarshal([]byte(`{"email_account_id":"mailbox","task_id":"task"}`), &legacy); err != nil {
		t.Fatal(err)
	}
	if legacy.Operation.Conservative() != MailTransportUnknown {
		t.Fatal("legacy operation was inferred from task presence")
	}
	reader := JobEvent{}.Schema()
	doc, err := SchemaDocument(reader)
	if err != nil {
		t.Fatal(err)
	}
	var tree any
	if err := json.Unmarshal(doc, &tree); err != nil {
		t.Fatal(err)
	}
	var remove func(any)
	remove = func(node any) {
		switch v := node.(type) {
		case []any:
			for _, item := range v {
				remove(item)
			}
		case map[string]any:
			if fields, ok := v["fields"].([]any); ok {
				var keep []any
				for _, field := range fields {
					f := field.(map[string]any)
					if f["name"] != "operation" {
						keep = append(keep, field)
						remove(f["type"])
					}
				}
				v["fields"] = keep
			} else {
				remove(v["type"])
			}
		}
	}
	remove(tree)
	legacyDoc, err := json.Marshal(tree)
	if err != nil {
		t.Fatal(err)
	}
	writer, err := avro.Parse(string(legacyDoc))
	if err != nil {
		t.Fatal(err)
	}
	resolved, err := avro.NewSchemaCompatibility().Resolve(reader, writer)
	if err != nil {
		t.Fatal(err)
	}
	var got JobEvent
	if err := EventAvro.Unmarshal(resolved, encode(t, writer, JobEvent{Type: JobEventTypeEmailServerError, Body: legacy}), &got); err != nil {
		t.Fatal(err)
	}
	assertBody(t, got.Body, legacy)
	if _, err := avro.NewSchemaCompatibility().Resolve(writer, reader); err != nil {
		t.Fatalf("old reader cannot ignore operation: %v", err)
	}
	typed := EmailErrorEvent{Operation: MailTransportSend, TaskID: "task", EmailAccountID: "mailbox"}
	if err := EventAvro.Unmarshal(reader, encode(t, reader, JobEvent{Type: JobEventTypeEmailServerError, Body: typed}), &got); err != nil {
		t.Fatal(err)
	}
	assertBody(t, got.Body, typed)
}

func TestMailTransportRecoveryNeedsValidOperationClock(t *testing.T) {
	now := time.Now()
	for _, at := range []time.Time{{}, time.Unix(-1, 0), now.Add(time.Hour)} {
		if ValidMailTransportTime(at, now) {
			t.Fatalf("invalid clock accepted: %v", at)
		}
	}
	if !ValidMailTransportTime(now.Add(-time.Hour), now) {
		t.Fatal("real past observation rejected")
	}
	if MailTransportOperation("new_operation").Conservative() != MailTransportUnknown {
		t.Fatal("future operation guessed")
	}
}
