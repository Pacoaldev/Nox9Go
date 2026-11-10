package chat

import (
	"bytes"
	"strings"

	json "encoding/json/v2"

	"nox9go/proxy/internal/providers"
)

// Media content blocks a model cannot read are removed from the client body and
// replaced with a short text placeholder, so the request degrades to what the
// model can answer instead of being rejected upstream.
//
// Port of upstream stripUnsupportedModalities (open-sse/translator/concerns/modality.js),
// called from chatCore before translation. Without it an image block reached a
// text-only model verbatim — kiro/glm-5 answers 400 IMAGE_FORMAT_UNSUPPORTED —
// because nothing else in the request path inspects the target's capabilities.
//
// Two placeholders exist, mirroring upstream: the current turn explains what was
// dropped, an older turn is neutral (a combo may route a different model each
// turn and the history media is already stripped or placeholdered downstream).

// Capability keys the strip understands. These are the modality names the
// capacity adapter and DetectRequiredCapabilities already use.
const (
	capVision     = "vision"
	capPDF        = "pdf"
	capAudioInput = "audioInput"
)

const (
	phImageNow  = "[image omitted: model has no vision support]"
	phAudioNow  = "[audio omitted: model has no audio support]"
	phFileNow   = "[file omitted: model has no document support]"
	phImagePrev = "[Previous image omitted from context.]"
	phAudioPrev = "[Previous audio omitted from context.]"
	phFilePrev  = "[Previous file omitted from context.]"
)

// stripUnsupportedModalities removes every input-modality block the target model
// cannot read, replacing each removed kind with one text placeholder. The body is
// returned untouched when the model supports vision, audio and documents, when it
// parses as something other than a known request shape, or when nothing needed
// dropping.
func stripUnsupportedModalities(body []byte, caps providers.Capabilities) []byte {
	if len(body) == 0 {
		return body
	}
	// Fast exit: the model reads everything this step would strip.
	if caps.Vision && caps.AudioInput && caps.PDF {
		return body
	}
	// Cheap pre-gate: a body whose bytes carry no media marker cannot have a
	// block to strip, so the JSON decode below is skipped for the common
	// text-only turn. Every carrier the walker knows leaves one of these
	// substrings in the raw body.
	if !hasMediaMarker(body, caps) {
		return body
	}

	var m map[string]any
	if err := json.Unmarshal(body, &m); err != nil {
		return body
	}

	changed := false
	if msgs, ok := m["messages"].([]any); ok {
		if stripMessages(msgs, caps) {
			changed = true
		}
	}
	// OpenAI Responses: input[].content[] carries input_image / input_file.
	if items, ok := m["input"].([]any); ok {
		if stripResponsesInput(items, caps) {
			changed = true
		}
	}
	// Gemini / Antigravity: contents[].parts[]. Some envelopes nest them.
	if contents, ok := m["contents"].([]any); ok {
		if stripGeminiParts(contents, caps) {
			changed = true
		}
	}
	if req, ok := m["request"].(map[string]any); ok {
		if contents, ok := req["contents"].([]any); ok {
			if stripGeminiParts(contents, caps) {
				changed = true
			}
		}
	}

	if !changed {
		return body
	}
	out, err := json.Marshal(m)
	if err != nil {
		return body
	}
	return out
}

// hasMediaMarker reports whether the raw body carries any substring a media
// block or media carrier would introduce. It is a conservative gate: a false
// positive only costs the JSON decode that would have happened anyway, and a
// false negative is impossible while every carrier keeps its marker.
func hasMediaMarker(body []byte, caps providers.Capabilities) bool {
	if !caps.Vision {
		if bytes.Contains(body, []byte(`"image`)) ||
			bytes.Contains(body, []byte(`"attachments"`)) ||
			bytes.Contains(body, []byte(`"experimental_attachments"`)) ||
			bytes.Contains(body, []byte(`"inlineData"`)) ||
			bytes.Contains(body, []byte(`"fileData"`)) {
			return true
		}
	}
	if !caps.AudioInput {
		if bytes.Contains(body, []byte(`"input_audio`)) ||
			bytes.Contains(body, []byte(`"audio_url"`)) ||
			bytes.Contains(body, []byte(`"audio/"`)) ||
			bytes.Contains(body, []byte(`"audio":`)) {
			return true
		}
	}
	if !caps.PDF {
		if bytes.Contains(body, []byte(`"file"`)) ||
			bytes.Contains(body, []byte(`"document"`)) ||
			bytes.Contains(body, []byte(`"input_file"`)) ||
			bytes.Contains(body, []byte("application/pdf")) {
			return true
		}
	}
	return false
}

// stripMessages walks OpenAI / Claude / Ollama messages, reporting whether the
// body was modified.
func stripMessages(msgs []any, caps providers.Capabilities) bool {
	last := len(msgs) - 1
	changed := false
	for i, raw := range msgs {
		msg, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		isLast := i == last

		if !caps.Vision {
			// Ollama / Hermes message-level images array.
			if _, ok := msg["images"]; ok {
				delete(msg, "images")
				changed = true
			}
			// Vercel AI SDK / Hermes attachment carriers.
			changed = dropImageAttachments(msg, "experimental_attachments") || changed
			changed = dropImageAttachments(msg, "attachments") || changed
			// Hermes message-level modality properties.
			if _, ok := msg["image_url"]; ok {
				delete(msg, "image_url")
				changed = true
			}
			if _, ok := msg["image"]; ok {
				delete(msg, "image")
				changed = true
			}
		}

		if content, ok := msg["content"].([]any); ok {
			out, did := filterMediaBlocks(content, caps, isLast, textBlockPlaceholder)
			if did {
				msg["content"] = out
				changed = true
			}
		}
	}
	return changed
}

