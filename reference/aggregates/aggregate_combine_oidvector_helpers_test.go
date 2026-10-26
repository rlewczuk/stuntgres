package reference_test

import (
	"bytes"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Return the same public check for native Dump/Restore replay. Raw rows are
// inspected before fixture Normalize, so TEXT cannot masquerade as OIDVECTOR.
func aggregateCombineOIDVector(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) func() {
	t.Helper()
	createAggregateRun(t, p, b, c,
		fixture.Exec("CREATE FUNCTION ac_vector_zero() RETURNS integer LANGUAGE SQL AS $$SELECT 1$$", 0),
		fixture.Exec("CREATE TABLE ac_vector_values(id integer,v oidvector)", 0),
		fixture.Exec("INSERT INTO ac_vector_values VALUES(1,'2281 2281'),(2,''),(3,NULL)", 3))
	return func() {
		t.Helper()
		raw := func(db fixture.Executor, query string, expected any, args ...any) {
			t.Helper()
			rows, err := db.QueryContext(p.Ctx, query, args...)
			if err != nil {
				t.Fatal(query, err)
			}
			defer rows.Close()
			columns, err := rows.ColumnTypes()
			if err != nil || len(columns) != 1 || columns[0].Name() != "v" || columns[0].DatabaseTypeName() != "OIDVECTOR" {
				t.Fatal("raw oidvector column metadata", query, columns, err)
			}
			if !rows.Next() {
				t.Fatal("missing raw vector row", query, rows.Err())
			}
			var value any
			if err := rows.Scan(&value); err != nil {
				t.Fatal(query, err)
			}
			if expected == nil {
				if value != nil {
					t.Fatal("raw vector NULL was not NULL", value)
				}
			} else {
				if b.PG {
					if value, ok := value.([]byte); !ok || value == nil {
						t.Fatalf("raw PostgreSQL vector: expected nonnil byte payload, got %T(%v)", value, value)
					}
				} else if _, ok := value.(string); !ok {
					t.Fatalf("raw native vector: expected string payload, got %T(%v)", value, value)
				}
				literal := expected.(string)
				switch value := value.(type) {
				case string:
					if value != literal {
						t.Fatal("raw vector text", query, value, literal)
					}
				case []byte:
					if !bytes.Equal(value, []byte(literal)) {
						t.Fatal("raw vector bytes", query, value, []byte(literal))
					}
				default:
					t.Fatalf("raw vector %s: unexpected datum %T(%v)", query, value, value)
				}
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatal("extra/failed raw vector row", query, rows.Err())
			}
		}
		for _, tc := range []struct {
			query    string
			expected any
		}{
			{"SELECT proargtypes AS v FROM pg_proc WHERE oid=6299", "2281 2281"},
			{"SELECT proargtypes AS v FROM pg_proc WHERE oid=6300", "2281"},
			{"SELECT proargtypes AS v FROM pg_proc WHERE oid=6301", "17 2281"},
			{"SELECT proargtypes AS v FROM pg_proc WHERE proname='ac_vector_zero'", ""},
			{"SELECT v FROM ac_vector_values WHERE id=1", "2281 2281"},
			{"SELECT v FROM ac_vector_values WHERE id=2", ""},
			{"SELECT v FROM ac_vector_values WHERE id=3", nil},
		} {
			raw(c, tc.query, tc.expected)
			statement, err := c.PrepareContext(p.Ctx, tc.query)
			if err != nil {
				t.Fatal(err)
			}
			raw(fixture.HeldStatement{Executor: c, Statement: statement, SQL: tc.query}, tc.query, tc.expected)
			statement.Close()
		}
		const query = "SELECT $1::oidvector AS v"
		statement, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer statement.Close()
		held := fixture.HeldStatement{Executor: c, Statement: statement, SQL: query}
		for _, tc := range []struct {
			input    any
			expected any
		}{
			{"2281 2281", "2281 2281"}, {"17 2281", "17 2281"}, {"", ""}, {nil, nil},
			{" \t0x10 010 -1\r", "16 8 4294967295"}, {"1-2", "1 4294967294"},
		} {
			raw(c, query, tc.expected, tc.input)
			raw(held, query, tc.expected, tc.input)
			fresh, err := c.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			raw(fixture.HeldStatement{Executor: c, Statement: fresh, SQL: query}, query, tc.expected, tc.input)
			fresh.Close()
		}
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT v::text AS v FROM ac_vector_values ORDER BY id", []string{"v"}, []string{"TEXT"}, [][]any{{"2281 2281"}, {""}, {nil}}))
		// REL_18_6 btoidvectorcmp is length-first, then unsigned OID words.
		// Qualify the carrier so ORDER BY cannot resolve to its TEXT projection.
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT q.v::text AS rendered FROM (VALUES (''::oidvector),('4294967295'::oidvector),('0 0'::oidvector),('0 2'::oidvector),('0 4294967295'::oidvector),(NULL::oidvector)) q(v) ORDER BY q.v NULLS LAST",
			[]string{"rendered"}, []string{"TEXT"}, [][]any{{""}, {"4294967295"}, {"0 0"}, {"0 2"}, {"0 4294967295"}, {nil}}))
		createAggregateBoth(t, p, b, c, fixture.Select("SELECT typtype::text AS kind,typcategory::text AS category,typispreferred,typelem::bigint AS element,typarray::bigint AS companion,typinput::oid::bigint AS input,typoutput::oid::bigint AS output,typreceive::oid::bigint AS receive,typsend::oid::bigint AS send,typanalyze::oid::bigint AS analyze,typsubscript::oid::bigint AS subscript,typlen,typbyval,typalign::text AS alignment,typstorage::text AS storage,typtypmod,typcollation::bigint AS collation FROM pg_type WHERE oid=30",
			[]string{"kind", "category", "typispreferred", "element", "companion", "input", "output", "receive", "send", "analyze", "subscript", "typlen", "typbyval", "alignment", "storage", "typtypmod", "collation"},
			[]string{"TEXT", "TEXT", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT2", "BOOL", "TEXT", "TEXT", "INT4", "INT8"},
			[][]any{{"b", "A", false, int64(26), int64(1013), int64(54), int64(55), int64(2420), int64(2421), int64(0), int64(6179), int64(-1), false, "i", "p", int64(-1), int64(0)}}))
	}
}
