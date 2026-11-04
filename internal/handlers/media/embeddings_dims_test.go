package media

import (
	json "encoding/json/v2"
	"testing"
)

func TestForwardGeminiEmbeddingDimensions(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want int
	}{
		{"maps dimensions", `{"model":"m","dimensions":1536}`, 1536},
		{"no dimensions", `{"model":"m"}`, 0},
		{"non-positive ignored", `{"model":"m","dimensions":0}`, 0},
		{"invalid json passes through", `{bad`, 0},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := forwardGeminiEmbeddingDimensions([]byte(tt.in))
			var got struct {
				Dims int `json:"outputDimensionality"`
			}
			_ = json.Unmarshal(out, &got)
			if got.Dims != tt.want {
				t.Errorf("outputDimensionality = %d, want %d", got.Dims, tt.want)
			}
		})
	}
}