// stripResponsesInput walks the Responses API input array.
func stripResponsesInput(items []any, caps providers.Capabilities) bool {
	last := len(items) - 1
	changed := false
	for i, raw := range items {
		item, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		content, ok := item["content"].([]any)
		if !ok {
			continue
		}
		out, did := filterMediaBlocks(content, caps, i == last, responsesTextPlaceholder)
		if did {
			item["content"] = out
			changed = true
		}
	}
	return changed
}

// stripGeminiParts walks Gemini contents[].parts[].
func stripGeminiParts(contents []any, caps providers.Capabilities) bool {
	last := len(contents) - 1
	changed := false
	for i, raw := range contents {
		entry, ok := raw.(map[string]any)
		if !ok {
			continue
		}
		parts, ok := entry["parts"].([]any)
		if !ok {
			continue
		}
		out, did := filterMediaBlocks(parts, caps, i == last, geminiTextPlaceholder)
		if did {
			entry["parts"] = out
			changed = true
		}
	}
	return changed
}

// placeholder builds the text block substituted for one removed modality.
type placeholder func(cap string, isLast bool) map[string]any

func textBlockPlaceholder(cap string, isLast bool) map[string]any {
	return map[string]any{"type": "text", "text": placeholderText(cap, isLast)}
}

func responsesTextPlaceholder(cap string, isLast bool) map[string]any {
	return map[string]any{"type": "input_text", "text": placeholderText(cap, isLast)}
}

func geminiTextPlaceholder(cap string, isLast bool) map[string]any {
	return map[string]any{"text": placeholderText(cap, isLast)}
}

func placeholderText(cap string, isLast bool) string {
	if isLast {
		switch cap {
		case capVision:
			return phImageNow
		case capAudioInput:
			return phAudioNow
		case capPDF:
			return phFileNow
		}
	}
	switch cap {
	case capVision:
		return phImagePrev
	case capAudioInput:
		return phAudioPrev
	case capPDF:
		return phFilePrev
	}
	return ""
}

// filterMediaBlocks drops every block whose required capability the model lacks
// and appends one placeholder per removed kind, in a stable order. It reports
// whether the array changed.
func filterMediaBlocks(blocks []any, caps providers.Capabilities, isLast bool, ph placeholder) ([]any, bool) {
	out := make([]any, 0, len(blocks))
	var removed []string
	seen := make(map[string]bool)
	changed := false

	for _, raw := range blocks {
		block, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		cap := blockCapability(block)
		if cap != "" && !capabilitySupported(caps, cap) {
			if !seen[cap] {
				seen[cap] = true
				removed = append(removed, cap)
			}
			changed = true
			continue
		}
		out = append(out, raw)
	}

	if !changed {
		return blocks, false
	}
	for _, cap := range removed {
		out = append(out, ph(cap, isLast))
	}
	return out, true
}

// blockCapability reads which modality a content block needs, or "" when the
// block is text/other. One mapper covers the OpenAI, Claude, Responses and
// Gemini block shapes: the type names do not collide, and the Gemini inline
// parts carry their modality in the mime.
func blockCapability(b map[string]any) string {
	typ, _ := b["type"].(string)
	switch typ {
	case "image_url", "image", "input_image":
		return capVision
	case "input_audio", "audio_url", "audio", "input_audio_url":
		return capAudioInput
	case "file", "document", "input_file":
		if mime := fileBlockMime(b); mime != "" {
			return capForMime(mime)
		}
		return capPDF
	}
	if inner, ok := b["inlineData"].(map[string]any); ok {
		return capForMime(stringOf(inner, "mimeType"))
	}
	if inner, ok := b["fileData"].(map[string]any); ok {
		return capForMime(stringOf(inner, "mimeType"))
	}
	return ""
}

// capForMime maps a mime type to the modality it needs. A mime that names no
// modality we strip returns "".
func capForMime(mime string) string {
	switch {
	case mime == "":
		return ""
	case strings.HasPrefix(mime, "image/"):
		return capVision
	case mime == "application/pdf":
		return capPDF
	case strings.HasPrefix(mime, "audio/"):
		return capAudioInput
	}
	return ""
}

// capabilitySupported reports whether caps covers one modality key.
func capabilitySupported(caps providers.Capabilities, cap string) bool {
	switch cap {
	case capVision:
		return caps.Vision
	case capPDF:
		return caps.PDF
	case capAudioInput:
		return caps.AudioInput
	}
	return true
}

// dropImageAttachments removes image entries from one attachment carrier on a
// message, reporting whether anything was removed. Only image attachments are
// dropped here: an audio/pdf attachment is handled by the block walker.
func dropImageAttachments(msg map[string]any, key string) bool {
	arr, ok := msg[key].([]any)
	if !ok {
		return false
	}
	out := make([]any, 0, len(arr))
	changed := false
	for _, raw := range arr {
		att, ok := raw.(map[string]any)
		if !ok {
			out = append(out, raw)
			continue
		}
		mime := firstString(att, "contentType", "mediaType")
		if mime == "" {
			if url, ok := att["url"].(string); ok {
				mime = dataURIMime(url)
			}
		}
		if strings.HasPrefix(mime, "image/") {
			changed = true
			continue
		}
		out = append(out, raw)
	}
	if !changed {
		return false
	}
	msg[key] = out
	return true
}
