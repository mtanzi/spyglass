package proxy

import (
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/mtanzi/spyglass/internal/core"
	"github.com/mtanzi/spyglass/internal/family/anthropic"
)

// Extractor turns the facts a tap collected into the fields of the call
// record. The tap calls it once, when the response body is closed. The
// extractor of ADR 0014 implements it; request is the one read of the request
// body and the extractor must not keep it.
type Extractor interface {
	Extract(rec *core.CallRecord, request []byte, facts anthropic.Facts)
}

// tap is the ModifyResponse wrapper of ADR 0013. Read hands each chunk to the
// scanner and passes it on unchanged. Close is the exchange-finished hook. No
// byte is kept beyond what the scanner's bounds allow, and none is written out.
// ReverseProxy drives it from the handler goroutine, so it needs no lock
// except for Close, which can run more than once.
type tap struct {
	body    io.ReadCloser
	scanner anthropic.Scanner
	flight  *flight // owns the record, the request bytes, the watchdog and the failure kind

	unscanned bool // the body is encoded, so the scanner cannot read it
	ended     bool // the body ended, by EOF or by the watchdog
	gotTTFT   bool

	closeOnce sync.Once
}

// newTap wraps resp.Body. A compressed body passes through unscanned and the
// record is marked partial, because the scanner reads plain bytes only.
func newTap(f *flight, resp *http.Response) *tap {
	enc := resp.Header.Get("Content-Encoding")
	return &tap{
		body:      resp.Body,
		scanner:   anthropic.NewScanner(f.rec.Streamed),
		flight:    f,
		unscanned: enc != "" && !strings.EqualFold(enc, "identity"),
	}
}

// Read reads from the upstream, scans what arrived and returns it untouched.
// The watchdog runs only while Read waits on the upstream. When it fires, the
// cancelled request makes the pending read fail; Read turns that into a clean
// end of the body, because the status line has already gone to the client.
func (t *tap) Read(p []byte) (int, error) {
	if t.ended {
		return 0, io.EOF
	}
	t.flight.wd.Arm()
	n, err := t.body.Read(p)
	t.flight.wd.Disarm()
	if n > 0 {
		t.scan(p[:n])
	}
	switch {
	case err != nil && err != io.EOF && t.flight.wd.Fired():
		t.flight.failure, t.ended = core.FailureIdleTimeout, true
		return n, io.EOF
	case err != nil:
		t.ended = true
	}
	return n, err
}

func (t *tap) scan(chunk []byte) {
	if t.unscanned {
		return
	}
	t.scanner.Write(chunk)
	if !t.gotTTFT && t.scanner.ContentStarted() {
		t.gotTTFT = true
		rec := t.flight.rec
		rec.TTFT = time.Since(rec.TS)
	}
}

// Close closes the upstream body and, the first time, finishes the exchange:
// it logs the timeout, if any, and hands the facts to the extractor.
// ReverseProxy closes the body after a complete copy, a failed copy and a
// client abort, so this runs once per exchange on every path.
func (t *tap) Close() error {
	err := t.body.Close()
	t.closeOnce.Do(t.finish)
	return err
}

func (t *tap) finish() {
	t.flight.wd.Disarm()
	facts := t.scanner.Facts()
	if t.unscanned {
		facts.Partial = true
	}
	rec := t.flight.rec
	rec.ExtractionPartial = facts.Partial
	if t.flight.failure == core.FailureIdleTimeout {
		t.flight.h.log.Warn("upstream stream went idle", "call_id", rec.ID.String(),
			"provider", rec.Provider, "endpoint", rec.Endpoint)
	}
	if ex := t.flight.h.extractor; ex != nil {
		ex.Extract(rec, t.flight.body, facts)
		t.flight.extracted = true
	}
}
