package main

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode/utf16"
	"unicode/utf8"

	"github.com/tidwall/gjson"
)

// keepKeys are the JSON keys whose string values are structure, not content:
// types, roles, ids and names the proxy records as facts. Every other string
// value of a request or response is replaced.
var keepKeys = map[string]bool{
	"type": true, "role": true, "model": true, "name": true, "id": true,
	"tool_use_id": true, "media_type": true, "stop_reason": true, "stop_sequence": true,
	"service_tier": true, "request_id": true, "event": true, "ttl": true, "speed": true,
}

// requestHeaderKeep lists the request headers a fixture keeps, in canonical
// form. Credentials and everything not named here are dropped.
var requestHeaderKeep = []string{"User-Agent", "Anthropic-Version", "Anthropic-Beta", "Content-Type", "Traceparent"}

// idHeaders carry a real identifier, replaced by a stable fake: the same input
// always gives the same output, so exchanges of one session still match.
var idHeaders = map[string]string{
	"X-Claude-Code-Session-Id":      "session",
	"X-Claude-Code-Agent-Id":        "agent",
	"X-Claude-Code-Parent-Agent-Id": "agent",
	"X-Claude-Code-Prompt-Id":       "prompt",
}

// plainHeaders are Claude Code hint headers kept as they are: they hold
// categories and counters, not content.
var plainHeaders = []string{
	"X-Claude-Code-Agent-Type", "X-Claude-Code-Request-Class", "X-Claude-Code-Compaction",
	"X-Claude-Code-Context-Compacted", "X-Claude-Code-Prev-Tool-Durations",
}

// responseHeaderKeep lists the response headers a fixture replays.
var responseHeaderKeep = []string{"Request-Id", "Retry-After", "Anthropic-Ratelimit-Requests-Limit", "Anthropic-Ratelimit-Requests-Remaining", "Anthropic-Ratelimit-Tokens-Limit", "Anthropic-Ratelimit-Tokens-Remaining"}

// leakPatterns must match nothing in a scrubbed fixture.
var leakPatterns = []*regexp.Regexp{
	regexp.MustCompile(`sk-ant-[A-Za-z0-9_-]{8,}`),
	regexp.MustCompile(`(?i)bearer\s+[A-Za-z0-9._~+/=-]{8,}`),
	regexp.MustCompile(`\blp_[A-Za-z0-9]{20,}`),
	// x-llm-proxy-key stays so a key sent by an old client is still redacted.
	regexp.MustCompile(`(?i)"?(authorization|x-api-key|x-spyglass-key|x-llm-proxy-key|cookie)"?\s*[:=]\s*"?[A-Za-z0-9]`),
	regexp.MustCompile(`[A-Za-z0-9._%+-]+@[A-Za-z0-9.-]+\.[A-Za-z]{2,}`),
	regexp.MustCompile(`/(Users|home)/[A-Za-z0-9._-]+/`),
}

func runScrub(args []string, stdout, stderr io.Writer) int {
	fs := flag.NewFlagSet("scrub", flag.ContinueOnError)
	fs.SetOutput(stderr)
	in := fs.String("in", "", "raw exchange directory written by record (required)")
	out := fs.String("out", "", "fixture directory to write, under testdata/fixtures (required)")
	if err := fs.Parse(args); err != nil {
		return 2
	}
	if *in == "" || *out == "" || fs.NArg() > 0 {
		_, _ = fmt.Fprintln(stderr, "fixturecap scrub: -in and -out are required")
		return 2
	}
	if err := scrubExchange(*in, *out); err != nil {
		_, _ = fmt.Fprintf(stderr, "fixturecap scrub: %v\n", err)
		return 1
	}
	_, _ = fmt.Fprintf(stdout, `wrote %s
Next:
  1. Read every file in it: the scrub is mechanical, you are the review.
  2. make goldens          (writes record.golden.json for the new fixture)
  3. Read the golden, add a case to TestFixtureFacts if it shows a new rule.
  4. Commit the fixture. Never commit the raw capture.
`, *out)
	return 0
}

