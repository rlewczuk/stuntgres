package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePageinspectHeapAttrsNulls(t *testing.T) {
	attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		heapHeld(t, p, b, c, attrsSplit6, []fixture.Step{
			attrsValue(attrsSplit6, nil, nil, 1, 99, "bad", true),
			attrsValue(attrsSplit6, attrsArray("01020304", "NULL"), attrsBytes("01020304"), nil, 1, nil, nil),
			attrsValue(attrsSplit6, attrsArray("NULL", "NULL"), []byte{}, nil, nil, nil, nil),
		})
		q := "SELECT heap_part.tuple_data_split($1::oid,$2::bytea,$3::integer,$4::integer,$5::text,$6::boolean) AS attrs"
		heapHeld(t, p, b, c, q, []fixture.Step{attrsValue(q, nil, int64(4294967295), nil, 1, 99, "bad", nil), heapError(q, "XX000", "could not open relation with OID 0", nil, []byte{}, 0, 0, nil, false)})
		for _, relation := range []string{"NULL::regclass", "4294967295::oid::regclass"} {
			q := "SELECT * FROM heap_part.heap_page_item_attrs($1," + relation + ",NULL::boolean) ORDER BY lp"
			heapBoth(t, p, b, c, attrsRows(q, nil, nil))
			heapBoth(t, p, b, c, attrsRows(q, nil, make([]byte, 24)))
		}
	})
}

func TestReferencePageinspectHeapAttrsDiagnostics(t *testing.T) {
	t.Run("diagnostics-and-atomicity", func(t *testing.T) {
		attrsCase(t, "a integer,b integer", func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, tc := range []struct {
				data        string
				mask, natts int
				bits        any
				message     string
			}{
				{"", 1, 2, nil, "t_bits string must not be NULL"},
				{"", 1, 2, "", "unexpected length of t_bits string: 0, expected 8"},
				{"", 1, 2, "x", "unexpected length of t_bits string: 1, expected 8"},
				{"", 1, 2, "é000000", "invalid character \"é\" in t_bits string"},
				{"", 1, 2, "x0000000", "invalid character \"x\" in t_bits string"},
				{"", 0, 2, "", "t_bits string is expected to be NULL, but instead it is 0 bytes long"},
				{"", 0, 2, "00000000", "t_bits string is expected to be NULL, but instead it is 8 bytes long"},
				{"", 0, 3, nil, "number of attributes in tuple header is greater than number of attributes in tuple descriptor"},
				{"010203", 0, 1, nil, "unexpected end of tuple data"},
				{"0102030400", 0, 1, nil, "end of tuple reached without looking at all its data"},
			} {
				s := heapError(attrsSplit5, "XX001", tc.message, attrsBytes(tc.data), tc.mask, tc.natts, tc.bits)
				heapHeld(t, p, b, c, attrsSplit5, []fixture.Step{s})
			}
			q := "SELECT heap_part.tuple_data_split(4294967295::oid,$1,$2,$3,$4)"
			heapBoth(t, p, b, c, heapError(q, "XX001", "t_bits string must not be NULL", []byte{}, 1, 99, nil))
			heapBoth(t, p, b, c, heapError(q, "XX000", "could not open relation with OID 4294967295", []byte{}, 0, 99, nil))
			for _, n := range []int{0, 23} {
				message := "input page too small (0 bytes)"
				if n == 23 {
					message = "input page too small (23 bytes)"
				}
				heapBoth(t, p, b, c, attrsInvocationError(attrsSource2, "22023", message, make([]byte, n)))
				heapBoth(t, p, b, c, attrsInvocationError(attrsSource3, "22023", message, make([]byte, n), false))
			}
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE attrs_sentinel(v bytea[])", 0))
			run(fixture.Exec("INSERT INTO attrs_sentinel VALUES(ARRAY['\\x99'::bytea])", 1))
			q = "INSERT INTO attrs_sentinel VALUES(heap_part.tuple_data_split('attrs_schema'::regclass::oid,$1,0,2,NULL)),(heap_part.tuple_data_split('attrs_schema'::regclass::oid,$2,0,1,NULL))"
			s := heapError(q, "XX001", "unexpected end of tuple data", attrsBytes("0102030405060708"), []byte{1})
			s.SelectRows = false
			heapBoth(t, p, b, c, s)
			unchanged := fixture.Select("SELECT v FROM attrs_sentinel", []string{"v"}, []string{"_BYTEA"}, [][]any{{attrsArray("99")}})
			unchanged.Bag = true
			heapBoth(t, p, b, c, unchanged)
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				_, err := c.ExecContext(ctx, q, attrsBytes("0102030405060708"), []byte{1})
				if !errors.Is(err, context.Canceled) {
					t.Fatal("pre-canceled insert", err)
				}
				var state interface{ SQLState() string }
				if errors.As(err, &state) {
					t.Fatal("context error acquired SQLSTATE", err)
				}
				heapBoth(t, p, b, c, unchanged)
			}
		})
	})
	t.Run("admitted-schema-cancellation", attrsCancellation)
}

