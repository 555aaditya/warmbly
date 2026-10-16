package integration

import (
	"testing"

	"github.com/warmbly/warmbly/internal/models"
)

// A label list is tested item by item: "Interested" must not match a reply
// labelled "Not interested", and the same holds once the list has been
// through JSON.
func TestFieldConditionContainsReadsAListByItem(t *testing.T) {
	cond := func(op, value string) models.AutomationCondition {
		return models.AutomationCondition{Field: models.AutoCondField, Key: "ai_labels", Operator: op, Value: value}
	}
	for _, tc := range []struct {
		name   string
		labels any
		op     string
		value  string
		want   bool
	}{
		{name: "label present", labels: []string{"Meeting", "Interested"}, op: models.AutoOpContains, value: "interested", want: true},
		{name: "only a longer label holds the word", labels: []string{"Not interested"}, op: models.AutoOpContains, value: "Interested"},
		{name: "decoded from json", labels: []any{"Pricing"}, op: models.AutoOpContains, value: "Pricing", want: true},
		{name: "empty list is not present", labels: []string{}, op: models.AutoOpExists},
		{name: "list with a label is present", labels: []string{"Question"}, op: models.AutoOpExists, want: true},
		{name: "no verdict", labels: nil, op: models.AutoOpContains, value: "Meeting"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			data := map[string]any{}
			if tc.labels != nil {
				data["ai_labels"] = tc.labels
			}
			if got := evaluateAutomationCondition(cond(tc.op, tc.value), data, "seed"); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
	// A plain string keeps substring matching.
	if !evaluateAutomationCondition(models.AutomationCondition{Field: models.AutoCondField, Key: "subject", Operator: models.AutoOpContains, Value: "pricing"},
		map[string]any{"subject": "Re: pricing question"}, "seed") {
		t.Fatal("text contains stopped matching a substring")
	}
}

func TestExpressionListHelpers(t *testing.T) {
	data := map[string]any{"ai_labels": []any{"Not interested"}}
	if EvalExpression(`has .ai_labels "Interested"`, data) {
		t.Fatal(`has matched "Interested" inside "Not interested"`)
	}
	if !EvalExpression(`has .ai_labels "not interested"`, data) {
		t.Fatal("has missed a label that is there")
	}
	if got := renderTemplate(`{{join .ai_labels ", "}}`, map[string]any{"ai_labels": []string{"Meeting", "Pricing"}}); got != "Meeting, Pricing" {
		t.Fatalf("join = %q", got)
	}
}

func TestReplyVerdictPresenceUsesReviewFlag(t *testing.T) {
	cond := models.AutomationCondition{Field: models.AutoCondField, Key: "ai_needs_review", Operator: models.AutoOpExists}
	for _, tc := range []struct {
		name string
		data map[string]any
		want bool
	}{
		{name: "no verdict", data: map[string]any{}},
		{name: "judged with empty labels and intent", data: map[string]any{
			"ai_needs_review": false, "ai_labels": []string{}, "ai_kind": "", "ai_intent": "",
		}, want: true},
		{name: "judged and needs review", data: map[string]any{"ai_needs_review": true}, want: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := evaluateAutomationCondition(cond, tc.data, "seed"); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}
