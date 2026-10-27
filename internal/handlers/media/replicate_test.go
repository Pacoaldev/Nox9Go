package media

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestReplicatePredictionURL covers the two model id forms Replicate accepts.
// Getting this wrong is not a soft failure: an official model (owner/name) sent
// to /v1/predictions with no version is a 422, and a versioned id sent to the
// model path is a 404 that reads like a bad credential.
func TestReplicatePredictionURL(t *testing.T) {
	cases := []struct {
		model       string
		wantURL     string
		wantVersion string
		wantErr     bool
	}{
		{
			model:   "black-forest-labs/flux-1.1-pro",
			wantURL: "https://api.replicate.com/v1/models/black-forest-labs/flux-1.1-pro/predictions",
		},
		// A fine-tuned or private model lives behind a deployment; sending it
		// down the official-model path is a 404 that reads like a bad key.
		{
			model:   "deployments/me/my-flux",
			wantURL: "https://api.replicate.com/v1/deployments/me/my-flux/predictions",
		},
		{model: "deployments/me", wantErr: true},
		{
			model:       "replicate/hello-world:5c7d5dc6dd8bf75c1acaa8565735e7986bc5b66206b55cca93cb72c9bf15ccaa",
			wantURL:     "https://api.replicate.com/v1/predictions",
			wantVersion: "5c7d5dc6dd8bf75c1acaa8565735e7986bc5b66206b55cca93cb72c9bf15ccaa",
		},
		// A bare id is what a caller who typed the catalog provider id sends;
		// it must be refused with a message naming the required form rather
		// than forwarded to a route that will answer with an auth error.
		{model: "replicate", wantErr: true},
		{model: "justoneword", wantErr: true},
		{model: "", wantErr: true},
		{model: "owner/name:", wantErr: true},
	}
	for _, tc := range cases {
		gotURL, gotVersion, err := replicatePredictionURL(tc.model)
		if tc.wantErr {
			if err == nil {
				t.Errorf("%q: expected an error, got url %q", tc.model, gotURL)
			}
			continue
		}
		if err != nil {
			t.Errorf("%q: unexpected error: %v", tc.model, err)
			continue
		}
		if gotURL != tc.wantURL {
			t.Errorf("%q: url = %q, want %q", tc.model, gotURL, tc.wantURL)
		}
		if gotVersion != tc.wantVersion {
			t.Errorf("%q: version = %q, want %q", tc.model, gotVersion, tc.wantVersion)
		}
	}
}

// TestReplicateUpstreamMessage keeps the auth failure readable. Replicate says
// "no API key found" when it simply did not receive one, which is what sent the
// operator looking at their key when the problem was the route.
func TestReplicateUpstreamMessage(t *testing.T) {
	cases := []struct {
		body string
		want string
	}{
		{`{"detail":"Request was throttled"}`, "Request was throttled"},
		{`{"error":"invalid_api_key","message":"no API key found"}`, "invalid_api_key: no API key found"},
		{`{"title":"Not Found"}`, "Not Found"},
	}
	for _, tc := range cases {
		if got := replicateUpstreamMessage([]byte(tc.body)); got != tc.want {
			t.Errorf("body %s: got %q, want %q", tc.body, got, tc.want)
		}
	}
}

// TestReplicateOutputURL covers the three output shapes Replicate models use: a
// bare URL, a list, and an object. Assuming one shape turns a successful job
// into an empty result.
func TestReplicateOutputURL(t *testing.T) {
	cases := []struct {
		output any
		want   string
	}{
		{"https://replicate.delivery/x/out.mp4", "https://replicate.delivery/x/out.mp4"},
		{[]any{"https://replicate.delivery/a/1.mp4", "https://replicate.delivery/a/2.mp4"}, "https://replicate.delivery/a/1.mp4"},
		{map[string]any{"url": "https://replicate.delivery/b/v.mp4"}, "https://replicate.delivery/b/v.mp4"},
		{map[string]any{"video": "https://replicate.delivery/c/w.mp4"}, "https://replicate.delivery/c/w.mp4"},
		{map[string]any{"image": "https://replicate.delivery/d/i.png"}, "https://replicate.delivery/d/i.png"},
		{map[string]any{"unrelated": 1}, ""},
		{nil, ""},
	}
	for _, tc := range cases {
		if got := replicateOutputURL(tc.output); got != tc.want {
			t.Errorf("output %#v: got %q, want %q", tc.output, got, tc.want)
		}
	}
}

// TestReplicateOutputURLsKeepsEveryImage covers the multi-image case: an image
// model that returns several outputs must not be reduced to the first one.
func TestReplicateOutputURLsKeepsEveryImage(t *testing.T) {
	output := []any{
		"https://replicate.delivery/a/1.png",
		"https://replicate.delivery/a/2.png",
	}
	got := replicateOutputURLs(output)
	if len(got) != 2 {
		t.Fatalf("got %d urls, want 2: %#v", len(got), got)
	}
	if got[1] != "https://replicate.delivery/a/2.png" {
		t.Errorf("second url = %q", got[1])
	}
}

