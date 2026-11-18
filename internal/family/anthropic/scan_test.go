package anthropic

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func readFixture(t *testing.T, name string) []byte {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return b
}

func scanChunks(streamed bool, chunks ...[]byte) (Facts, bool) {
	s := NewScanner(streamed)
	for _, c := range chunks {
		s.Write(c)
	}
	return s.Facts(), s.ContentStarted()
}

func factsJSON(t *testing.T, f Facts) []byte {
	t.Helper()
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	return append(b, '\n')
}

func TestScannerGolden(t *testing.T) {
	tests := []struct {
		fixture  string
		streamed bool
		content  bool // ContentStarted
	}{
		{"sse_normal", true, true},
		{"sse_thinking_ping", true, true},
		{"sse_tool_use", true, true},
		{"sse_cumulative_usage", true, true},
		{"sse_error_midstream", true, true},
		{"sse_truncated", true, true},
		{"body_message", false, false},
		{"body_error", false, false},
	}
	for _, tt := range tests {
		t.Run(tt.fixture, func(t *testing.T) {
			ext := ".sse"
			if !tt.streamed {
				ext = ".json"
			}
			in := readFixture(t, tt.fixture+ext)
			f, content := scanChunks(tt.streamed, in)
			if content != tt.content {
				t.Errorf("ContentStarted = %v, want %v", content, tt.content)
			}
			got := factsJSON(t, f)
			path := filepath.Join("testdata", tt.fixture+".facts.golden.json")
			if *update {
				if err := os.WriteFile(path, got, 0o644); err != nil {
					t.Fatal(err)
				}
			}
			if want := readFixture(t, tt.fixture+".facts.golden.json"); string(got) != string(want) {
				t.Errorf("facts mismatch\n got: %s\nwant: %s", got, want)
			}
		})
	}
}

// TestScannerEveryByteOffset cuts every stream fixture at every byte offset and
// wants the facts of the uncut stream. A one-byte-per-chunk run covers the rest.
func TestScannerEveryByteOffset(t *testing.T) {
	for _, name := range []string{"sse_normal", "sse_thinking_ping", "sse_tool_use", "sse_cumulative_usage", "sse_error_midstream", "sse_truncated"} {
		t.Run(name, func(t *testing.T) {
			in := readFixture(t, name+".sse")
			want, wantContent := scanChunks(true, in)
			for i := 0; i <= len(in); i++ {
				got, content := scanChunks(true, in[:i:i], in[i:])
				if !reflect.DeepEqual(got, want) || content != wantContent {
					t.Fatalf("cut at %d: facts differ\n got: %s\nwant: %s", i, factsJSON(t, got), factsJSON(t, want))
				}
			}
			bytes := make([][]byte, len(in))
			for i := range in {
				bytes[i] = in[i : i+1]
			}
			if got, _ := scanChunks(true, bytes...); !reflect.DeepEqual(got, want) {
				t.Fatalf("byte by byte: facts differ\n got: %s\nwant: %s", factsJSON(t, got), factsJSON(t, want))
			}
		})
	}
}

func intp(n int) *int { return &n }

