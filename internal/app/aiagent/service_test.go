package aiagent

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/google/uuid"

	"github.com/warmbly/warmbly/internal/app/aitools"
	"github.com/warmbly/warmbly/internal/models"
	"github.com/warmbly/warmbly/internal/pkg/generation"
)

func testRegistry() *aitools.Registry {
	r := aitools.NewRegistry()
	noop := func(context.Context, aitools.Invocation, json.RawMessage) (string, error) { return `{}`, nil }
	r.Register(aitools.Tool{Name: "add_tag", Risk: generation.RiskWrite, RequiredOrgPerm: models.PermManageContacts, Handler: noop})
	r.Register(aitools.Tool{Name: "set_campaign_status", Risk: generation.RiskWrite, AlwaysAsk: true, RequiredOrgPerm: models.PermSendCampaigns, Handler: noop})
	r.Register(aitools.Tool{Name: "send_reply", Risk: generation.RiskSend, RequiredOrgPerm: models.PermAccessUnibox, Handler: noop})
	r.Register(aitools.Tool{Name: "list_contacts", Risk: generation.RiskRead, RequiredOrgPerm: models.PermViewContacts, Handler: noop})
	return r
}

func TestCanonicalArgsSortedAndCapped(t *testing.T) {
	got, cut := canonicalArgs(json.RawMessage(`{"b":1,"a":{"d":2,"c":3}}`))
	if cut {
		t.Fatal("small args were cut")
	}
	if !(strings.Index(got, `"a"`) < strings.Index(got, `"b"`) && strings.Index(got, `"c"`) < strings.Index(got, `"d"`)) {
		t.Fatalf("keys not sorted: %s", got)
	}
	big := `{"body":"` + strings.Repeat("é", models.AgentApprovalArgsMax) + `"}`
	got, cut = canonicalArgs(json.RawMessage(big))
	if !cut || len(got) > models.AgentApprovalArgsMax {
		t.Fatalf("large args: cut=%v len=%d", cut, len(got))
	}
	if !utf8.ValidString(got) {
		t.Fatalf("cut inside a rune: %q", got[len(got)-8:])
	}
}

func TestWithholdSecrets(t *testing.T) {
	out, secrets := withholdSecrets(`{"ok":true,"secret":"whsec_abc","webhook_id":"w"}`, []string{"secret"})
	if secrets["secret"] != "whsec_abc" {
		t.Fatalf("secret not returned: %v", secrets)
	}
	if strings.Contains(out, "whsec_abc") || !strings.Contains(out, `"webhook_id":"w"`) {
		t.Fatalf("model-facing result = %s", out)
	}
	if out, secrets := withholdSecrets(`{"error":"nope"}`, []string{"secret"}); secrets != nil || out != `{"error":"nope"}` {
		t.Fatalf("a result without the field changed: %s %v", out, secrets)
	}
}

func TestAlwaysAllowNeedsSettingsAndANonSendingTool(t *testing.T) {
	s := &service{registry: testRegistry()}
	admin := aitools.Invocation{OrgID: uuid.New(), OrgPerms: models.PermManageSettings | models.PermManageContacts}
	member := aitools.Invocation{OrgID: uuid.New(), OrgPerms: models.PermManageContacts}
	if !s.alwaysAllowOffered(admin, "add_tag", "write") {
		t.Error("a settings manager is not offered always allow on a write tool")
	}
	if s.alwaysAllowOffered(member, "add_tag", "write") {
		t.Error("a member without manage_settings is offered always allow")
	}
	for _, name := range []string{"set_campaign_status", "send_reply", "mcp_x_y", "unknown"} {
		if s.alwaysAllowOffered(admin, name, "write") {
			t.Errorf("%s is offered always allow", name)
		}
	}
	policies := map[string]string{"add_tag": "always_allow", "set_campaign_status": "always_allow", "mcp_x_y": "always_allow"}
	if !s.autoAllowed(policies, generation.ToolDef{Name: "add_tag", Risk: generation.RiskWrite}) {
		t.Error("a policy for a write tool does not apply")
	}
	for _, name := range []string{"set_campaign_status", "mcp_x_y"} {
		if s.autoAllowed(policies, generation.ToolDef{Name: name, Risk: generation.RiskWrite}) {
			t.Errorf("a policy auto-runs %s", name)
		}
	}
}

func TestForReaderWithholdsResultsOfToolsTheReaderCannotRun(t *testing.T) {
	s := &service{registry: testRegistry()}
	reader := aitools.Invocation{OrgID: uuid.New(), OrgPerms: models.PermViewContacts}
	msgs := []generation.AgentMessage{
		{Role: "assistant", ToolCalls: []generation.ToolCall{{ID: "1", Name: "list_contacts"}, {ID: "2", Name: "send_reply"}}},
		{Role: "tool", ToolCallID: "1", Content: `{"count":1}`},
		{Role: "tool", ToolCallID: "2", Content: `{"to":"someone@example.com"}`},
	}
	out := s.forReader(context.Background(), reader, msgs)
	if out[1].Content != `{"count":1}` {
		t.Errorf("a permitted result was withheld: %s", out[1].Content)
	}
	if out[2].Content != withheldResult {
		t.Errorf("an unpermitted result was kept: %s", out[2].Content)
	}
	if msgs[2].Content == withheldResult {
		t.Error("the stored messages were modified")
	}
}

func TestOrderTiedMessagesAnswersEveryToolCallInPlace(t *testing.T) {
	call := func(id string) generation.AgentMessage {
		return generation.AgentMessage{Role: "assistant", ToolCalls: []generation.ToolCall{{ID: id, Name: "list_contacts"}}}
	}
	result := func(id string) generation.AgentMessage {
		return generation.AgentMessage{Role: "tool", ToolCallID: id, Content: id}
	}
	reply := generation.AgentMessage{Role: "assistant", Content: "done"}
	// A resumed run saved in one transaction: the approved call's result, two tool rounds, the reply.
	got := orderTiedMessages([]generation.AgentMessage{reply, result("b"), call("c"), result("c"), result("a"), call("b")})
	if len(got) != 6 {
		t.Fatalf("got %d messages, want 6", len(got))
	}
	if got[0].ToolCallID != "a" {
		t.Fatalf("result for the earlier call is not first: %v", got)
	}
	if got[5].Content != "done" {
		t.Fatalf("reply is not last: %v", got)
	}
	for i := 1; i < 5; i += 2 {
		if len(got[i].ToolCalls) != 1 || got[i+1].ToolCallID != got[i].ToolCalls[0].ID {
			t.Fatalf("tool call at %d is not followed by its result: %v", i, got)
		}
	}
}