// TestIsVideoEndpointExcludesEdits pins the scope decision: Replicate has no
// edit or extend endpoint, so those lanes must not be routed here where they
// would spend a prediction on a body the model ignores.
func TestIsVideoEndpointExcludesEdits(t *testing.T) {
	if !isVideoEndpoint("/v1/videos/generations") {
		t.Error("generations must be routed to Replicate")
	}
	for _, ep := range []string{"/v1/videos/edits", "/v1/videos/extensions"} {
		if isVideoEndpoint(ep) {
			t.Errorf("%s must not be routed to Replicate: no such upstream endpoint", ep)
		}
	}
	if !isImageEndpoint("/v1/images/generations") {
		t.Error("image generations must be routed to Replicate")
	}
}

// TestWriteReplicateImageResult covers the OpenAI image contract, including the
// n cap and the data-URI form some Flux builds answer with.
func TestWriteReplicateImageResult(t *testing.T) {
	rec := httptest.NewRecorder()
	pred := &replicatePrediction{
		ID:     "pred_img",
		Status: "succeeded",
		Output: []any{
			"https://replicate.delivery/x/1.png",
			"https://replicate.delivery/x/2.png",
		},
	}
	writeReplicateImageResult(rec, "black-forest-labs/flux-1.1-pro", pred, 1)

	var body struct {
		Data []struct {
			URL string `json:"url"`
		} `json:"data"`
		URL string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 1 {
		t.Fatalf("n=1 must cap the data list, got %d entries", len(body.Data))
	}
	if !strings.HasSuffix(body.URL, "1.png") {
		t.Errorf("url = %q", body.URL)
	}
}

// TestWriteReplicateImageDataURI keeps a data-URI output readable as b64_json.
func TestWriteReplicateImageDataURI(t *testing.T) {
	rec := httptest.NewRecorder()
	pred := &replicatePrediction{
		ID:     "pred_uri",
		Status: "succeeded",
		Output: "data:image/png;base64,iVBORw0KGgo=",
	}
	writeReplicateImageResult(rec, "owner/model", pred, 0)

	var body struct {
		Data []struct {
			B64 string `json:"b64_json"`
		} `json:"data"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Data) != 1 || body.Data[0].B64 != "iVBORw0KGgo=" {
		t.Errorf("b64 not extracted: %+v", body.Data)
	}
}

// TestReplicateRequestBuildsInput pins the request shape sent upstream: the
// prompt and any caller-supplied fields both land in "input", and a caller who
// already shaped an input object keeps it.
func TestReplicateRequestBuildsInput(t *testing.T) {
	var req replicateRequest
	if err := json.Unmarshal([]byte(`{"model":"black-forest-labs/flux-1.1-pro","prompt":"a serene lake at sunset","input":{"aspect_ratio":"16:9"}}`), &req); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if req.Prompt != "a serene lake at sunset" {
		t.Errorf("prompt = %q", req.Prompt)
	}
	if req.Input["aspect_ratio"] != "16:9" {
		t.Errorf("input lost the caller's fields: %#v", req.Input)
	}
}

// TestWriteReplicateVideoResult checks the response shape the endpoint's
// callers parse: a completed video object with a url.
func TestWriteReplicateVideoResult(t *testing.T) {
	rec := httptest.NewRecorder()
	pred := &replicatePrediction{
		ID:     "pred_1",
		Status: "succeeded",
		Output: "https://replicate.delivery/x/out.mp4",
	}
	writeReplicateVideoResult(rec, "black-forest-labs/flux-1.1-pro", pred)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d", rec.Code)
	}
	var body struct {
		ID     string `json:"id"`
		Status string `json:"status"`
		URL    string `json:"url"`
	}
	if err := json.Unmarshal(rec.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.ID != "pred_1" || body.Status != "completed" {
		t.Errorf("unexpected body: %+v", body)
	}
	if !strings.HasSuffix(body.URL, "out.mp4") {
		t.Errorf("url = %q", body.URL)
	}
}

// TestReplicatePollRefusesNonHTTPS keeps a compromised prediction object from
// steering the poll at another scheme. The URL comes from the upstream body.
func TestReplicatePollRefusesNonHTTPS(t *testing.T) {
	h := &MediaHandler{}
	pred := &replicatePrediction{ID: "p1"}
	pred.URLs.Get = "http://evil.example/predictions/p1"
	req := httptest.NewRequest(http.MethodPost, "/v1/videos/generations", nil)
	if _, err := h.replicatePoll(req, http.DefaultClient, pred, "key"); err == nil {
		t.Fatal("expected a refusal for an http poll url")
	}
}
