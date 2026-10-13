package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArraySearchTypesAndFrames(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, expr, typ string
			want            any
		}{
			{"bigint_remove", "array_remove(ARRAY[1,2],1::bigint)", "_INT8", "{2}"},
			{"replacement_widens_absent", "array_replace(ARRAY[1,2],9,3::bigint)", "_INT8", "{1,2}"},
			{"numeric_search", "array_position(ARRAY[1,2],2.0::numeric)", "INT4", int64(2)},
			{"bpchar_padding", "array_position(ARRAY['a '::char(3)],'a'::char(1))", "INT4", int64(1)},
			{"numeric_nan", "array_position(ARRAY['NaN'::numeric],'NaN'::numeric)", "INT4", int64(1)},
			{"float_nan", "array_positions(ARRAY['NaN'::float8],'NaN'::float8)", "_INT4", "{1}"},
			{"jsonb_structure", "array_position(ARRAY['{\"a\":1,\"b\":2}'::jsonb],'{\"b\":2,\"a\":1}'::jsonb)", "INT4", int64(1)},
			{"date", "array_position(ARRAY['2000-01-01'::date],'2000-01-01'::date)", "INT4", int64(1)},
			{"timestamp", "array_position(ARRAY['2000-01-01 00:00:00'::timestamp],'2000-01-01'::timestamp)", "INT4", int64(1)},
			{"unknown_array", "array_position('{1,2}',2)", "INT4", int64(2)},
			{"unknown_operator", "'{1,1}' @> ARRAY[1]", "BOOL", true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				arraySearchBoth(t, p, b, c, arraySearchSelect(b, "SELECT "+tc.expr+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.want}}))
			})
		}
		for _, q := range []string{
			"CREATE DOMAIN as_element AS INT",
			"CREATE DOMAIN as_other_element AS INT",
			"CREATE DOMAIN as_outer AS INT[]",
			"CREATE TYPE as_enum AS ENUM ('a','b')",
			"CREATE TYPE as_other_enum AS ENUM ('a','b')",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		arraySearchBoth(t, p, b, c, fixture.Select("SELECT array_position(ARRAY['a'::as_enum,'b'::as_enum],'b'::as_enum) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(2)}}))
		// PostgreSQL 18.0 observations confirm TEXT as the all-unknown common
		// leaf and retention of a domain when all known leaves agree.
		for _, tc := range []struct {
			name, q, typ, raw string
			want              any
			args              []any
		}{
			{"unknown_position", "SELECT array_position(NULL,NULL) AS v", "INT4", "INT4", nil, nil},
			{"unknown_positions", "SELECT array_positions(NULL,NULL) AS v", "_INT4", "_INT4", nil, nil},
			{"unknown_remove", "SELECT array_remove(NULL,NULL) AS v", "_TEXT", "_TEXT", nil, nil},
			{"unknown_replace", "SELECT array_replace(NULL,NULL,NULL) AS v", "_TEXT", "_TEXT", nil, nil},
			{"quoted_remove", "SELECT array_remove('{a,b}','a') AS v", "_TEXT", "_TEXT", "{b}", nil},
			{"quoted_replace", "SELECT array_replace('{a,b}','a','c') AS v", "_TEXT", "_TEXT", "{c,b}", nil},
			{"same_domain", "SELECT array_remove(ARRAY[1::as_element,2::as_element],1::as_element) AS v", "_AS_ELEMENT", "_INT4", "{2}", nil},
			{"domain_null_search", "SELECT array_remove(ARRAY[1::as_element,2::as_element],NULL) AS v", "_AS_ELEMENT", "_INT4", "{1,2}", nil},
			{"domain_quoted_search", "SELECT array_remove(ARRAY[1::as_element,2::as_element],'1') AS v", "_AS_ELEMENT", "_INT4", "{2}", nil},
			{"domain_null_replacement", "SELECT array_replace(ARRAY[1::as_element,2::as_element],1::as_element,NULL) AS v", "_AS_ELEMENT", "_INT4", "{NULL,2}", nil},
			{"domain_unknown_array", "SELECT array_remove('{1,2}',1::as_element) AS v", "_AS_ELEMENT", "_INT4", "{2}", nil},
			{"domain_inferred_parameters", "SELECT array_replace($1,$2::as_element,$3) AS v", "_AS_ELEMENT", "_INT4", "{NULL,2}", []any{"{1,2}", 1, nil}},
			{"domain_base", "SELECT array_remove(ARRAY[1::as_element,2::as_element],1) AS v", "_INT4", "_INT4", "{2}", nil},
			{"distinct_domains", "SELECT array_replace(ARRAY[1::as_element],1::as_element,2::as_other_element) AS v", "_INT4", "_INT4", "{2}", nil},
			{"outer_domain_remove", "SELECT array_remove('{1,2}'::as_outer,1) AS v", "_INT4", "_INT4", "{2}", nil},
			{"outer_domain_membership", "SELECT '{1}'::as_outer @> ARRAY[1] AS v", "BOOL", "BOOL", true, nil},
			{"enum_replace", "SELECT array_replace(ARRAY['a'::as_enum],'a'::as_enum,'b'::as_enum) AS v", "_AS_ENUM", "_TEXT", "{b}", nil},
			{"bpchar_payload", "SELECT array_replace(ARRAY['a '::char(3)],'z'::char(1),'b'::char(2)) AS v", "_BPCHAR", "_BPCHAR", "{\"a  \"}", nil},
			{"varchar_payload", "SELECT array_replace(ARRAY['abc'::varchar(3)],'z'::varchar(1),'b'::varchar(2)) AS v", "_VARCHAR", "_VARCHAR", "{abc}", nil},
			{"numeric_payload", "SELECT array_replace(ARRAY[1.23::numeric(4,2)],9::numeric,3.456::numeric(5,3)) AS v", "_NUMERIC", "_NUMERIC", "{1.23}", nil},
			{"timestamp_payload", "SELECT array_replace(ARRAY['2000-01-01 00:00:00.123'::timestamp(2)],'1999-01-01'::timestamp,'2001-01-01'::timestamp(3)) AS v", "_TIMESTAMP", "_TIMESTAMP", "{\"2000-01-01 00:00:00.12\"}", nil},
			{"inferred_array", "SELECT array_position($1, $2::int) AS v", "INT4", "INT4", int64(2), []any{"{1,2}", 2}},
			{"inferred_scalar", "SELECT array_position($1::int[], $2) AS v", "INT4", "INT4", int64(2), []any{"{1,2}", 2}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				typ := tc.typ
				if b.PG && (tc.typ == "_AS_ELEMENT" || tc.typ == "_AS_ENUM") {
					// pq does not publish a database type name for dynamic arrays.
					typ = ""
				}
				s := fixture.ArrayReferenceSelect(tc.q, []string{"v"}, []string{typ}, []string{tc.raw}, [][]any{{tc.want}}, b.PG, tc.args...)
				if tc.name == "numeric_payload" {
					basic := s.CheckMetadata
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						basic(t, rows)
						columns, err := rows.ColumnTypes()
						if err != nil {
							t.Fatal(err)
						}
						precision, scale, known := columns[0].DecimalSize()
						wantPrecision, wantScale, wantKnown := int64(0), int64(0), false
						if b.PG {
							wantPrecision, wantScale, wantKnown = 65535, 65531, true
						}
						if precision != wantPrecision || scale != wantScale || known != wantKnown {
							t.Fatalf("unsized replacement decimal=%d,%d/%t want %d,%d/%t", precision, scale, known, wantPrecision, wantScale, wantKnown)
						}
					}
				}
				arraySearchBoth(t, p, b, c, s)
			})
		}
		const operatorHint = "No operator matches the given name and argument types. You might need to add explicit type casts."
		const functionHint = "No function matches the given name and argument types. You might need to add explicit type casts."
		for _, tc := range []struct {
			name, q string
			d       fixture.Diagnostic
			args    []any
		}{
			{"domain_operator", "SELECT ARRAY[1::as_element] @> ARRAY[1] AS v", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: as_element[] @> integer[]", Hint: operatorHint, Position: "29"}, nil},
			{"enum_operator", "SELECT ARRAY['a'::as_enum] && ARRAY['a'::as_other_enum] AS v", fixture.Diagnostic{Code: "42883", Message: "operator does not exist: as_enum[] && as_other_enum[]", Hint: operatorHint, Position: "28"}, nil},
			{"repeated_slot", "SELECT array_position($1,$1) AS v", fixture.Diagnostic{Code: "42P08", Message: "inconsistent types deduced for parameter $1", Detail: "text[] versus text", Position: "26"}, []any{"{1,2}"}},
			{"scalar_slot", "SELECT $1::int,array_position($1,1) AS v", fixture.Diagnostic{Code: "42883", Message: "function array_position(integer, integer) does not exist", Hint: functionHint, Position: "16"}, []any{1}},
			{"incompatible_reuse", "SELECT array_position($1,1),array_position($1,'a'::text) AS v", fixture.Diagnostic{Code: "42883", Message: "function array_position(integer[], text) does not exist", Hint: functionHint, Position: "29"}, []any{"{1,2}"}},
		} {
			t.Run(tc.name, func(t *testing.T) { arraySearchDiagnosticBoth(t, p, b, c, tc.q, tc.d, true, tc.args...) })
		}
		t.Run("held", func(t *testing.T) {
			const q = "SELECT array_position($1::int[],$2::int,$3::int) AS p,array_positions($1::int[],$2::int) AS ps,array_remove($1::int[],$2::int) AS rm,array_replace($1::int[],$2::int,$4::int) AS rp"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			for _, tc := range []struct{ args, want []any }{
				{[]any{arraySearchInput, 7, -1, 9}, []any{int64(0), "{-2,0,2}", "[-2:-1]={NULL,8}", "[-2:2]={9,NULL,9,8,9}"}},
				{[]any{arraySearchInput, nil, -2, 9}, []any{int64(-1), "{-1}", "[-2:1]={7,7,8,7}", "[-2:2]={7,9,7,8,7}"}},
				{[]any{"{}", 7, nil, 9}, []any{nil, "{}", "{}", "{}"}},
				{[]any{nil, nil, nil, nil}, []any{nil, nil, nil, nil}},
			} {
				fixture.RunStep(t, p.Ctx, held, b.Recorder, arraySearchSelect(b, q, []string{"p", "ps", "rm", "rp"}, []string{"INT4", "_INT4", "_INT4", "_INT4"}, [][]any{tc.want}, tc.args...))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, arraySearchRuntimeError(q, "22004", "initial position must not be null", "{NULL}", nil, nil, 9))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, arraySearchSelect(b, q, []string{"p", "ps", "rm", "rp"}, []string{"INT4", "_INT4", "_INT4", "_INT4"}, [][]any{{int64(1), "{1}", "{}", "{9}"}}, "{7}", 7, 1, 9))
		})
		t.Run("held_domain_unknowns", func(t *testing.T) {
			const q = "SELECT array_replace($1,$2::as_element,$3) AS v"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			typ := "_AS_ELEMENT"
			if b.PG {
				typ = ""
			}
			for _, tc := range []struct {
				args []any
				want string
			}{
				{[]any{"{1,2}", 1, nil}, "{NULL,2}"},
				{[]any{"{3,4}", 4, 9}, "{3,9}"},
			} {
				s := fixture.ArrayReferenceSelect(q, []string{"v"}, []string{typ}, []string{"_INT4"}, [][]any{{tc.want}}, b.PG, tc.args...)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		})
		t.Run("array_valued_domain_boundary", func(t *testing.T) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN as_leaf_array AS INT[]", 0))
			const a = `'{"{1}","{2}"}'::as_leaf_array[]`
			const required = `'{"{2}"}'::as_leaf_array[]`
			for _, tc := range []struct {
				name, expression, typ string
				want                  any
			}{
				{"position", "array_position(" + a + ",'{2}'::as_leaf_array)", "INT4", int64(2)},
				{"positions", "array_positions(" + a + ",'{2}'::as_leaf_array)", "_INT4", "{2}"},
				{"remove", "array_remove(" + a + ",'{2}'::as_leaf_array)", "", `{"{1}"}`},
				{"replace", "array_replace(" + a + ",'{2}'::as_leaf_array,'{3}'::as_leaf_array)", "", `{"{1}","{3}"}`},
				{"contains", a + " @> " + required, "BOOL", true},
				{"contained", required + " <@ " + a, "BOOL", true},
				{"overlap", a + " && " + required, "BOOL", true},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := "SELECT " + tc.expression + " AS v"
					if b.PG {
						raw := tc.typ
						if raw == "" {
							raw = "_INT4"
						}
						s := fixture.ArrayReferenceSelect(q, []string{"v"}, []string{tc.typ}, []string{raw}, [][]any{{tc.want}}, b.PG)
						arraySearchBoth(t, p, b, c, s)
					} else {
						arraySearchBoth(t, p, b, c, arraySearchRuntimeError(q, "0A000", "unsupported array element type"))
					}
				})
			}
			// An unavailable native comparator must not defeat earlier fast paths.
			arraySearchBoth(t, p, b, c, fixture.Select("SELECT array_position("+a+",NULL::as_leaf_array,NULL) AS p,array_positions(NULL::as_leaf_array[],NULL::as_leaf_array) AS ps,NULL::as_leaf_array[] && "+a+" AS overlap", []string{"p", "ps", "overlap"}, []string{"INT4", "_INT4", "BOOL"}, [][]any{{nil, nil, nil}}))
			typ := "_AS_LEAF_ARRAY"
			if b.PG {
				typ = ""
			}
			s := fixture.ArrayReferenceSelect("SELECT array_remove('{}'::as_leaf_array[],NULL::as_leaf_array) AS rm,array_replace('{}'::as_leaf_array[],NULL::as_leaf_array,NULL::as_leaf_array) AS rp", []string{"rm", "rp"}, []string{typ, typ}, []string{"_INT4", "_INT4"}, [][]any{{"{}", "{}"}}, b.PG)
			arraySearchBoth(t, p, b, c, s)
		})
	})
}

func TestReferenceArraySearchMetadata(t *testing.T) {
	arraySearchScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE as_metadata(a INT[],s INT)", 0))
		q := "SELECT array_position(a,s) AS p,array_positions(a,s) AS ps,array_remove(a,s) AS rm,array_replace(a,s,9::bigint) AS rp,a @> ARRAY[s] AS contains FROM as_metadata"
		arraySearchBoth(t, p, b, c, arraySearchSelect(b, q, []string{"p", "ps", "rm", "rp", "contains"}, []string{"INT4", "_INT4", "_INT4", "_INT8", "BOOL"}, [][]any{}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO as_metadata VALUES ('{1,NULL,1}',1),(NULL,NULL)", 2))
		s := arraySearchSelect(b, q, []string{"p", "ps", "rm", "rp", "contains"}, []string{"INT4", "_INT4", "_INT4", "_INT8", "BOOL"}, [][]any{{int64(1), "{1,3}", "{NULL}", "{9,NULL,9}", true}, {nil, nil, nil, nil, nil}})
		s.Bag = true
		arraySearchBoth(t, p, b, c, s)
	})
}