// scrubExchange reads the raw exchange in and writes the fixture out. It
// writes nothing when the result still looks like it leaks.
func scrubExchange(in, out string) error {
	read := func(name string) ([]byte, error) {
		b, err := os.ReadFile(filepath.Join(in, name))
		if err != nil {
			return nil, err
		}
		return b, nil
	}
	metaRaw, err := read("meta.json")
	if err != nil {
		return err
	}
	var meta rawMeta
	if err := json.Unmarshal(metaRaw, &meta); err != nil {
		return fmt.Errorf("meta.json: %w", err)
	}
	reqRaw, err := read("request.json")
	if err != nil {
		return err
	}
	respName := "response.json"
	if meta.Streamed {
		respName = "response.sse"
	}
	respRaw, err := read(respName)
	if err != nil {
		return err
	}

	reqBody, err := scrubJSON(reqRaw)
	if err != nil {
		return fmt.Errorf("request.json: %w", err)
	}
	var respBody []byte
	if meta.Streamed {
		respBody, err = scrubSSE(respRaw)
	} else {
		respBody, err = scrubJSON(respRaw)
	}
	if err != nil {
		return fmt.Errorf("%s: %w", respName, err)
	}

	files := map[string][]byte{
		"meta.json":    mustJSON(meta),
		"request.json": reqBody,
		respName:       respBody,
	}
	if raw, err := read("request.headers.json"); err == nil {
		h, err := scrubRequestHeaders(raw)
		if err != nil {
			return fmt.Errorf("request.headers.json: %w", err)
		}
		files["request.headers.json"] = h
	}
	if raw, err := read("response.headers.json"); err == nil {
		h, err := replayHeaders(raw)
		if err != nil {
			return fmt.Errorf("response.headers.json: %w", err)
		}
		if h != nil {
			files["replay.json"] = h
		}
	}

	for name, data := range files {
		if err := checkLeaks(name, data); err != nil {
			return err
		}
	}
	if err := os.MkdirAll(out, 0o755); err != nil {
		return err
	}
	for name, data := range files {
		if err := os.WriteFile(filepath.Join(out, name), data, 0o644); err != nil {
			return err
		}
	}
	return nil
}

// checkLeaks fails when a scrubbed file still matches a leak pattern.
func checkLeaks(name string, data []byte) error {
	for _, re := range leakPatterns {
		if m := re.Find(data); m != nil {
			return fmt.Errorf("%s still matches %s (%q): nothing written; scrub the raw capture by hand or drop that exchange", name, re, m)
		}
	}
	return nil
}

// scrubJSON replaces the content strings of a JSON document in place. The
// layout and the encoded length of every string stay as they were, so sizes
// the proxy measures on the raw JSON survive.
func scrubJSON(raw []byte) ([]byte, error) {
	if !json.Valid(raw) {
		return nil, errors.New("not valid JSON")
	}
	return scrubRaw(raw, nil), nil
}

