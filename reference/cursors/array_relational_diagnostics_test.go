package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalResolutionDiagnostics(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Independent complete records confirmed by the pinned PostgreSQL18 oracle.
		for _, prefix := range []string{"", "/*é*/ "} {
			for _, tc := range []struct{ sql, token, message, hint, code string }{
				{"SELECT unnest(ARRAY[1],ARRAY['a'])", "unnest", "function unnest(integer[], text[]) does not exist", arrayInspectHint, "42883"},
				{"SELECT * FROM pg_catalog.unnest(ARRAY[1],ARRAY['a'])", "pg_catalog.unnest", "function pg_catalog.unnest(integer[], text[]) does not exist", arrayInspectHint, "42883"},
				{"SELECT unnest(NULL)", "unnest", "function unnest(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "42725"},
				{"SELECT * FROM unnest(NULL)", "unnest", "function unnest(unknown) is not unique", "Could not choose a best candidate function. You might need to add explicit type casts.", "42725"},
			} {
				q := prefix + tc.sql
				t.Run(q, func(t *testing.T) {
					arrayRelationalError(t, p, b, c, q, arrayInspectDiagnostic(q, tc.token, tc.code, tc.message, tc.hint), false)
				})
			}
		}
		for _, tc := range []struct{ q, token, code, message, hint string }{
			{"SELECT * FROM unnest(DISTINCT ARRAY[1],ARRAY['a'])", "unnest", "42883", "function unnest(integer[], text[]) does not exist", arrayInspectHint},
			{"SELECT * FROM unnest(ARRAY[1],ARRAY['a'] ORDER BY 1)", "unnest", "42883", "function unnest(integer[], text[]) does not exist", arrayInspectHint},
			{"SELECT * FROM unnest(ARRAY[1],ARRAY['a']) FILTER (WHERE TRUE)", "WHERE", "42601", `syntax error at or near "WHERE"`, ""},
			{"SELECT * FROM unnest(ARRAY[1],ARRAY['a']) OVER ()", ")", "42601", `syntax error at or near ")"`, ""},
			{"SELECT * FROM unnest(*)", "unnest", "42883", "function unnest() does not exist", arrayInspectHint},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := arrayInspectDiagnostic(tc.q, tc.token, tc.code, tc.message, tc.hint)
				if tc.q == "SELECT * FROM unnest(ARRAY[1],ARRAY['a']) OVER ()" {
					d.Position = "49"
				}
				arrayRelationalError(t, p, b, c, tc.q, d, false)
			})
		}
		// Unary explicit VARIADIC was observed valid, not a variadic overload.
		variadic := fixture.Select("SELECT * FROM unnest(VARIADIC ARRAY[1,2])", []string{"unnest"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		variadic.Bag = true
		arrayRelationalBoth(t, p, b, c, variadic)
		for _, tc := range []struct{ q, code, message, hint, position string }{
			{"SELECT * FROM unnest(ARRAY[1],VARIADIC ARRAY['a'])", "42883", "function unnest(integer[], text[]) does not exist", arrayInspectHint, "15"},
			{"SELECT * FROM unnest(ARRAY[1],ARRAY['a']) AS u(x INT,y TEXT)", "42601", "UNNEST() with multiple arguments cannot have a column definition list", "Use separate UNNEST() calls inside ROWS FROM(), and attach a column definition list to each one.", "48"},
			{"SELECT * FROM ROWS FROM (unnest(ARRAY[1],ARRAY['a']) AS (x INT,y TEXT))", "42883", "function unnest(integer[], text[]) does not exist", arrayInspectHint, "26"},
			{"SELECT * FROM ROWS FROM (unnest(ARRAY[1]),unnest(ARRAY['a'])) AS u(x INT,y TEXT)", "42601", "ROWS FROM() with multiple functions cannot have a column definition list", "Put a separate column definition list for each function inside ROWS FROM().", "68"},
			{"SELECT * FROM unnest(ARRAY[1]) AS u(x INT)", "42601", `a column definition list is only allowed for functions returning "record"`, "", "37"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint, d.Position = tc.hint, tc.position
				arrayRelationalError(t, p, b, c, tc.q, d, false)
			})
		}
	})
}