func attrsAuthority(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE ROLE attrs_reader", 0))
	run(fixture.Exec("GRANT USAGE ON SCHEMA public,heap_part TO attrs_reader", 0))
	defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
	for _, q := range []string{attrsSplit5, attrsSplit6, attrsSource2, attrsSource3} {
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		run(fixture.Exec("SET SESSION AUTHORIZATION attrs_reader", 0))
		args := []any{nil, 1, 99, "bad"}
		if q == attrsSplit6 {
			args = append(args, false)
		}
		if q == attrsSource2 {
			args = []any{make([]byte, 24)}
		}
		if q == attrsSource3 {
			args = []any{make([]byte, 24), false}
		}
		s := attrsInvocationError(q, "42501", "must be superuser to use raw page functions", args...)
		heapBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		if q == attrsSource2 || q == attrsSource3 {
			args[0] = nil
			s := attrsRows(q, nil, args...)
			heapBoth(t, p, b, c, s)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		if err := st.Close(); err != nil {
			t.Fatal(err)
		}
	}
	for _, signature := range []string{"tuple_data_split(oid,bytea,integer,integer,text)", "tuple_data_split(oid,bytea,integer,integer,text,boolean)", "heap_page_item_attrs(bytea,regclass)", "heap_page_item_attrs(bytea,regclass,boolean)"} {
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION heap_part."+signature+" FROM PUBLIC", 0))
		q, name := attrsSplit5, "tuple_data_split"
		args := []any{nil, 0, 0, nil}
		if signature == "tuple_data_split(oid,bytea,integer,integer,text,boolean)" {
			q = attrsSplit6
			args = append(args, false)
		}
		if signature == "heap_page_item_attrs(bytea,regclass)" {
			q, name, args = attrsSource2, "heap_page_item_attrs", []any{nil}
		}
		if signature == "heap_page_item_attrs(bytea,regclass,boolean)" {
			q, name, args = attrsSource3, "heap_page_item_attrs", []any{nil, false}
		}
		run(fixture.Exec("SET SESSION AUTHORIZATION attrs_reader", 0))
		heapBoth(t, p, b, c, heapError(q, "42501", "permission denied for function "+name, args...))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION heap_part."+signature+" TO PUBLIC", 0))
	}
}

func TestReferencePageinspectHeapAttrsPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"attrs_reader"})
	attrsCase(t, "a integer,b integer", attrsAuthority)
}

// The released SQL-standard wrappers contribute one statement context each.
// Routine ACL failures occur outside these bodies and do not use this helper.
func attrsInvocationError(query, code, message string, args ...any) fixture.Step {
	s := heapError(query, code, message, args...)
	const context = "SQL function \"heap_page_item_attrs\" statement 1"
	switch query {
	case attrsSource2:
		s.Diagnostic.Context = context + "\n" + context
	case attrsSource3:
		s.Diagnostic.Context = context
	}
	return s
}
