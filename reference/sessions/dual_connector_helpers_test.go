package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"github.com/lib/pq"
	"slices"
	"strings"
	"stuntgres"
	"stuntgres/dual"
	"stuntgres/reference/fixture"
	"sync"
	"testing"
)

// dualPair is one dual pool over the scenario's native connector and the
// scenario's reference: a second native engine in quick mode, the pinned
// PostgreSQL through lib/pq with REFERENCE=1.
type dualPair struct {
	DB     *sql.DB // dual.NewConnector(native, reference)
	Ref    *sql.DB // the unwrapped reference: induces reference-only state
	Native *sql.DB // the scenario's native pool (same engine as DB's native side)
	Log    *dualLog
}

// dualReferences holds one reference connector per scenario, so every dual
// pool of a scenario, subtests included, mirrors onto the same reference state
// in both modes. An entry lives until the scenario's context ends, which is
// the scenario's own cleanup, not a subtest's.
var dualReferences sync.Map // *fixture.Pair -> driver.Connector

func dualReference(t *testing.T, p *fixture.Pair) driver.Connector {
	t.Helper()
	if c, ok := dualReferences.Load(p); ok {
		return c.(driver.Connector)
	}
	var c driver.Connector
	if *fixture.Mode {
		pc, err := pq.NewConnector(fixture.DSN)
		if err != nil {
			t.Fatal(err)
		}
		c = pc
	} else {
		nc, err := stuntgres.NewDriver().OpenConnector(p.NativeDSN + "/dual-reference")
		if err != nil {
			t.Fatal(err)
		}
		c = nc
	}
	dualReferences.Store(p, c)
	context.AfterFunc(p.Ctx, func() { dualReferences.Delete(p) })
	return c
}

// newDualPair opens a dual pool. wrap optionally wraps the reference
// connector (the fault connector). Cleanup closes the pools before the
// fixture resets the reference schema.
func newDualPair(t *testing.T, p *fixture.Pair, opts dual.Options, wrap func(driver.Connector) driver.Connector) *dualPair {
	t.Helper()
	base := dualReference(t, p)
	ref := base
	if wrap != nil {
		ref = wrap(base)
	}
	d := &dualPair{Native: p.Native, Log: &dualLog{}}
	opts.Log = d.Log
	d.DB = sql.OpenDB(dual.NewConnector(p.NativeConnector, ref, opts))
	d.Ref = sql.OpenDB(base)
	t.Cleanup(func() {
		if err := d.DB.Close(); err != nil {
			t.Errorf("dual pool close: %v", err)
		}
		if err := d.Ref.Close(); err != nil {
			t.Errorf("reference pool close: %v", err)
		}
	})
	return d
}

// dualLog is the goroutine-safe buffer the dual connector writes into.
type dualLog struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (l *dualLog) Write(b []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.buf.Write(b)
}

type dualParam struct {
	Index    int     `json:"index"`
	Name     string  `json:"name"`
	Type     string  `json:"type"`
	Format   string  `json:"format"`
	Null     bool    `json:"null"`
	Encoding string  `json:"encoding"`
	Value    *string `json:"value"`
}

type dualRecord struct {
	Tstamp        string          `json:"tstamp"`
	ConnectionID  uint64          `json:"connection_id"`
	Cycle         uint64          `json:"cycle"`
	Call          string          `json:"call"`
	Statement     uint64          `json:"statement"`
	Query         string          `json:"query"`
	Params        []dualParam     `json:"params"`
	TxOptions     json.RawMessage `json:"tx_options"`
	Response      json.RawMessage `json:"response"`
	Error         string          `json:"error"`
	Errmsg        string          `json:"errmsg"`
	ResponseRef   json.RawMessage `json:"response_ref"`
	ErrorRef      string          `json:"error_ref"`
	ErrmsgRef     string          `json:"errmsg_ref"`
	Unmirrored    string          `json:"unmirrored"`
	Uncompared    string          `json:"uncompared"`
	OrderOnly     bool            `json:"order_only"`
	ResponseDiff  []string        `json:"response_diff"`
	DurationUS    int64           `json:"duration_us"`
	DurationRefUS int64           `json:"duration_ref_us"`
}

var (
	dualRequiredKeys = []string{"tstamp", "connection_id", "cycle", "call", "query", "params", "response",
		"error", "response_ref", "error_ref", "duration_us", "duration_ref_us"}
	dualOptionalKeys = []string{"statement", "tx_options", "errmsg", "errmsg_ref", "unmirrored", "uncompared",
		"order_only", "response_diff"}
)