// scrubSSE scrubs the data line of every event and keeps the rest of the
// stream (event names, ping events, blank lines, comments) as it is. The
// partial_json fragments of a tool_use block are joined, scrubbed as one JSON
// text and cut again at the original fragment lengths, so the joined arguments
// stay valid JSON of the original size.
func scrubSSE(raw []byte) ([]byte, error) {
	lines := strings.SplitAfter(string(raw), "\n")
	frags := map[int64][]string{}
	for _, line := range lines {
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			continue
		}
		if !json.Valid([]byte(payload)) {
			return nil, errors.New("data line is not JSON")
		}
		if d := gjson.Get(payload, "delta"); d.Get("type").String() == "input_json_delta" {
			i := gjson.Get(payload, "index").Int()
			frags[i] = append(frags[i], d.Get("partial_json").String())
		}
	}
	pieces := map[int64][]string{}
	for i, fs := range frags {
		joined := string(scrubRaw([]byte(strings.Join(fs, "")), nil))
		for _, f := range fs {
			pieces[i] = append(pieces[i], joined[:len(f)])
			joined = joined[len(f):]
		}
	}

	var out bytes.Buffer
	for _, line := range lines {
		payload, ok := strings.CutPrefix(line, "data:")
		if !ok {
			out.WriteString(line)
			continue
		}
		var over map[string][]byte
		if gjson.Get(payload, "delta.type").String() == "input_json_delta" {
			i := gjson.Get(payload, "index").Int()
			b, err := marshal(pieces[i][0], "")
			if err != nil {
				return nil, err
			}
			over = map[string][]byte{"partial_json": bytes.TrimSpace(b)}
			pieces[i] = pieces[i][1:]
		}
		out.WriteString("data:")
		out.Write(scrubRaw([]byte(payload), over))
	}
	return out.Bytes(), nil
}

func marshal(v any, indent string) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	enc.SetIndent("", indent)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// scrubFrame is one open object or array while scrubRaw walks JSON text. key
// is the key the next value belongs to: the last key read in an object, the
// key the array sits under in an array.
type scrubFrame struct {
	object  bool
	wantKey bool
	key     string
}

// scrubRaw walks JSON text and copies it byte for byte, except string values.
// A string under a key in keepKeys stays; a string under a key in over becomes
// that literal; every other string becomes placeholder text. The text may end
// early, as joined partial_json of a cut stream does.
func scrubRaw(raw []byte, over map[string][]byte) []byte {
	out := make([]byte, 0, len(raw))
	var stack []scrubFrame
	top := func() *scrubFrame {
		if len(stack) == 0 {
			return nil
		}
		return &stack[len(stack)-1]
	}
	key := func() string {
		if f := top(); f != nil {
			return f.key
		}
		return ""
	}
	for i := 0; i < len(raw); {
		c := raw[i]
		switch c {
		case '{':
			stack = append(stack, scrubFrame{object: true, wantKey: true})
		case '[':
			stack = append(stack, scrubFrame{key: key()})
		case '}', ']':
			if len(stack) > 0 {
				stack = stack[:len(stack)-1]
			}
		case ',', ':':
			if f := top(); f != nil && f.object {
				f.wantKey = c == ','
			}
		case '"':
			end, closed := stringEnd(raw, i)
			lit := raw[i:end]
			switch f := top(); {
			case f != nil && f.object && f.wantKey:
				var k string
				_ = json.Unmarshal(lit, &k)
				f.key = k
				out = append(out, lit...)
			case over[key()] != nil:
				out = append(out, over[key()]...)
			case keepKeys[key()]:
				out = append(out, lit...)
			default:
				inner := lit[1:]
				if closed {
					inner = inner[:len(inner)-1]
				}
				out = append(out, '"')
				out = append(out, placeholder(inner)...)
				if closed {
					out = append(out, '"')
				}
			}
			i = end
			continue
		}
		out = append(out, c)
		i++
	}
	return out
}

// stringEnd returns the index after the string literal that starts at raw[i],
// and whether its closing quote was there.
func stringEnd(raw []byte, i int) (int, bool) {
	for j := i + 1; j < len(raw); j++ {
		switch raw[j] {
		case '\\':
			j++
		case '"':
			return j + 1, true
		}
	}
	return len(raw), false
}

const placeholderPrefix = "FAKEPROMPT-"

// unicodeStandIns hold an escape for a character of each UTF-8 length.
var unicodeStandIns = [...]string{1: `\u0078`, 2: `\u00e9`, 3: `\u4e00`, 4: `\ud83d\ude00`}

