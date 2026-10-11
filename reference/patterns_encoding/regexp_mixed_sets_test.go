package reference_test

import (
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

// Every witness combines a selected regexp call with the incumbent string set
// provider. REL_18_0 nodeProjectSet.c pads exhausted same-level siblings, not a
// Cartesian product; nested/wrapped calls introduce distinct projection levels.
func TestReferenceRegexpMixedProjection(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			name, q      string
			names, types []string
			rows         [][]any
		}{
			{"unequal", `SELECT string_to_table('a,b',',') AS s,regexp_split_to_table('x y z',' ') AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", "y"}, {nil, "z"}}},
			{"unequal_reversed", `SELECT regexp_split_to_table('x y z',' ') AS r,string_to_table('a,b',',') AS s`, []string{"r", "s"}, []string{"TEXT", "TEXT"}, [][]any{{"x", "a"}, {"y", "b"}, {"z", nil}}},
			{"equal", `SELECT string_to_table('a,b',',') AS s,regexp_split_to_table('x y',' ') AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", "y"}}},
			{"one_vs_two", `SELECT string_to_table('a',',') AS s,regexp_split_to_table('x y',' ') AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {nil, "y"}}},
			{"empty_string_peer", `SELECT string_to_table('',',') AS s,regexp_split_to_table('x y',' ') AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{nil, "x"}, {nil, "y"}}},
			{"empty_regexp_peer", `SELECT string_to_table('a,b',',') AS s,regexp_matches('x','a') AS m`, []string{"s", "m"}, []string{"TEXT", "_TEXT"}, [][]any{{"a", nil}, {"b", nil}}},
			{"both_empty", `SELECT string_to_table('',',') AS s,regexp_matches('x','a') AS m`, []string{"s", "m"}, []string{"TEXT", "_TEXT"}, nil},
			{"both_null", `SELECT string_to_table(NULL::text,',') AS s,regexp_split_to_table(NULL::text,' ') AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, nil},
			{"array_peer", `SELECT string_to_table('x,y,z',',') AS s,regexp_matches('ab','(.)','g') AS m`, []string{"s", "m"}, []string{"TEXT", "_TEXT"}, [][]any{{"x", "{a}"}, {"y", "{b}"}, {"z", nil}}},
			{"regexp_over_string", `SELECT regexp_split_to_table(string_to_table('a:b,c:d',','),':') AS r`, []string{"r"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"c"}, {"d"}}},
			{"string_over_regexp", `SELECT string_to_table(regexp_split_to_table('a:b,c:d',','),':') AS s`, []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"c"}, {"d"}}},
			{"nested_regexp_peer", `SELECT regexp_split_to_table(string_to_table('a:b,c:d',','),':') AS r,string_to_table('x,y,z',',') AS s`, []string{"r", "s"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", "x"}, {"c", "y"}, {"d", "y"}}},
			{"nested_string_peer", `SELECT string_to_table(regexp_split_to_table('a:b,c:d',','),':') AS s,regexp_split_to_table('x y z',' ') AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"b", "x"}, {"c", "y"}, {"d", "y"}}},
			{"wrappers", `SELECT upper(string_to_table('a,b',',')) AS s,upper(regexp_split_to_table('x y z',' ')) AS r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"A", "X"}, {"B", "Y"}, {nil, "Z"}}},
			{"hidden_regexp_key", `SELECT string_to_table('b,a',',') AS s ORDER BY regexp_split_to_table('x y z',' ')`, []string{"s"}, []string{"TEXT"}, [][]any{{"b"}, {"a"}, {nil}}},
			{"hidden_string_key", `SELECT regexp_split_to_table('b a',' ') AS r ORDER BY string_to_table('x,y,z',',')`, []string{"r"}, []string{"TEXT"}, [][]any{{"b"}, {"a"}, {nil}}},
		} {
			t.Run(c.name, func(t *testing.T) {
				regexpRoutes(t, p, b, regexpRows(b, c.q, c.names, c.types, c.rows))
			})
		}

		// Reuse the actual prepared handle with changing cardinality/NULL input.
		// Zero rows still retain independently specified TEXT/TEXT metadata.
		const heldSQL = `SELECT string_to_table($1,',') AS s,regexp_split_to_table($2,' ') AS r`
		stmt, err := b.DB.PrepareContext(p.Ctx, heldSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: heldSQL}
		for _, c := range []struct {
			name string
			s, r any
			rows [][]any
		}{
			{"three", "a,b", "x y z", [][]any{{"a", "x"}, {"b", "y"}, {nil, "z"}}},
			{"empty_string", "", "x y", [][]any{{nil, "x"}, {nil, "y"}}},
			{"empty_regexp", "a,b", nil, [][]any{{"a", nil}, {"b", nil}}},
			{"all_null", nil, nil, nil},
			{"unicode_recovery", "é,🙂", "甲 乙", [][]any{{"é", "甲"}, {"🙂", "乙"}}},
		} {
			t.Run("held_"+c.name, func(t *testing.T) {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, regexpRows(b, heldSQL, []string{"s", "r"}, []string{"TEXT", "TEXT"}, c.rows, c.s, c.r))
			})
		}

		// Pin one physical connection for sequence session memory. Explicit
		// output and subsequent currval distinguish scalar output evaluation
		// from the final all-exhausted attempt and wrapper projection levels.
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		run := func(t *testing.T, s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
		run(t, fixture.Exec(`CREATE SEQUENCE regexp_mixed_attempt START 1`, 0))
		for _, c := range []struct {
			name, q string
			rows    [][]any
			curr    int64
		}{
			{"bare_scalar_attempt", `SELECT nextval('regexp_mixed_attempt') AS n,string_to_table('a,b',',') AS s,regexp_split_to_table('x y z',' ') AS r`, [][]any{{int64(1), "a", "x"}, {int64(2), "b", "y"}, {int64(3), nil, "z"}}, 4},
			{"wrapped_scalar_outputs", `SELECT nextval('regexp_mixed_attempt') AS n,upper(string_to_table('a,b',',')) AS s,upper(regexp_split_to_table('x y z',' ')) AS r`, [][]any{{int64(1), "A", "X"}, {int64(2), "B", "Y"}, {int64(3), nil, "Z"}}, 3},
			{"all_empty_attempt", `SELECT nextval('regexp_mixed_attempt') AS n,string_to_table('',',') AS s,regexp_matches('x','a') AS r`, nil, 1},
		} {
			t.Run(c.name, func(t *testing.T) {
				for _, prepared := range []bool{false, true} {
					t.Run(fmt.Sprintf("prepared=%t", prepared), func(t *testing.T) {
						run(t, fixture.Select(`SELECT setval('regexp_mixed_attempt',1,false) AS n`, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
						types := []string{"INT8", "TEXT", "TEXT"}
						if c.name == "all_empty_attempt" {
							types[2] = "_TEXT"
						}
						s := regexpRows(b, c.q, []string{"n", "s", "r"}, types, c.rows)
						s.Prepared = prepared
						run(t, s)
						run(t, fixture.Select(`SELECT currval('regexp_mixed_attempt') AS n`, []string{"n"}, []string{"INT8"}, [][]any{{c.curr}}))
					})
				}
			})
		}
	})
}

// ROWS FROM combines the already-admitted closed provider families in one zip;
// distinct FROM items still form ordinary joins with per-left invocation.
func TestReferenceRegexpMixedFrom(t *testing.T) {
	regexpScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, c := range []struct {
			name, q      string
			names, types []string
			rows         [][]any
		}{
			{"rows_unequal", `SELECT s,r,o FROM ROWS FROM(string_to_table('a,b',','),regexp_split_to_table('x y z',' ')) WITH ORDINALITY t(s,r,o) ORDER BY o`, []string{"s", "r", "o"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{"a", "x", int64(1)}, {"b", "y", int64(2)}, {nil, "z", int64(3)}}},
			{"rows_empty_string", `SELECT s,r,o FROM ROWS FROM(string_to_table('',','),regexp_split_to_table('x y',' ')) WITH ORDINALITY t(s,r,o) ORDER BY o`, []string{"s", "r", "o"}, []string{"TEXT", "TEXT", "INT8"}, [][]any{{nil, "x", int64(1)}, {nil, "y", int64(2)}}},
			{"rows_empty_regexp", `SELECT s,m,o FROM ROWS FROM(string_to_table('a,b',','),regexp_matches('x','a')) WITH ORDINALITY t(s,m,o) ORDER BY o`, []string{"s", "m", "o"}, []string{"TEXT", "_TEXT", "INT8"}, [][]any{{"a", nil, int64(1)}, {"b", nil, int64(2)}}},
			{"rows_array", `SELECT m,s,o FROM ROWS FROM(regexp_matches('ab','(.)','g'),string_to_table('x,y,z',',')) WITH ORDINALITY t(m,s,o) ORDER BY o`, []string{"m", "s", "o"}, []string{"_TEXT", "TEXT", "INT8"}, [][]any{{"{a}", "x", int64(1)}, {"{b}", "y", int64(2)}, {nil, "z", int64(3)}}},
			{"rows_all_empty", `SELECT s,m,o FROM ROWS FROM(string_to_table('',','),regexp_matches('x','a')) WITH ORDINALITY t(s,m,o)`, []string{"s", "m", "o"}, []string{"TEXT", "_TEXT", "INT8"}, nil},
			{"separate_cartesian", `SELECT s,r FROM string_to_table('a,b',',') a(s),regexp_split_to_table('x y',' ') b(r) ORDER BY s,r`, []string{"s", "r"}, []string{"TEXT", "TEXT"}, [][]any{{"a", "x"}, {"a", "y"}, {"b", "x"}, {"b", "y"}}},
			{"string_to_regexp", `SELECT s.v,r.x FROM string_to_table('a:b,c:d',',') s(v) CROSS JOIN regexp_split_to_table(s.v,':') r(x) ORDER BY s.v,r.x`, []string{"v", "x"}, []string{"TEXT", "TEXT"}, [][]any{{"a:b", "a"}, {"a:b", "b"}, {"c:d", "c"}, {"c:d", "d"}}},
			{"regexp_to_string", `SELECT r.v,s.x FROM regexp_split_to_table('a:b,c:d',',') r(v) CROSS JOIN string_to_table(r.v,':') s(x) ORDER BY r.v,s.x`, []string{"v", "x"}, []string{"TEXT", "TEXT"}, [][]any{{"a:b", "a"}, {"a:b", "b"}, {"c:d", "c"}, {"c:d", "d"}}},
			{"left_null_extension", `SELECT s.v,r.m,r.o FROM string_to_table('aa,x',',') s(v) LEFT JOIN regexp_matches(s.v,'a','g') WITH ORDINALITY r(m,o) ON true ORDER BY s.v,r.o`, []string{"v", "m", "o"}, []string{"TEXT", "_TEXT", "INT8"}, [][]any{{"aa", "{a}", int64(1)}, {"aa", "{a}", int64(2)}, {"x", nil, nil}}},
			{"nested_offsets", `SELECT a.id,b.v,c.v AS cv,r.x FROM (VALUES(1,'A'),(2,'Q')) a(id,s) CROSS JOIN (string_to_table('B',',') b(v) CROSS JOIN (string_to_table('C',',') c(v) CROSS JOIN regexp_split_to_table(a.s||' '||b.v||' '||c.v,' ') r(x))) ORDER BY a.id,r.x`, []string{"id", "v", "cv", "x"}, []string{"INT4", "TEXT", "TEXT", "TEXT"}, [][]any{{int64(1), "B", "C", "A"}, {int64(1), "B", "C", "B"}, {int64(1), "B", "C", "C"}, {int64(2), "B", "C", "B"}, {int64(2), "B", "C", "C"}, {int64(2), "B", "C", "Q"}}},
			{"correlated_rows", `SELECT s.v,t.a,t.b,t.o FROM string_to_table('a b,c d',',') s(v) CROSS JOIN ROWS FROM(regexp_split_to_table(s.v,' '),string_to_table('X',',')) WITH ORDINALITY t(a,b,o) ORDER BY s.v,t.o`, []string{"v", "a", "b", "o"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{"a b", "a", "X", int64(1)}, {"a b", "b", nil, int64(2)}, {"c d", "c", "X", int64(1)}, {"c d", "d", nil, int64(2)}}},
		} {
			t.Run(c.name, func(t *testing.T) {
				s := regexpRows(b, c.q, c.names, c.types, c.rows)
				if c.name == "left_null_extension" {
					s.CheckMetadata = regexpMetadataNullable(b.PG, []string{"m", "o"})
				}
				regexpRoutes(t, p, b, s)
			})
		}
		for _, join := range []string{"RIGHT", "FULL"} {
			t.Run(join, func(t *testing.T) {
				// Internal captures of the string provider are satisfied within
				// the RHS, so an empty independent enclosing left preserves it.
				q := `SELECT a.id,s.v,r.x FROM (SELECT 1 AS id WHERE false) a ` + join + ` JOIN (string_to_table('a:b,c:d',',') s(v) CROSS JOIN regexp_split_to_table(s.v,':') r(x)) ON true ORDER BY s.v,r.x`
				s := regexpRows(b, q, []string{"id", "v", "x"}, []string{"INT4", "TEXT", "TEXT"}, [][]any{{nil, "a:b", "a"}, {nil, "a:b", "b"}, {nil, "c:d", "c"}, {nil, "c:d", "d"}})
				nullable := []string{"id"}
				if join == "FULL" {
					nullable = []string{"id", "v", "x"}
				}
				s.CheckMetadata = regexpMetadataNullable(b.PG, nullable)
				regexpRoutes(t, p, b, s)

				// The selected RHS captures only inherited P, not its own empty
				// L. It stays legal and must retain the real ancestor value.
				q = `SELECT a.v,b.v AS bv,r.x,r.o FROM string_to_table('a b,c d',',') a(v),(string_to_table('',',') b(v) ` + join + ` JOIN regexp_split_to_table(a.v,' ') WITH ORDINALITY r(x,o) ON true) ORDER BY a.v,r.o`
				s = regexpRows(b, q, []string{"v", "bv", "x", "o"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, [][]any{{"a b", nil, "a", int64(1)}, {"a b", nil, "b", int64(2)}, {"c d", nil, "c", int64(1)}, {"c d", nil, "d", int64(2)}})
				nullable = []string{"bv"}
				if join == "FULL" {
					nullable = []string{"bv", "x", "o"}
				}
				s.CheckMetadata = regexpMetadataNullable(b.PG, nullable)
				regexpRoutes(t, p, b, s)

				// Genuine own-L capture, and legal P combined with illegal L,
				// must retain full42P10 provenance at the actual b.v reference.
				for _, from := range []string{
					`string_to_table('a b',',') b(v) ` + join + ` JOIN regexp_split_to_table(b.v,' ') r(x) ON true`,
					`string_to_table('A',',') a(v),(string_to_table('B',',') b(v) ` + join + ` JOIN regexp_split_to_table(a.v||' '||b.v,' ') r(x) ON true)`,
				} {
					q = `SELECT /* é🙂 */ 1 FROM ` + from
					s := regexpBindError(q, "42P10", `invalid reference to FROM-clause entry for table "b"`, "", "b.v,' '")
					s.Diagnostic.Detail = "The combining JOIN type must be INNER or LEFT for a LATERAL reference."
					regexpRoutes(t, p, b, s)
				}
			})
		}
	})
}