func TestScannerFacts(t *testing.T) {
	t.Run("normal stream", func(t *testing.T) {
		f, _ := scanChunks(true, readFixture(t, "sse_normal.sse"))
		want := Usage{InputTokens: intp(25), OutputTokens: intp(12), CacheReadInputTokens: intp(100),
			CacheCreationInputTokens: intp(40), Ephemeral5mInputTokens: intp(40), Ephemeral1hInputTokens: intp(0)}
		if !reflect.DeepEqual(f.Usage, want) {
			t.Errorf("usage = %+v", f.Usage)
		}
		if f.StopReason != "end_turn" || !f.Complete || f.Partial || f.ErrorType != "" {
			t.Errorf("facts = %+v", f)
		}
		if len(f.ToolUses) != 0 {
			t.Errorf("tool uses = %+v", f.ToolUses)
		}
	})
	t.Run("message_delta usage is cumulative and replaces message_start", func(t *testing.T) {
		// The streaming docs: the usage of message_delta is cumulative. A
		// server-tool call starts at 2679 input tokens and ends at 10682, so a
		// scanner that kept the message_start count would undercount about 4 times.
		f, _ := scanChunks(true, readFixture(t, "sse_cumulative_usage.sse"))
		want := Usage{InputTokens: intp(10682), OutputTokens: intp(510), CacheReadInputTokens: intp(4000),
			CacheCreationInputTokens: intp(200), Ephemeral5mInputTokens: intp(200), Ephemeral1hInputTokens: intp(0)}
		if !reflect.DeepEqual(f.Usage, want) {
			t.Errorf("usage = %+v", f.Usage)
		}
		var m map[string]json.RawMessage
		if err := json.Unmarshal(f.UsageRaw, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["input_tokens"]) != "10682" || string(m["output_tokens"]) != "510" ||
			string(m["server_tool_use"]) != `{"web_search_requests":1}` || string(m["service_tier"]) != `"standard"` {
			t.Errorf("usage raw = %s", f.UsageRaw)
		}
		if !f.Complete || f.Partial || f.StopReason != "end_turn" {
			t.Errorf("facts = %+v", f)
		}
	})
	t.Run("tool use argument sizes", func(t *testing.T) {
		f, _ := scanChunks(true, readFixture(t, "sse_tool_use.sse"))
		var got []string
		for _, u := range f.ToolUses {
			got = append(got, strings.Join([]string{string(u.Source), u.ToolCallID, u.Name, itoa(u.ArgBytes)}, " "))
		}
		// The partial_json pieces decode to {"file_path": "/tmp/a "q".txt"}, 33 bytes
		// once the JSON escapes are undone; NoArgs keeps the 2 bytes of its empty input object.
		want := []string{"client toolu_01A Read 33", "client toolu_01B NoArgs 2", "server srvtoolu_01C web_search 14"}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("tool uses = %q, want %q", got, want)
		}
		if f.StopReason != "tool_use" {
			t.Errorf("stop reason = %q", f.StopReason)
		}
	})
	t.Run("error event mid-stream", func(t *testing.T) {
		f, content := scanChunks(true, readFixture(t, "sse_error_midstream.sse"))
		if f.ErrorType != "overloaded_error" || f.Complete || !content {
			t.Errorf("facts = %+v, content %v", f, content)
		}
		if strings.Contains(string(factsJSON(t, f)), "Overloaded") {
			t.Error("the provider's error message was kept")
		}
	})
	t.Run("truncated stream", func(t *testing.T) {
		f, _ := scanChunks(true, readFixture(t, "sse_truncated.sse"))
		if f.Complete || f.Partial {
			t.Errorf("facts = %+v", f)
		}
		if len(f.ToolUses) != 1 || f.ToolUses[0].Name != "Bash" || f.ToolUses[0].ArgBytes != len(`{"command": "ls`) {
			t.Errorf("open tool block = %+v", f.ToolUses)
		}
	})
	t.Run("ping only has no content", func(t *testing.T) {
		f, content := scanChunks(true, []byte("event: ping\ndata: {\"type\": \"ping\"}\n\n"))
		if content || f.Partial {
			t.Errorf("content %v, facts %+v", content, f)
		}
	})
	t.Run("usage raw merges later over earlier", func(t *testing.T) {
		f, _ := scanChunks(true, readFixture(t, "sse_normal.sse"))
		var m map[string]json.RawMessage
		if err := json.Unmarshal(f.UsageRaw, &m); err != nil {
			t.Fatal(err)
		}
		if string(m["output_tokens"]) != "12" || string(m["input_tokens"]) != "25" {
			t.Errorf("usage raw = %s", f.UsageRaw)
		}
	})
	t.Run("malformed event data", func(t *testing.T) {
		f, _ := scanChunks(true, []byte("event: message_delta\ndata: {not json\n\n"))
		if !f.Partial {
			t.Error("a malformed event did not mark the facts partial")
		}
	})
}

func itoa(n int) string {
	b, _ := json.Marshal(n)
	return string(b)
}

func TestScannerOversizeEvent(t *testing.T) {
	start := "event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":7}}}\n\n"
	huge := "event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"" +
		strings.Repeat("x", MaxEventBytes) + "\"}}\n\n"
	stop := "event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":3}}\n\n"
	f, _ := scanChunks(true, []byte(start), []byte(huge), []byte(stop))
	if !f.Partial {
		t.Error("an oversize event did not mark the facts partial")
	}
	// The scanner resynchronises: facts around the oversize event are kept.
	if f.StopReason != "end_turn" || f.Usage.InputTokens == nil || *f.Usage.InputTokens != 7 || f.Usage.OutputTokens == nil || *f.Usage.OutputTokens != 3 {
		t.Errorf("facts = %+v", f)
	}
}

func TestBodyScanner(t *testing.T) {
	t.Run("message", func(t *testing.T) {
		f, _ := scanChunks(false, readFixture(t, "body_message.json"))
		if f.StopReason != "tool_use" || !f.Complete || f.Partial || f.Usage.OutputTokens == nil || *f.Usage.OutputTokens != 20 {
			t.Errorf("facts = %+v", f)
		}
		if len(f.ToolUses) != 1 || f.ToolUses[0].ToolCallID != "toolu_01A" || f.ToolUses[0].ArgBytes != len(`{"file_path":"/tmp/a.txt"}`) {
			t.Errorf("tool uses = %+v", f.ToolUses)
		}
	})
	t.Run("error body", func(t *testing.T) {
		f, _ := scanChunks(false, readFixture(t, "body_error.json"))
		if f.ErrorType != "rate_limit_error" || f.Partial {
			t.Errorf("facts = %+v", f)
		}
	})
	t.Run("split across writes", func(t *testing.T) {
		in := readFixture(t, "body_message.json")
		want, _ := scanChunks(false, in)
		for i := 0; i <= len(in); i++ {
			if got, _ := scanChunks(false, in[:i:i], in[i:]); !reflect.DeepEqual(got, want) {
				t.Fatalf("cut at %d: facts differ", i)
			}
		}
	})
	t.Run("over the bound", func(t *testing.T) {
		s := NewScanner(false)
		chunk := make([]byte, 1<<20)
		for range MaxBodyBytes/len(chunk) + 1 {
			s.Write(chunk)
		}
		f := s.Facts()
		if !f.Partial || f.Complete || f.StopReason != "" {
			t.Errorf("facts = %+v", f)
		}
	})
	t.Run("not json", func(t *testing.T) {
		if f, _ := scanChunks(false, []byte("<html>bad gateway</html>")); !f.Partial {
			t.Error("a body that is not JSON did not mark the facts partial")
		}
	})
	t.Run("empty body", func(t *testing.T) {
		if f, _ := scanChunks(false); f.Partial || f.Complete {
			t.Errorf("facts = %+v", f)
		}
	})
}
