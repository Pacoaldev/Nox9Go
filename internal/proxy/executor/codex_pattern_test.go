package executor

import (
	"testing"
)

func TestStripCodexUnsupportedPatterns(t *testing.T) {
	unicodePattern := `^(?!__.*__$)[^\p{Cc}\p{Cf}\p{Zl}\p{Zp}]{1,200}$`
	validPattern := `^[a-z][a-z0-9_-]{0,31}$`

	source := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"validField": map[string]any{
				"type":    "string",
				"pattern": validPattern,
			},
			"invalidField": map[string]any{
				"type":    "string",
				"pattern": unicodePattern,
			},
			"nested": map[string]any{
				"type": "object",
				"properties": map[string]any{
					"deepInvalid": map[string]any{
						"type":    "string",
						"pattern": unicodePattern,
					},
				},
			},
		},
	}

	cleaned := StripCodexUnsupportedPatterns(source)

	props := cleaned["properties"].(map[string]any)
	vf := props["validField"].(map[string]any)
	if vf["pattern"] != validPattern {
		t.Errorf("expected valid pattern preserved, got %v", vf["pattern"])
	}

	invf := props["invalidField"].(map[string]any)
	if _, ok := invf["pattern"]; ok {
		t.Errorf("expected invalidField pattern stripped, got %v", invf["pattern"])
	}

	nested := props["nested"].(map[string]any)
	nestedProps := nested["properties"].(map[string]any)
	deepInv := nestedProps["deepInvalid"].(map[string]any)
	if _, ok := deepInv["pattern"]; ok {
		t.Errorf("expected deepInvalid pattern stripped, got %v", deepInv["pattern"])
	}
}
