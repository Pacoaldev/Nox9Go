package handlers

import (
	json "encoding/json/v2"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

// TestSuggestedModelsRequestyFree covers the Requesty router filter. Its
// catalog states the rates as top-level numbers (dollars per token) and carries
// no pricing map, so filterOpenRouterFree would drop every row: a filter keyed
// on the wrong field returns nothing and the section silently empties.
func TestSuggestedModelsRequestyFree(t *testing.T) {
	feed := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.Write([]byte(`{"object":"list","data":[
			{"id":"nvidia/muse-glimmer-30b","input_price":0,"output_price":0,"context_window":131072},
			{"id":"nvidia/nemotron-3-ultra-550b-a55b","input_price":0,"output_price":0,"context_window":1048576},
			{"id":"openai/gpt-6.1-sol","input_price":1.35e-06,"output_price":1.08e-05,"context_window":1050000},
			{"id":"paid-output","input_price":0,"output_price":3e-08,"context_window":262144},
			{"id":"no-pricing","context_window":262144}
		]}`))
	}))
	defer feed.Close()

	req := httptest.NewRequest(http.MethodGet,
		"/api/providers/suggested-models?url="+url.QueryEscape(feed.URL)+"&type=requesty-free", nil)
	rec := httptest.NewRecorder()
	HandleSuggestedModels(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", rec.Code, rec.Body.String())
	}
	var out struct {
		Data []suggestedModel `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &out); err != nil {
		t.Fatalf("failed to unmarshal: %v", err)
	}
	want := []string{"nvidia/muse-glimmer-30b", "nvidia/nemotron-3-ultra-550b-a55b"}
	if len(out.Data) != len(want) {
		t.Fatalf("expected %v, got %v", want, out.Data)
	}
	for i := range want {
		if out.Data[i].ID != want[i] {
			t.Fatalf("expected %v, got %v", want, out.Data)
		}
	}
	if out.Data[1].ContextLength == nil || *out.Data[1].ContextLength != 1048576 {
		t.Fatalf("expected contextLength 1048576, got %v", out.Data[1])
	}
}
