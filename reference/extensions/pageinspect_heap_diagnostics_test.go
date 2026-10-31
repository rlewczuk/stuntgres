package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func heapAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE ROLE heap_reader", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA heap_part TO heap_reader", 0))
	defer func() {
		if _, err := c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
			t.Error(err)
		}
	}()
	for _, q := range []string{heapSource, heapFlagsSource, heapFlagsScalar} {
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		run(fixture.Exec("SET SESSION AUTHORIZATION heap_reader", 0))
		args := [][]any{{heapImage()}, {[]byte{}}, {make([]byte, 23)}}
		if q != heapSource {
			args = [][]any{{int64(0), int64(0)}, {int64(777), int64(32777)}}
		}
		for _, a := range args {
			s := heapError(q, "42501", "must be superuser to use raw page functions", a...)
			heapBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		null := heapStep(q, nil)
		if q == heapFlagsSource {
			null = heapFlagsStep(q, nil, int64(0), nil, nil)
		}
		if q == heapFlagsScalar {
			null = fixture.Select(q, []string{"heap_tuple_infomask_flags"}, []string{"RECORD"}, [][]any{{nil}}, nil, int64(0))
		}
		heapBoth(t, p, b, c, null)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, null)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
	run(fixture.Exec("CREATE TABLE heap_part.heap_acl_inputs(raw bytea,a integer,z integer)", 0))
	run(fixture.Exec("INSERT INTO heap_part.heap_acl_inputs VALUES(NULL,NULL,NULL)", 1))
	run(fixture.Exec("GRANT SELECT ON heap_part.heap_acl_inputs TO heap_reader", 0))
	for _, tc := range []struct{ signature, literal, stored, name string }{
		{"heap_page_items(bytea)", "SELECT * FROM heap_part.heap_page_items(NULL::bytea)", "SELECT * FROM heap_part.heap_page_items((SELECT raw FROM heap_part.heap_acl_inputs))", "heap_page_items"},
		{"heap_tuple_infomask_flags(integer,integer)", "SELECT heap_part.heap_tuple_infomask_flags(NULL::integer,0)", "SELECT heap_part.heap_tuple_infomask_flags(a,z) FROM heap_part.heap_acl_inputs", "heap_tuple_infomask_flags"},
	} {
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION heap_part."+tc.signature+" FROM PUBLIC", 0))
		run(fixture.Exec("SET SESSION AUTHORIZATION heap_reader", 0))
		heapBoth(t, p, b, c, heapError(tc.literal, "42501", "permission denied for function "+tc.name))
		heapHeld(t, p, b, c, tc.stored, []fixture.Step{heapError(tc.stored, "42501", "permission denied for function "+tc.name)})
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION heap_part."+tc.signature+" TO PUBLIC", 0))
	}
	run(fixture.Exec("REVOKE USAGE ON SCHEMA heap_part FROM heap_reader", 0))
	run(fixture.Exec("SET SESSION AUTHORIZATION heap_reader", 0))
	for _, tc := range []struct {
		q        string
		position int
	}{{"SELECT * FROM heap_part.heap_page_items(NULL::bytea)", 15}, {"SELECT heap_part.heap_tuple_infomask_flags(NULL::integer,0)", 8}} {
		s := fixture.ImplicitError(tc.q, fixture.Diagnostic{Code: "42501", Message: "permission denied for schema heap_part", Position: fixture.Position(tc.position)})
		s.SelectRows = true
		heapBoth(t, p, b, c, s)
	}
	run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
}

func TestReferencePageinspectHeapDiagnostics(t *testing.T) {
	t.Run("lookup-and-atomicity", func(t *testing.T) {
		heapCase(t, true, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				q, message string
				position   int
			}{
				{"SELECT * FROM heap_part.heap_page_items()", "function heap_part.heap_page_items() does not exist", 15},
				{"SELECT * FROM heap_part.heap_page_items(true)", "function heap_part.heap_page_items(boolean) does not exist", 15},
				{"SELECT heap_part.heap_tuple_infomask_flags(1::bigint,0)", "function heap_part.heap_tuple_infomask_flags(bigint, integer) does not exist", 8},
				{"SELECT heap_part.heap_tuple_infomask_flags(0,true)", "function heap_part.heap_tuple_infomask_flags(integer, boolean) does not exist", 8},
				{"SELECT heap_part.heap_tuple_infomask_flags(0)", "function heap_part.heap_tuple_infomask_flags(integer) does not exist", 8},
				{"SELECT heap_part.heap_tuple_infomask_flags(t_infomask=>0,wrong=>0)", "function heap_part.heap_tuple_infomask_flags(t_infomask => integer, wrong => integer) does not exist", 8},
				{"SELECT * FROM heap_part.heap_page_items(wrong=>NULL::bytea)", "function heap_part.heap_page_items(wrong => bytea) does not exist", 15},
			} {
				heapBoth(t, p, b, c, heapLookup(tc.q, tc.message, tc.position))
			}
			for _, prefix := range []string{"SELECT ", "SELECT /*é*/ "} {
				q := prefix + "heap_part.heap_tuple_infomask_flags(t_infomask=>0,t_infomask=>0)"
				// Second label is the syntax error location; positions count Unicode
				// characters, not UTF-8 bytes.
				at := strings.LastIndex(q, "t_infomask=>")
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "argument name \"t_infomask\" used more than once", Position: fixture.Position(utf8.RuneCountInString(q[:at]) + 1)})
				s.SelectRows = true
				heapBoth(t, p, b, c, s)
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE heap_sentinel(value text[])", 0))
			run(fixture.Exec("INSERT INTO heap_sentinel VALUES('{sentinel}')", 1))
			q := "INSERT INTO heap_sentinel VALUES((heap_part.heap_tuple_infomask_flags($1,0)).raw_flags),((heap_part.heap_tuple_infomask_flags(10/$2::integer,0)).raw_flags)"
			s := heapError(q, "22012", "division by zero", int64(1), int64(0))
			s.SelectRows = false
			heapBoth(t, p, b, c, s)
			unchanged := fixture.Select("SELECT value FROM heap_sentinel", []string{"value"}, []string{"_TEXT"}, [][]any{{"{sentinel}"}})
			unchanged.Bag = true
			heapBoth(t, p, b, c, unchanged)
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				_, err := c.ExecContext(ctx, q, int64(1), int64(1))
				if !errors.Is(err, context.Canceled) {
					t.Fatal("pre-canceled mutation", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("context error invented SQLSTATE", err)
				}
				heapBoth(t, p, b, c, unchanged)
			}
		})
	})
	t.Run("live-authority", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"heap_reader"})
		heapCase(t, true, heapAuthority)
	})
}