// placeholder returns text for the inside of a JSON string literal that
// carries no content and keeps both sizes of s: its encoded bytes and the
// bytes it decodes to. Plain bytes become x, and every escape becomes a
// content-free escape of the same encoded and decoded length. A leading run of
// x long enough takes the marker prefix the other fixtures use.
func placeholder(s []byte) []byte {
	out := make([]byte, 0, len(s))
	for i := 0; i < len(s); {
		switch {
		case s[i] != '\\' || i+1 >= len(s):
			out = append(out, 'x')
			i++
		case s[i+1] != 'u':
			out = append(out, `\n`...)
			i += 2
		default:
			n, size := unicodeEscape(s[i:])
			if n == 0 {
				out = append(out, 'x')
				i++
				continue
			}
			out = append(out, unicodeStandIns[size]...)
			i += n
		}
	}
	run := len(out) - len(bytes.TrimLeft(out, "x"))
	if run >= len(placeholderPrefix) {
		copy(out, placeholderPrefix)
	}
	return out
}

// unicodeEscape reads the \u escape at the start of s, with its low surrogate
// when it is a pair. It returns the encoded length and the UTF-8 length of
// what it decodes to, or zero when the escape is cut short.
func unicodeEscape(s []byte) (n, size int) {
	hex4 := func(b []byte) (rune, bool) {
		if len(b) < 6 {
			return 0, false
		}
		v, err := strconv.ParseUint(string(b[2:6]), 16, 16)
		return rune(v), err == nil
	}
	r, ok := hex4(s)
	if !ok {
		return 0, 0
	}
	if utf16.IsSurrogate(r) {
		if r < 0xdc00 && len(s) >= 12 && s[6] == '\\' && s[7] == 'u' {
			if lo, ok := hex4(s[6:]); ok && utf16.DecodeRune(r, lo) != utf8.RuneError {
				return 12, 4
			}
		}
		return 6, 3
	}
	return 6, utf8.RuneLen(r)
}

// fakeID maps a real identifier to a stable fake of the same kind.
func fakeID(kind, v string) string {
	sum := sha256.Sum256([]byte(v))
	return kind + "-" + hex.EncodeToString(sum[:])[:8]
}

// fakeTraceparent keeps the W3C shape with ids derived from the real ones.
func fakeTraceparent(v string) string {
	sum := sha256.Sum256([]byte(v))
	h := hex.EncodeToString(sum[:])
	return "00-" + h[:32] + "-" + h[32:48] + "-01"
}

// scrubRequestHeaders keeps the headers a fixture needs, with identifiers
// replaced.
func scrubRequestHeaders(raw []byte) ([]byte, error) {
	var h http.Header
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, name := range requestHeaderKeep {
		if v := h.Get(name); v != "" {
			if name == "Traceparent" {
				v = fakeTraceparent(v)
			}
			out[name] = v
		}
	}
	for name, kind := range idHeaders {
		if v := h.Get(name); v != "" {
			out[name] = fakeID(kind, v)
		}
	}
	for _, name := range plainHeaders {
		if v := h.Get(name); v != "" {
			out[name] = v
		}
	}
	return marshalSorted(out)
}

// replayHeaders turns the raw response headers into a replay.json, or nil when
// none is worth keeping.
func replayHeaders(raw []byte) ([]byte, error) {
	var h http.Header
	if err := json.Unmarshal(raw, &h); err != nil {
		return nil, err
	}
	keep := map[string]string{}
	for _, name := range responseHeaderKeep {
		if v := h.Get(name); v != "" {
			keep[name] = v
		}
	}
	if len(keep) == 0 {
		return nil, nil
	}
	return marshal(map[string]any{"headers": keep}, " ")
}

func marshalSorted(m map[string]string) ([]byte, error) {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var buf bytes.Buffer
	buf.WriteString("{\n")
	for i, k := range keys {
		kb, _ := json.Marshal(k)
		vb, _ := json.Marshal(m[k])
		buf.WriteString(" " + string(kb) + ": " + string(vb))
		if i < len(keys)-1 {
			buf.WriteString(",")
		}
		buf.WriteString("\n")
	}
	buf.WriteString("}\n")
	return buf.Bytes(), nil
}