func TestReferenceArrayRelationalTypingMetadata(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE DOMAIN ar_int AS INT", "CREATE DOMAIN ar_array AS INT[]", "CREATE DOMAIN ar_word AS VARCHAR(3)", "CREATE COLLATION ar_coll FROM pg_catalog.\"C\""} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct {
			q, typ string
			want   any
		}{
			{"SELECT array_sample(ARRAY[1],1) AS v", "_INT4", "{1}"},
			{"SELECT array_shuffle(ARRAY['a']) AS v", "_TEXT", "{a}"},
			{"SELECT array_sample('{7}'::ar_array,1) AS v", "_INT4", "{7}"},
			{"SELECT array_sample('{7}'::ar_int[],1) AS v", "_AR_INT", "{7}"},
			{"SELECT array_shuffle(NULL::ar_int[]) AS v", "_AR_INT", nil},
			{"SELECT unnest('{7}'::ar_int[]) AS v", "AR_INT", int64(7)},
			{"SELECT unnest(NULL::ar_word[]) AS v", "AR_WORD", nil},
		} {
			t.Run(tc.q, func(t *testing.T) {
				typ := tc.typ
				if b.PG && typ == "_AR_INT" {
					typ = ""
				}
				if typ == "AR_INT" {
					typ = "INT4"
				}
				if typ == "AR_WORD" {
					typ = "VARCHAR"
				}
				raw := tc.typ
				switch raw {
				case "_AR_INT":
					raw = "_INT4"
				case "AR_INT":
					raw = "INT4"
				case "AR_WORD":
					raw = "VARCHAR"
				}
				want := [][]any{{tc.want}}
				if tc.q == "SELECT unnest(NULL::ar_word[]) AS v" {
					want = [][]any{}
				}
				s := fixture.ArrayReferenceSelect(tc.q, []string{"v"}, []string{typ}, []string{raw}, want, b.PG)
				arrayRelationalBoth(t, p, b, c, s)
			})
		}
		// Complete diagnostic records below were independently observed in both
		// direct execution and preparation on the pinned PostgreSQL18 fixture.
		for _, tc := range []struct{ q, token, code, message, hint string }{
			{"SELECT array_shuffle(1)", "array_shuffle", "42883", "function array_shuffle(integer) does not exist", arrayInspectHint},
			{"SELECT array_sample(ARRAY[1],1::bigint)", "array_sample", "42883", "function array_sample(integer[], bigint) does not exist", arrayInspectHint},
			{"SELECT array_sample(ARRAY[1],1.5)", "array_sample", "42883", "function array_sample(integer[], numeric) does not exist", arrayInspectHint},
			{"SELECT array_shuffle(NULL)", "", "42804", "could not determine polymorphic type because input has type unknown", ""},
			{"SELECT array_sample(NULL,1)", "", "42804", "could not determine polymorphic type because input has type unknown", ""},
			{"SELECT array_shuffle(a => ARRAY[1])", "array_shuffle", "42883", "function array_shuffle(a => integer[]) does not exist", arrayInspectHint},
			{"SELECT array_sample(a => ARRAY[1],n => 1)", "array_sample", "42883", "function array_sample(a => integer[], n => integer) does not exist", arrayInspectHint},
			{"SELECT array_sample(ARRAY[1])", "array_sample", "42883", "function array_sample(integer[]) does not exist", arrayInspectHint},
			{"SELECT array_shuffle(ARRAY[1],1)", "array_shuffle", "42883", "function array_shuffle(integer[], integer) does not exist", arrayInspectHint},
			{"SELECT array_sample(VARIADIC ARRAY[1,2])", "array_sample", "42883", "function array_sample(integer[]) does not exist", arrayInspectHint},
			{"SELECT array_shuffle(DISTINCT ARRAY[1])", "array_shuffle", "42809", "DISTINCT specified, but array_shuffle is not an aggregate function", ""},
			{"SELECT array_sample(ARRAY[1],1) FILTER (WHERE TRUE)", "array_sample", "42809", "FILTER specified, but array_sample is not an aggregate function", ""},
			{"SELECT array_shuffle(ARRAY[1]) OVER ()", "array_shuffle", "42809", "OVER specified, but array_shuffle is not a window function nor an aggregate function", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				arrayRelationalError(t, p, b, c, tc.q, arrayInspectDiagnostic(tc.q, tc.token, tc.code, tc.message, tc.hint), false)
			})
		}
		domainTypes := []string{"_AR_INT", "_AR_WORD"}
		if b.PG {
			domainTypes = []string{"", ""}
		}
		domainArrays := fixture.ArrayReferenceSelect("SELECT array_sample('{7}'::ar_int[],1) AS v,array_shuffle(NULL::ar_word[]) AS w", []string{"v", "w"}, domainTypes, []string{"_INT4", "_VARCHAR"}, [][]any{{"{7}", nil}}, b.PG)
		physical := domainArrays.CheckMetadata
		domainArrays.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			physical(t, rows)
			cols, err := rows.ColumnTypes()
			if err != nil {
				t.Fatal(err)
			}
			for _, col := range cols {
				if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
					t.Fatalf("domain-array decimal %d,%d/%t", p, s, known)
				}
				if nullable, known := col.Nullable(); nullable || known {
					t.Fatalf("domain-array nullable %t/%t", nullable, known)
				}
			}
		}
		arrayRelationalBoth(t, p, b, c, domainArrays)
		domainUnnest := fixture.Select("SELECT unnest('{7,NULL}'::ar_int[]) AS v", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}, {nil}})
		domainUnnest.Bag = true
		domainUnnest.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
			fixture.JSONTypeMetadataCheck(t, rows, b.PG, domainUnnest.Columns, domainUnnest.Types)
		}
		arrayRelationalBoth(t, p, b, c, domainUnnest)
		for _, tc := range []struct {
			q      string
			length int64
			rows   [][]any
		}{
			{"SELECT * FROM unnest('{abc,NULL}'::ar_word[]) AS u(v)", 3, [][]any{{"abc"}, {nil}}},
			{"SELECT * FROM unnest(ARRAY['abc'::varchar(3)] COLLATE ar_coll) AS u(v)", 9223372036854775807, [][]any{{"abc"}}},
		} {
			s := fixture.Select(tc.q, []string{"v"}, []string{"VARCHAR"}, tc.rows)
			s.Bag = true
			length := tc.length
			// pq interprets unsized typmod -1 as length -5. Native exposes
			// truthful unbounded VARCHAR; do not reproduce the adapter bug.
			if b.PG && length == 9223372036854775807 {
				length = -5
			}
			s.CheckMetadata = arrayRelationalVarcharMetadata(length)
			arrayRelationalBoth(t, p, b, c, s)
		}
		// Assert outer-domain peeling and element-domain identity on the actual
		// array results above, including ColumnTypes and raw transport. Keep
		// parameter-bound domains on the same direct/new/held execution routes;
		// type/collation inquiry functions are not prerequisites of this feature.
		for _, tc := range []struct{ q, typ string }{
			{"SELECT array_sample($1::ar_array,1) AS v", "_INT4"},
			{"SELECT array_sample($1::ar_int[],1) AS v", "_AR_INT"},
		} {
			typ := tc.typ
			if b.PG && typ == "_AR_INT" {
				typ = ""
			}
			s := fixture.ArrayReferenceSelect(tc.q, []string{"v"}, []string{typ}, []string{"_INT4"}, [][]any{{"{7}"}}, b.PG, "{7}")
			arrayRelationalBoth(t, p, b, c, s)
		}
		for _, tc := range []struct{ q, code, message, detail, hint, position string }{
			{"SELECT array_sample(NULL::int[],1::bigint)", "42883", "function array_sample(integer[], bigint) does not exist", "", arrayInspectHint, "8"},
			{"SELECT array_sample('{1,,2}'::int[],NULL::int)", "22P02", `malformed array literal: "{1,,2}"`, `Unexpected "," character.`, "", "21"},
			{"SELECT array_sample(ARRAY[1],n => 1)", "42883", "function array_sample(integer[], n => integer) does not exist", "", arrayInspectHint, "8"},
			{"SELECT * FROM unnest(ARRAY[1],NULL)", "42725", "function pg_catalog.unnest(unknown) is not unique", "", "Could not choose a best candidate function. You might need to add explicit type casts.", "15"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Detail, d.Hint, d.Position = tc.detail, tc.hint, tc.position
				arrayRelationalError(t, p, b, c, tc.q, d, false)
			})
		}
		for _, prepared := range []bool{false, true} {
			arrayRelationalRows(t, p, c, "SELECT array_shuffle(VARIADIC ARRAY[1,2]) AS v", prepared, nil, func(rows *sql.Rows) {
				arrayRelationalArrayMetadata(t, rows, b.PG)
				if !rows.Next() {
					t.Fatal("missing VARIADIC shuffle result")
				}
				var value string
				if err := rows.Scan(&value); err != nil {
					t.Fatal(err)
				}
				if value != "{1,2}" && value != "{2,1}" {
					t.Fatalf("VARIADIC shuffle changed multiset: %q", value)
				}
				if rows.Next() {
					t.Fatal("extra VARIADIC shuffle result")
				}
			})
		}
		for _, q := range []string{"SELECT array_sample($1::int[],$2::int) AS v", "SELECT array_shuffle($1::int[]) AS v"} {
			args := []any{"{7}"}
			if q == "SELECT array_sample($1::int[],$2::int) AS v" {
				args = append(args, 1)
			}
			s := fixture.ArrayReferenceSelect(q, []string{"v"}, []string{"_INT4"}, []string{"_INT4"}, [][]any{{"{7}"}}, b.PG, args...)
			arrayRelationalBoth(t, p, b, c, s)
		}
	})
}

func arrayRelationalVarcharMetadata(length int64) func(*testing.T, *sql.Rows) {
	return func(t *testing.T, rows *sql.Rows) {
		t.Helper()
		cols, err := rows.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 1 {
			t.Fatalf("varchar metadata columns %d", len(cols))
		}
		col := cols[0]
		if col.Name() != "v" || col.DatabaseTypeName() != "VARCHAR" || col.ScanType() != reflect.TypeFor[string]() {
			t.Fatalf("varchar metadata %q/%q/%v", col.Name(), col.DatabaseTypeName(), col.ScanType())
		}
		if n, known := col.Length(); n != length || !known {
			t.Fatalf("varchar length %d/%t want %d/true", n, known, length)
		}
		if p, s, known := col.DecimalSize(); p != 0 || s != 0 || known {
			t.Fatalf("varchar decimal %d,%d/%t", p, s, known)
		}
		if nullable, known := col.Nullable(); nullable || known {
			t.Fatalf("varchar nullable %t/%t", nullable, known)
		}
	}
}
