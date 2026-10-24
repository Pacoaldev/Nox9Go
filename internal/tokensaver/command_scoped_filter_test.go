package tokensaver

import (
	"encoding/json"
	"strings"
	"testing"
)

// A tool result is the output of one command, and the filter set knows which
// command each of its rules was written for: 53 of the 55 filters declare the
// commands they belong to. The command is in the request body — the tool call
// carries it in its arguments — so the filter can be chosen by the command
// that produced the output.
//
// Chosen by content alone, the same rules fire on a file that merely mentions
// what the filter keys on. A 5,638-byte `sed -n 1,45p` result whose file
// happened to mention git came back as the 24-byte string
// "git: already on branch", and the model had no way to tell that the content
// had been replaced.
func TestCommandScopedFilterIsNotChosenByContentAlone(t *testing.T) {
	cfg := DefaultRTKConfig()

	// The output of `sed -n 1,45p notes.txt`, where the file mentions git.
	content := "  build ok\n* item one\n  item two\nAlready on\n" +
		strings.Repeat("some file content line\n", 200)

	body, err := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "bash",
							"arguments": `{"command":"sed -n 1,45p notes.txt"}`,
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call_1",
				"content":      content,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out, _ := CompressMessagesWithConfig(body, cfg)
	got := string(out)
	if strings.Contains(got, "git: already on branch") {
		t.Errorf("a sed result was replaced by the git-branch summary: %s", truncateForMessage(got))
	}
	if !strings.Contains(got, "some file content line") {
		t.Errorf("the file content was lost: %s", truncateForMessage(got))
	}
}

// The same output, produced by the command the filter was written for, is
// still summarised. The fix is not "compress less"; it is "compress the
// output that belongs to the filter".
func TestCommandScopedFilterStillAppliesToItsOwnCommand(t *testing.T) {
	cfg := DefaultRTKConfig()

	content := "  main\n* feature/rtk\n  release\n" + strings.Repeat("noise line\n", 200)

	body, err := json.Marshal(map[string]any{
		"messages": []any{
			map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":   "call_1",
						"type": "function",
						"function": map[string]any{
							"name":      "bash",
							"arguments": `{"command":"git branch"}`,
						},
					},
				},
			},
			map[string]any{
				"role":         "tool",
				"tool_call_id": "call_1",
				"content":      content,
			},
		},
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	out, changed := CompressMessagesWithConfig(body, cfg)
	if !changed {
		t.Fatal("a git branch result must still be compressed")
	}
	got := string(out)
	if strings.Contains(got, "noise line") {
		t.Errorf("the git branch result was not filtered: %s", truncateForMessage(got))
	}
	if !strings.Contains(got, "feature/rtk") {
		t.Errorf("the branch name was lost: %s", truncateForMessage(got))
	}
}

// The command is read from the tool call's arguments, in the shapes the
// three protocols use.
func TestCommandIsResolvedFromEveryToolCallShape(t *testing.T) {
	cases := []struct {
		name string
		item map[string]any
		want string
	}{
		{
			name: "chat completions, arguments as a JSON string",
			item: map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call_1",
						"function": map[string]any{"name": "bash", "arguments": `{"command":"git status"}`},
					},
				},
			},
			want: "git status",
		},
		{
			name: "responses, arguments as a JSON string",
			item: map[string]any{
				"type":      "function_call",
				"call_id":   "call_2",
				"name":      "bash",
				"arguments": `{"command":"git diff"}`,
			},
			want: "git diff",
		},
		{
			name: "claude tool_use, input as an object",
			item: map[string]any{
				"role": "assistant",
				"content": []any{
					map[string]any{
						"type":  "tool_use",
						"id":    "call_3",
						"name":  "bash",
						"input": map[string]any{"command": "git log"},
					},
				},
			},
			want: "git log",
		},
		{
			name: "the cmd spelling the translator's sanitiser accepts",
			item: map[string]any{
				"role": "assistant",
				"tool_calls": []any{
					map[string]any{
						"id":       "call_4",
						"function": map[string]any{"name": "bash", "arguments": `{"cmd":"git status"}`},
					},
				},
			},
			want: "git status",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, cmdMap := buildToolCallMap([]map[string]any{tc.item})
			var got string
			for _, v := range cmdMap {
				got = v
			}
			if got != tc.want {
				t.Errorf("command = %q, want %q", got, tc.want)
			}
		})
	}
}

// A tool call that carries no command leaves the command unknown, and the
// content-scoped filters are all that may apply. That is the case the filter
// set is written for.
func TestCommandlessToolCallResolvesNoCommand(t *testing.T) {
	_, cmdMap := buildToolCallMap([]map[string]any{
		{
			"role": "assistant",
			"tool_calls": []any{
				map[string]any{
					"id":       "call_1",
					"function": map[string]any{"name": "read", "arguments": `{"path":"/tmp/x"}`},
				},
			},
		},
	})
	if len(cmdMap) != 0 {
		t.Errorf("cmdMap = %v, want empty", cmdMap)
	}
}

func truncateForMessage(s string) string {
	if len(s) > 400 {
		return s[:400] + "..."
	}
	return s
}
