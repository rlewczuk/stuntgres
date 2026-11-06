package dual

import (
	"bytes"
	"database/sql/driver"
	"encoding/json"
	"slices"
	"strings"
	"testing"
	"time"
)

// recordKeys returns the top-level keys of one JSON object in order.
func recordKeys(t *testing.T, line []byte) []string {
	t.Helper()
	dec := json.NewDecoder(bytes.NewReader(line))
	if tok, err := dec.Token(); err != nil || tok != json.Delim('{') {
		t.Fatalf("record start %v: %v", tok, err)
	}
	var keys []string
	for dec.More() {
		tok, err := dec.Token()
		if err != nil {
			t.Fatal(err)
		}
		keys = append(keys, tok.(string))
		var skip json.RawMessage
		if err := dec.Decode(&skip); err != nil {
			t.Fatal(err)
		}
	}
	return keys
}

func TestDualRecordFields(t *testing.T) {
	l := &logWriter{w: &bytes.Buffer{}}
	full := record{
		Tstamp: "t", ConnectionID: 1, Cycle: 2, Call: CallBegin, Statement: 2, Query: "q",
		Params: renderParams(nil), TxOptions: &txOptions{Isolation: "Repeatable Read", ReadOnly: true},
		Response: empty{}, Error: "42P01", Errmsg: "m", ResponseRef: empty{}, ErrorRef: "08006", ErrmsgRef: "r",
		Unmirrored: "u", Uncompared: "option", OrderOnly: true, ResponseDiff: []string{"d"}, DurationUS: 1, DurationRefUS: 2,
	}
	want := []string{"tstamp", "connection_id", "cycle", "call", "statement", "query", "params", "tx_options",
		"response", "error", "errmsg", "response_ref", "error_ref", "errmsg_ref", "unmirrored", "uncompared",
		"order_only", "response_diff", "duration_us", "duration_ref_us"}
	line := l.encode(&full)
	if got := recordKeys(t, line); !slices.Equal(got, want) {
		t.Fatalf("keys %q, want %q", got, want)
	}
	if !bytes.HasSuffix(line, []byte("}\n")) || bytes.Count(line, []byte("\n")) != 1 {
		t.Fatalf("one line per record: %q", line)
	}
	minimal := record{Params: renderParams(nil), Response: empty{}, ResponseRef: empty{}}
	line = l.encode(&minimal)
	required := []string{"tstamp", "connection_id", "cycle", "call", "query", "params", "response", "error",
		"response_ref", "error_ref", "duration_us", "duration_ref_us"}
	if got := recordKeys(t, line); !slices.Equal(got, required) {
		t.Fatalf("omitempty keys %q, want %q", got, required)
	}
	if !strings.Contains(string(line), `"params":[]`) {
		t.Fatalf("empty params: %s", line)
	}
}

func TestDualRecordParams(t *testing.T) {
	at := time.Date(2024, 1, 2, 3, 4, 5, 6, time.UTC)
	got, err := json.Marshal(renderParams([]driver.NamedValue{
		{Ordinal: 1, Value: int64(5)},
		{Ordinal: 2, Value: []byte{0xff}},
		{Ordinal: 3, Value: nil},
		{Ordinal: 4, Name: "n", Value: "s"},
		{Ordinal: 5, Value: at},
		{Ordinal: 6, Value: 1.5},
		{Ordinal: 7, Value: true},
		{Ordinal: 8, Value: []byte(nil)},
	}))
	if err != nil {
		t.Fatal(err)
	}
	want := `[{"index":1,"type":"int64","format":"text","null":false,"encoding":"text","value":"5"},` +
		`{"index":2,"type":"[]byte","format":"text","null":false,"encoding":"base64","value":"/w=="},` +
		`{"index":3,"format":"text","null":true,"encoding":"text","value":null},` +
		`{"index":4,"name":"n","type":"string","format":"text","null":false,"encoding":"text","value":"s"},` +
		`{"index":5,"type":"time.Time","format":"text","null":false,"encoding":"text","value":"2024-01-02T03:04:05.000000006Z"},` +
		`{"index":6,"type":"float64","format":"text","null":false,"encoding":"text","value":"1.5"},` +
		`{"index":7,"type":"bool","format":"text","null":false,"encoding":"text","value":"true"},` +
		`{"index":8,"format":"text","null":true,"encoding":"text","value":null}]`
	if string(got) != want {
		t.Fatalf("params\n got %s\nwant %s", got, want)
	}
}

// writeRecorder records each Write call separately.
type writeRecorder struct{ writes []string }

func (w *writeRecorder) Write(p []byte) (int, error) {
	w.writes = append(w.writes, string(p))
	return len(p), nil
}

func TestDualRecordOrder(t *testing.T) {
	w := &writeRecorder{}
	e := emitter{log: &logWriter{w: w}, next: 1}
	e.emit(2, []byte("2\n"))
	e.emit(3, []byte("3\n"))
	if len(w.writes) != 0 {
		t.Fatalf("written before cycle 1: %q", w.writes)
	}
	e.emit(1, []byte("1\n"))
	e.emit(4, []byte("4\n"))
	if want := []string{"1\n", "2\n", "3\n", "4\n"}; !slices.Equal(w.writes, want) {
		t.Fatalf("writes %q, want %q", w.writes, want)
	}
	e.emit(6, []byte("6\n"))
	e.flush()
	e.emit(5, []byte("5\n"))
	if want := []string{"1\n", "2\n", "3\n", "4\n", "6\n", "5\n"}; !slices.Equal(w.writes, want) {
		t.Fatalf("after flush %q, want %q", w.writes, want)
	}
}