// records decodes the log strictly: one object per line, the documented key
// set with every required key, and no unknown field.
func (l *dualLog) records(t *testing.T) []dualRecord {
	t.Helper()
	l.mu.Lock()
	data := bytes.Clone(l.buf.Bytes())
	l.mu.Unlock()
	var out []dualRecord
	for _, line := range bytes.Split(bytes.TrimSuffix(data, []byte("\n")), []byte("\n")) {
		if len(line) == 0 {
			continue
		}
		var keys map[string]json.RawMessage
		if err := json.Unmarshal(line, &keys); err != nil {
			t.Fatalf("dual record %s: %v", line, err)
		}
		for _, k := range dualRequiredKeys {
			if _, ok := keys[k]; !ok {
				t.Fatalf("dual record lacks %q: %s", k, line)
			}
		}
		for k := range keys {
			if !slices.Contains(dualRequiredKeys, k) && !slices.Contains(dualOptionalKeys, k) {
				t.Fatalf("dual record has undocumented %q: %s", k, line)
			}
		}
		dec := json.NewDecoder(bytes.NewReader(line))
		dec.DisallowUnknownFields()
		var r dualRecord
		if err := dec.Decode(&r); err != nil {
			t.Fatalf("dual record %s: %v", line, err)
		}
		out = append(out, r)
	}
	return out
}

// sqlRecords drops the pool-lifecycle records.
func sqlRecords(rs []dualRecord) []dualRecord {
	return slices.DeleteFunc(slices.Clone(rs), func(r dualRecord) bool {
		return r.Call == "connect" || r.Call == "close" || r.Call == "is_valid" || r.Call == "reset_session"
	})
}

func dualCalls(rs []dualRecord) []string {
	calls := make([]string, len(rs))
	for i, r := range rs {
		calls[i] = r.Call
	}
	return calls
}

// lastQuery is the last record of a call for query.
func lastQuery(t *testing.T, rs []dualRecord, call, query string) dualRecord {
	t.Helper()
	for i := len(rs) - 1; i >= 0; i-- {
		if rs[i].Call == call && rs[i].Query == query {
			return rs[i]
		}
	}
	t.Fatalf("no %s record for %q", call, query)
	return dualRecord{}
}

func wantDiff(t *testing.T, r dualRecord, want ...string) {
	t.Helper()
	if !slices.Equal(r.ResponseDiff, want) {
		t.Errorf("%s %q response_diff:\n got %q\nwant %q", r.Call, r.Query, r.ResponseDiff, want)
	}
}

// wantJSON compares a raw record value with the compacted expectation.
func wantJSON(t *testing.T, label string, got json.RawMessage, want string) {
	t.Helper()
	var g, w bytes.Buffer
	if err := json.Compact(&g, got); err != nil {
		t.Fatalf("%s: %v", label, err)
	}
	if err := json.Compact(&w, []byte(want)); err != nil {
		t.Fatalf("%s expectation: %v", label, err)
	}
	if g.String() != w.String() {
		t.Errorf("%s:\n got %s\nwant %s", label, g.String(), w.String())
	}
}

// dualSets decodes a rows summary.
type dualSets struct {
	Sets []struct {
		Columns []string `json:"columns"`
		Types   []string `json:"types"`
		Rows    int      `json:"rows"`
		Partial bool     `json:"partial"`
	} `json:"sets"`
	Complete bool `json:"complete"`
}

func decodeSets(t *testing.T, raw json.RawMessage) dualSets {
	t.Helper()
	var s dualSets
	if err := json.Unmarshal(raw, &s); err != nil {
		t.Fatalf("rows summary %s: %v", raw, err)
	}
	return s
}

func dualExec(t *testing.T, ctx context.Context, q interface {
	ExecContext(context.Context, string, ...any) (sql.Result, error)
}, query string, args ...any) int64 {
	t.Helper()
	res, err := q.ExecContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	n, err := res.RowsAffected()
	if err != nil {
		t.Fatalf("%s rows affected: %v", query, err)
	}
	return n
}

// dualStrings reads every row of a one-column query as text.
func dualStrings(t *testing.T, ctx context.Context, q interface {
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}, query string, args ...any) []string {
	t.Helper()
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			t.Fatal(err)
		}
		out = append(out, v)
	}
	if err := rows.Err(); err != nil {
		t.Fatalf("%s: %v", query, err)
	}
	return out
}

func dualCount(t *testing.T, ctx context.Context, db *sql.DB, table string) int64 {
	t.Helper()
	var n int64
	if err := db.QueryRowContext(ctx, "SELECT count(*) FROM "+table).Scan(&n); err != nil {
		t.Fatalf("count %s: %v", table, err)
	}
	return n
}

// dualSQLState is the caller error's SQLSTATE and message.
func dualSQLState(err error) (string, string) {
	var s interface{ SQLState() string }
	if !errors.As(err, &s) {
		return "", ""
	}
	return s.SQLState(), strings.TrimPrefix(err.Error(), "stuntgres: ")
}
