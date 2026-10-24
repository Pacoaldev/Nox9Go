//go:build integration

package integration

import (
	"encoding/json"
	"fmt"
	"net/http"
	"testing"
)

// The dashboard keys a provider node's custom-model rows with the node id —
// ProviderDetailView stores them under storageAlias, which is selectedNode.id
// — while every reader addresses the model by the node's display prefix
// (/v1/models publishes "prefix/id"; the resolver maps prefix → node). The
// candidate builder used to skip any row whose alias was neither a prefix nor
// a registry id, so a rule aimed at those models reported "Adds 0 of 0
// matches" while the unit tests stayed green: they seeded the custom row under
// the prefix, a shape the dashboard never writes.
//
// This drives the exact production shape through the real router.
func TestSmartRulesMatchCustomModelsKeyedByNodeID(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())

	if _, err := env.Repo.CreateProviderNode("node-cutad", "openai-compatible", "CutadAI",
		`{"prefix":"cutad","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	priority := 1
	data := `{"apiKey":"sk-cutad","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cutad"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cutad", "node-cutad", "apikey", "Cutad", &priority, data); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	// Production shape: key AND value carry the node id, not the prefix.
	if err := env.Repo.SetKV("customModels", "node-cutad|deepseek-v4-flash|llm",
		`{"providerAlias":"node-cutad","id":"deepseek-v4-flash","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-ds", "ds-all", []string{})

	cases := []struct {
		pattern string
		mode    string
		want    string
	}{
		{"deepseek*", "glob", "cutad/deepseek-v4-flash"},
		{"deepseek", "keyword", "cutad/deepseek-v4-flash"},
		{"cutad/deepseek-v4-flash", "glob", "cutad/deepseek-v4-flash"},
		{"re:^cutad/deepseek-v4-flash$", "regex", "cutad/deepseek-v4-flash"},
	}
	for _, tc := range cases {
		res := env.Post(t, "/api/combos/combo-ds/smart/preview", map[string]any{
			"matchRules": []any{map[string]any{"pattern": tc.pattern, "mode": tc.mode}},
		})
		if res.Status != http.StatusOK {
			t.Fatalf("%s %s: POST preview = %d, want 200 (body: %s)",
				tc.mode, tc.pattern, res.Status, truncate(res.Body))
		}
		var out struct {
			Candidates int `json:"candidates"`
			Added      []string
			Rules      []struct {
				Matched int `json:"matched"`
				Applied int `json:"applied"`
			} `json:"rules"`
		}
		if err := json.Unmarshal(res.Body, &out); err != nil {
			t.Fatalf("%s %s: decode: %v", tc.mode, tc.pattern, err)
		}
		if out.Candidates != 1 || len(out.Added) != 1 || out.Added[0] != tc.want ||
			len(out.Rules) != 1 || out.Rules[0].Matched != 1 || out.Rules[0].Applied != 1 {
			t.Fatalf("%s %q: got candidates=%d added=%v rules=%+v, want the one model %q",
				tc.mode, tc.pattern, out.Candidates, out.Added, out.Rules, tc.want)
		}
	}
	fmt.Printf("node-id-keyed candidates matched under all four modes\n")

	// The stored membership keeps the prefix spelling: it is the one the
	// resolver and /v1/models agree on. A dry run must not have written it.
	stored, err := env.Repo.GetComboRules(t.Context(), "combo-ds")
	if err != nil {
		t.Fatalf("GetComboRules: %v", err)
	}
	if stored != "" && stored != "[]" {
		t.Errorf("the preview wrote rules: %q", stored)
	}

	// Saving the rules materializes the member under the prefix spelling.
	res := env.Post(t, "/api/combos", map[string]any{
		"id":         "combo-save",
		"name":       "ds-save",
		"models":     []string{},
		"matchRules": []any{map[string]any{"pattern": "deepseek*", "mode": "glob"}},
	})
	if res.Status != http.StatusOK {
		t.Fatalf("create combo with rules = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	combo, err := env.Repo.GetComboById("combo-save")
	if err != nil {
		t.Fatalf("GetComboById: %v", err)
	}
	if combo == nil {
		t.Fatalf("combo-save was not stored")
	}
	var members []string
	if err := json.Unmarshal([]byte(combo.Models), &members); err != nil {
		t.Fatalf("decode combo models %q: %v", combo.Models, err)
	}
	if len(members) != 1 || members[0] != "cutad/deepseek-v4-flash" {
		t.Fatalf("saved members = %v, want [cutad/deepseek-v4-flash]", members)
	}
}

// A member written under the node-id spelling survives a reconcile: the
// candidate's AltID keeps the second spelling live so a rule snapshot stored
// before the fix is not pruned.
func TestSmartRulesKeepNodeIDSpelledMember(t *testing.T) {
	env := newEnv(t)
	upstream := env.NewUpstream(t, chatCompletionResponder())

	if _, err := env.Repo.CreateProviderNode("node-cutad", "openai-compatible", "CutadAI",
		`{"prefix":"cutad","apiType":"openai-compatible"}`); err != nil {
		t.Fatalf("create node: %v", err)
	}
	priority := 1
	data := `{"apiKey":"sk-cutad","baseUrl":"` + upstream.URL + `/chat/completions","providerSpecificData":{"prefix":"cutad"}}`
	if err := env.Repo.CreateProviderConnectionFull("conn-cutad", "node-cutad", "apikey", "Cutad", &priority, data); err != nil {
		t.Fatalf("create connection: %v", err)
	}
	if err := env.Repo.SetKV("customModels", "node-cutad|deepseek-v4-flash|llm",
		`{"providerAlias":"node-cutad","id":"deepseek-v4-flash","type":"llm"}`); err != nil {
		t.Fatalf("seed custom model: %v", err)
	}
	env.AddCombo(t, "combo-ds", "ds-all", []string{"node-cutad/deepseek-v4-flash"})
	if err := env.Repo.SetComboRules(t.Context(), "combo-ds",
		`[{"id":"r1","pattern":"deepseek*","mode":"glob","effect":"include","enabled":true,"maxMembers":20,"allKinds":false,"resolved":["node-cutad/deepseek-v4-flash"]}]`); err != nil {
		t.Fatalf("store rules: %v", err)
	}

	res := env.Post(t, "/api/combos/reconcile", nil)
	if res.Status != http.StatusOK {
		t.Fatalf("POST reconcile = %d, want 200 (body: %s)", res.Status, truncate(res.Body))
	}
	var applied struct {
		Reconciled int      `json:"reconciled"`
		Removed    []string `json:"removed"`
	}
	res.Decode(t, &applied)
	if applied.Reconciled != 0 {
		t.Errorf("reconciled = %d, want 0 (the node-id member must survive)", applied.Reconciled)
	}
	if len(applied.Removed) != 0 {
		t.Errorf("removed = %v, want nothing pruned", applied.Removed)
	}
}
