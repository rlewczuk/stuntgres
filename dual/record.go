package dual

import (
	"bytes"
	"database/sql/driver"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"maps"
	"slices"
	"strconv"
	"sync"
	"time"
	"unicode/utf8"
)

// record is one JSONL line, fields in documented order. Names follow the
// cmd/stuntgres query.log where the meaning matches.
type record struct {
	Tstamp        string     `json:"tstamp"`
	ConnectionID  uint64     `json:"connection_id"`
	Cycle         uint64     `json:"cycle"`
	Call          Call       `json:"call"`
	Statement     uint64     `json:"statement,omitempty"`
	Query         string     `json:"query"`
	Params        []param    `json:"params"`
	TxOptions     *txOptions `json:"tx_options,omitempty"`
	Response      any        `json:"response"`
	Error         string     `json:"error"`
	Errmsg        string     `json:"errmsg,omitempty"`
	ResponseRef   any        `json:"response_ref"`
	ErrorRef      string     `json:"error_ref"`
	ErrmsgRef     string     `json:"errmsg_ref,omitempty"`
	Unmirrored    string     `json:"unmirrored,omitempty"`
	Uncompared    string     `json:"uncompared,omitempty"`
	OrderOnly     bool       `json:"order_only,omitempty"`
	ResponseDiff  []string   `json:"response_diff,omitempty"`
	DurationUS    int64      `json:"duration_us"`
	DurationRefUS int64      `json:"duration_ref_us"`
}

// param is one argument in query.log's params shape; oid is omitted (no wire).
type param struct {
	Index    int     `json:"index"`
	Name     string  `json:"name,omitempty"`
	Type     string  `json:"type,omitempty"`
	Format   string  `json:"format"`
	Null     bool    `json:"null"`
	Encoding string  `json:"encoding"`
	Value    *string `json:"value"`
}

func renderParams(args []driver.NamedValue) []param {
	out := make([]param, len(args))
	for i, a := range args {
		p := param{Index: a.Ordinal, Name: a.Name, Format: "text", Encoding: "text"}
		if p.Index == 0 {
			p.Index = i + 1
		}
		if isNull(a.Value) {
			p.Null = true
		} else {
			p.Type = goType(a.Value)
			var text string
			text, p.Encoding = paramText(a.Value)
			p.Value = &text
		}
		out[i] = p
	}
	return out
}

// isNull reports SQL NULL: nil, or a nil []byte (which drivers treat as NULL).
func isNull(v driver.Value) bool {
	b, ok := v.([]byte)
	return v == nil || (ok && b == nil)
}

func goType(v driver.Value) string {
	switch v.(type) {
	case []byte:
		return "[]byte"
	case time.Time:
		return "time.Time"
	}
	return fmt.Sprintf("%T", v)
}

// paramText is a value's canonical text and its encoding.
func paramText(v driver.Value) (string, string) {
	switch x := v.(type) {
	case int64:
		return strconv.FormatInt(x, 10), "text"
	case float64:
		return strconv.FormatFloat(x, 'g', -1, 64), "text"
	case bool:
		return strconv.FormatBool(x), "text"
	case string:
		return x, "text"
	case []byte:
		if utf8.Valid(x) {
			return string(x), "text"
		}
		return base64.StdEncoding.EncodeToString(x), "base64"
	case time.Time:
		return x.Format(time.RFC3339Nano), "text"
	}
	return fmt.Sprint(v), "text"
}

// logWriter serializes records. mu is a leaf lock held for exactly one Write
// of one complete record; no driver call, hook or encoding runs under it.
// Write errors are dropped.
type logWriter struct {
	mu sync.Mutex
	w  io.Writer
}

// encode renders r as one line, or nil when records are discarded.
func (l *logWriter) encode(r *record) []byte {
	if l.w == nil {
		return nil
	}
	var b bytes.Buffer
	enc := json.NewEncoder(&b)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(r); err != nil {
		return nil
	}
	return b.Bytes()
}

func (l *logWriter) write(line []byte) {
	if l.w == nil || line == nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	_, _ = l.w.Write(line)
}

// emitter writes one connection's records in cycle order: a completed record
// waits only while an earlier cycle (open rows) is unfinished. It is owned by
// the connection and serialized by database/sql.
type emitter struct {
	log     *logWriter
	next    uint64
	waiting map[uint64][]byte
	flushed bool
}

func (e *emitter) emit(cycle uint64, line []byte) {
	if e.flushed {
		e.log.write(line)
		return
	}
	if cycle != e.next {
		if e.waiting == nil {
			e.waiting = map[uint64][]byte{}
		}
		e.waiting[cycle] = line
		return
	}
	e.log.write(line)
	e.next++
	for {
		waiting, ok := e.waiting[e.next]
		if !ok {
			return
		}
		delete(e.waiting, e.next)
		e.log.write(waiting)
		e.next++
	}
}

// flush writes every waiting record in cycle order; later records are written
// at once. The connection calls it when it closes.
func (e *emitter) flush() {
	for _, cycle := range slices.Sorted(maps.Keys(e.waiting)) {
		e.log.write(e.waiting[cycle])
	}
	clear(e.waiting)
	e.flushed = true
}
