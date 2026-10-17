package generation

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func catalogTestTools(n int) []ToolDef {
	tools := make([]ToolDef, n)
	for i := range tools {
		tools[i] = ToolDef{
			Name:        fmt.Sprintf("tool_%04d", i),
			Description: "Read a workspace record",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
			Risk:        RiskRead,
			Handler:     func(context.Context, json.RawMessage) (string, error) { return `{}`, nil },
		}
	}
	return tools
}

func findCatalogTools(t *testing.T, c *toolCatalog, query string, offset int) toolSearchResult {
	t.Helper()
	args, err := json.Marshal(map[string]any{"query": query, "offset": offset})
	if err != nil {
		t.Fatal(err)
	}
	out, err := c.search.Handler(context.Background(), args)
	if err != nil {
		t.Fatal(err)
	}
	var result toolSearchResult
	if err := json.Unmarshal([]byte(out), &result); err != nil {
		t.Fatal(err)
	}
	return result
}

func TestToolCatalogThreshold(t *testing.T) {
	for _, count := range []int{0, 1, 127, 128, 129, 138, 1024} {
		t.Run(fmt.Sprint(count), func(t *testing.T) {
			c := newToolCatalog(catalogTestTools(count), nil)
			if count <= 128 {
				if c != nil {
					t.Fatal("a catalog within the limit should be passed through")
				}
				return
			}
			if c == nil || len(c.definitions()) != 1 {
				t.Fatal("oversized catalogs should initially expose only discovery")
			}
		})
	}
}

func TestToolCatalogEveryToolIsReachableWithBoundedPages(t *testing.T) {
	tools := catalogTestTools(1024)
	c := newToolCatalog(tools, nil)
	seen := map[string]bool{}
	for offset := 0; ; {
		result := findCatalogTools(t, c, "", offset)
		if result.TotalMatches != len(tools) {
			t.Fatalf("total = %d, want %d", result.TotalMatches, len(tools))
		}
		defs := c.definitions()
		if len(defs) != len(result.Tools)+1 || len(defs) > 128 {
			t.Fatalf("wire catalog size = %d for %d matches", len(defs), len(result.Tools))
		}
		for i, match := range result.Tools {
			if seen[match.Name] || defs[i].Name != match.Name || defs[i].InputSchema == nil || defs[i].Handler == nil {
				t.Fatalf("duplicate or incomplete definition: %s", match.Name)
			}
			seen[match.Name] = true
		}
		if result.NextOffset == nil {
			break
		}
		if *result.NextOffset <= offset {
			t.Fatal("pagination did not advance")
		}
		offset = *result.NextOffset
	}
	if len(seen) != len(tools) {
		t.Fatalf("reachable tools = %d, want %d", len(seen), len(tools))
	}
}

func TestToolCatalogSearchAndValidation(t *testing.T) {
	tools := catalogTestTools(138)
	tools[137].Name = "mcp_crm_find_customer"
	tools[137].Description = "Look up a billing subscription"
	c := newToolCatalog(tools, nil)
	for _, query := range []string{"MCP_CRM_FIND_CUSTOMER", "subscription"} {
		result := findCatalogTools(t, c, query, 0)
		if len(result.Tools) != 1 || result.Tools[0].Name != tools[137].Name {
			t.Fatalf("search %q = %+v", query, result)
		}
	}
	if result := findCatalogTools(t, c, "workspace customer", 0); result.Tools[0].Name != tools[137].Name {
		t.Fatal("a name match should rank ahead of a description-only match")
	}
	for _, args := range []string{`{`, `{"query":42}`, `{"offset":-1}`} {
		if _, err := c.find(context.Background(), json.RawMessage(args)); err == nil {
			t.Fatalf("invalid arguments accepted: %s", args)
		}
	}
	for _, tc := range []struct {
		query  string
		offset int
	}{{"no_such_tool", 0}, {"", 9999}} {
		result := findCatalogTools(t, c, tc.query, tc.offset)
		if len(result.Tools) != 0 || result.NextOffset != nil || len(c.definitions()) != 1 {
			t.Fatalf("empty search left stale tools: %+v", result)
		}
	}
}

func TestToolCatalogRestoresCurrentPermissionsAndAvoidsNameCollisions(t *testing.T) {
	tools := catalogTestTools(138)
	tools[0].Name = "search_available_tools"
	tools[1].Name = "search_available_tools_2"
	c := newToolCatalog(tools, nil)
	if c.search.Name != "search_available_tools_3" || !strings.Contains(c.instructions(), c.search.Name) {
		t.Fatalf("discovery name = %s", c.search.Name)
	}
	messages := []AgentMessage{
		{Role: "assistant", ToolCalls: []ToolCall{{Name: c.search.Name, Args: json.RawMessage(`{"query":"tool_0137"}`)}}},
		{Role: "tool", Content: `{"tools":[{"name":"forbidden_tool"}]}`},
	}
	restored := newToolCatalog(tools, messages)
	if len(restored.active) != 1 || restored.active[0].Name != tools[137].Name {
		t.Fatal("discovery was not restored from its arguments")
	}
	messages = append(messages, AgentMessage{Role: "assistant", ToolCalls: []ToolCall{{Name: tools[136].Name}, {Name: "forbidden_tool"}}})
	restored = newToolCatalog(tools, messages)
	if len(restored.active) != 1 || restored.active[0].Name != tools[136].Name {
		t.Fatal("recent permitted tool was not restored")
	}
	tools = tools[:137]
	restored = newToolCatalog(tools, messages[:2])
	if len(restored.active) != 0 {
		t.Fatal("a tool removed from the current registry was restored")
	}
}
