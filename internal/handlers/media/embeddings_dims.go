package media

import (
	json "encoding/json/v2"
)

// forwardGeminiEmbeddingDimensions maps the OpenAI "dimensions" field to
// Gemini's "outputDimensionality". Bodies without a positive dimensions value
// pass through unchanged.
func forwardGeminiEmbeddingDimensions(body []byte) []byte {
	var payload map[string]any
	if err := json.Unmarshal(body, &payload); err != nil {
		return body
	}
	dims, ok := payload["dimensions"].(float64)
	if !ok || dims < 1 {
		return body
	}
	payload["outputDimensionality"] = int(dims)
	out, err := json.Marshal(payload)
	if err != nil {
		return body
	}
	return out
}