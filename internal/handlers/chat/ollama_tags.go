package chat

// Ollama-compatible model listing.
//
// The gateway answers POST /api/chat (Ollama's chat endpoint) but had no
// GET /api/tags, which is how every Ollama client enumerates models before it
// chats. Without it the client concludes the host has no models and never sends
// the request, so the chat half was unreachable in practice.
//
// The list is a static, documented shape rather than the gateway's real
// catalogue: an Ollama client only uses it to pick a name to send back, and the
// gateway resolves that name like any other model. Publishing the whole provider
// catalogue here would advertise hundreds of names in a picker meant for a local
// model store.

import (
	"net/http"

	"nox9go/proxy/internal/handlerutil"
)

// ollamaTagsPayload is the Ollama /api/tags document. Details are filled for
// client compatibility: Ollama's own UI reads format and family to group rows.
var ollamaTagsPayload = map[string]any{
	"models": []map[string]any{
		{
			"name":        "llama3.2",
			"modified_at": "2025-12-26T00:00:00Z",
			"size":        2000000000,
			"digest":      "abc123def456",
			"details": map[string]any{
				"format":             "gguf",
				"family":             "llama",
				"parameter_size":     "3B",
				"quantization_level": "Q4_K_M",
			},
		},
		{
			"name":        "qwen2.5",
			"modified_at": "2025-12-26T00:00:00Z",
			"size":        4000000000,
			"digest":      "def456abc123",
			"details": map[string]any{
				"format":             "gguf",
				"family":             "qwen",
				"parameter_size":     "7B",
				"quantization_level": "Q4_K_M",
			},
		},
	},
}

// HandleOllamaTags answers GET /api/tags (and its /v1/api/tags alias).
func (h *ChatHandler) HandleOllamaTags(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Access-Control-Allow-Origin", "*")
	w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
	w.Header().Set("Access-Control-Allow-Headers", "*")
	if r.Method == http.MethodOptions {
		w.WriteHeader(http.StatusNoContent)
		return
	}
	handlerutil.WriteJSON(w, http.StatusOK, ollamaTagsPayload)
}
