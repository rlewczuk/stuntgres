package reference_test

import (
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const freezeSQL = "SELECT fs_part.heap_force_freeze($1::regclass,$2::tid[]) AS repaired"

// freezeCase runs the same SQL witness against every configured backend.
func freezeCase(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend, *sql.Conn)) {
	t.Helper()
	fixture.SchemaReferenceCleanup(t, "fs_part")
	fixture.SchemaReferenceCleanup(t, "fs_moved")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			c := freezeConn(t, p, b)
			defer c.Close()
			if b.PG {
				clean := func() {
					// Cancellation can retire the original transport. End any
					// surviving owner, then clean through an independent borrower.
					c.ExecContext(p.Ctx, "ROLLBACK")
					c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
					cleaner := freezeConn(t, p, b)
					defer cleaner.Close()
					for _, q := range []string{"DROP EXTENSION IF EXISTS pg_surgery CASCADE", "DROP SCHEMA IF EXISTS fs_part CASCADE", "DROP SCHEMA IF EXISTS fs_moved CASCADE"} {
						if _, err := cleaner.ExecContext(p.Ctx, q); err != nil {
							t.Error(q, err)
						}
					}
				}
				clean()
				defer clean()
			}
			for _, q := range []string{
				"CREATE SCHEMA fs_part", "CREATE EXTENSION pg_surgery SCHEMA fs_part",
				"CREATE TABLE fs_part.heap(k integer,v text)",
				"ALTER TABLE fs_part.heap SET(autovacuum_enabled=false)",
			} {
				freezeRun(t, p, b, c, fixture.Exec(q, 0))
			}
			body(t, p, b, c)
		})
	}
}

func freezeConn(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func freezeRun(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
}

func freezeBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s fixture.Step) {
	t.Helper()
	freezeRun(t, p, b, c, s)
	freezeRun(t, p, b, c, fixture.Prepared(s))
}

func freezeVoid(q string, args ...any) fixture.Step {
	return fixture.Select(q, []string{"repaired"}, []string{"VOID"}, [][]any{{""}}, args...)
}

func freezeError(q, code, message string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.ControlsReferenceDiagnostic(code, message), args...)
	s.SelectRows = true
	return s
}

func freezeTIDs(tids ...string) string {
	quoted := make([]string, len(tids))
	for i, tid := range tids {
		quoted[i] = strconv.Quote(tid)
	}
	return "{" + strings.Join(quoted, ",") + "}"
}

type freezeHeader struct {
	k                  int64
	v, tid, xmin, xmax string
}

// Capture generated physical identities only within one backend. Expected
// payloads/cardinality and subsequent header transitions are supplied by callers.
func freezeCapture(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) []freezeHeader {
	t.Helper()
	rows, err := c.QueryContext(p.Ctx, q, args...)
	if err != nil {
		t.Fatal(q, err)
	}
	defer rows.Close()
	ct, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"INT4", "TEXT", "TID", "XID", "XID"}
	if len(ct) != len(want) {
		t.Fatalf("header metadata width %d", len(ct))
	}
	for i, typ := range want {
		if ct[i].DatabaseTypeName() != typ {
			t.Fatalf("column %d type %s, want %s", i, ct[i].DatabaseTypeName(), typ)
		}
	}
	var result []freezeHeader
	for rows.Next() {
		var h freezeHeader
		var tid, xmin, xmax any
		if err := rows.Scan(&h.k, &h.v, &tid, &xmin, &xmax); err != nil {
			t.Fatal(err)
		}
		for _, raw := range []any{xmin, xmax} {
			if value, ok := raw.([]byte); !ok || value == nil {
				t.Fatalf("XID transport %T(%v), want nonnil []byte", raw, raw)
			}
		}
		h.tid = fmt.Sprint(fixture.Normalize(tid))
		h.xmin = fmt.Sprint(fixture.Normalize(xmin))
		h.xmax = fmt.Sprint(fixture.Normalize(xmax))
		var block uint32
		var offset uint16
		if n, err := fmt.Sscanf(h.tid, "(%d,%d)", &block, &offset); err != nil || n != 2 || offset == 0 {
			t.Fatalf("invalid real TID %q", h.tid)
		}
		if x, err := strconv.ParseUint(h.xmin, 10, 32); err != nil || x < 2 {
			t.Fatalf("invalid xmin %q", h.xmin)
		}
		if _, err := strconv.ParseUint(h.xmax, 10, 32); err != nil {
			t.Fatalf("invalid xmax %q", h.xmax)
		}
		result = append(result, h)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return result
}

func freezeOne(t *testing.T, p *fixture.Pair, c *sql.Conn, q string, args ...any) freezeHeader {
	t.Helper()
	h := freezeCapture(t, p, c, q, args...)
	if len(h) != 1 {
		t.Fatalf("%s returned %d versions, want one", q, len(h))
	}
	return h[0]
}

func freezeExpect(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, expected ...freezeHeader) {
	t.Helper()
	var values [][]any
	for _, h := range expected {
		values = append(values, []any{h.k, h.v, h.tid, h.xmin, h.xmax})
	}
	freezeBoth(t, p, b, c, fixture.Select("SELECT k,v,ctid,xmin,xmax FROM fs_part.heap ORDER BY k,v", []string{"k", "v", "ctid", "xmin", "xmax"}, []string{"INT4", "TEXT", "TID", "XID", "XID"}, values))
}

func freezeRepaired(h freezeHeader) freezeHeader { h.xmin = "2"; h.xmax = "0"; return h }

func freezeApply(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, tids ...string) {
	t.Helper()
	freezeRun(t, p, b, c, freezeVoid(freezeSQL, "fs_part.heap", freezeTIDs(tids...)))
}

// The snapshot is established before any tuple can become a pruning candidate.
func freezeHorizon(t *testing.T, p *fixture.Pair, b fixture.Backend) *sql.Conn {
	t.Helper()
	c := freezeConn(t, p, b)
	ready := false
	defer func() {
		if !ready {
			c.ExecContext(p.Ctx, "ROLLBACK")
			c.Close()
		}
	}()
	freezeRun(t, p, b, c, fixture.Exec("BEGIN ISOLATION LEVEL REPEATABLE READ", 0))
	freezeRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM fs_part.heap", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	ready = true
	return c
}
