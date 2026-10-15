package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLJSONObjectAggregateParameters(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, tc := range []struct{ q, code, token string }{
			{"SELECT json_objectagg($1:1) AS v", "42P18", ""},
			{"SELECT json_objectagg($1:1) AS v,$1::int AS n", "42P08", "$1"},
		} {
			t.Run(tc.q, func(t *testing.T) {
				q := "/*é😀*/ " + tc.q
				d := fixture.ControlsReferenceDiagnostic(tc.code, "could not determine data type of parameter $1")
				if tc.token != "" {
					d.Position = jsonArrayAggregatePosition(q, tc.token)
				}
				jsonArrayAggregateError(t, p, b, q, d, true, 2)
			})
		}
		for _, tc := range []struct {
			q, typ, want string
			args         []any
		}{
			{"SELECT json_objectagg('a':$1) AS v", "JSON", `{ "a" : "2" }`, []any{"2"}},
			{"SELECT json_objectagg('a':$1) AS v", "JSON", `{ "a" : null }`, []any{nil}},
			{"SELECT json_objectagg($1::text:$2) AS v", "JSON", `{ "a" : "2" }`, []any{"a", "2"}},
			{"SELECT json_objectagg($1::text:$2::int RETURNING jsonb) AS v", "JSONB", `{"a": 2}`, []any{"a", 2}},
		} {
			t.Run(tc.q+tc.want, func(t *testing.T) {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(tc.q, tc.typ, tc.want, tc.args...))
			})
		}
		q := "SELECT $1::int AS n,json_objectagg($1:$1) AS v"
		jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"n", "v"}, []string{"INT4", "JSON"}, [][]any{{int64(2), `{ "2" : 2 }`}}, 2))
		// A SQL/JSON unknown value is resolved to text before a later typed
		// occurrence; unlike the unknown ANY key, it has a concrete candidate.
		q = "/*é😀*/ SELECT json_objectagg('a':$1) AS v,$1::int AS n"
		jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"v", "n"}, []string{"JSON", "INT4"}, [][]any{{`{ "a" : "2" }`, int64(2)}}, "2"))
		for _, returning := range []string{"", " RETURNING jsonb", " RETURNING jsonb FORMAT JSON"} {
			t.Run("held/"+returning, func(t *testing.T) {
				q := "SELECT json_objectagg(k:v ABSENT ON NULL WITH UNIQUE" + returning + ") AS v FROM (SELECT * FROM (VALUES (1,$1::text,$2::int),(2,$3::text,$4::int)) t(id,k,v) ORDER BY id) s"
				stmt, err := b.DB.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				x := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
				typ, singleton, pair := "JSON", `{ "a" : 1 }`, `{ "a" : 1, "b" : 2 }`
				if returning != "" {
					typ, singleton, pair = "JSONB", `{"a": 1}`, `{"a": 1, "b": 2}`
				}
				for _, frame := range []struct {
					args []any
					want any
					bad  string
				}{
					{[]any{"a", 1, "b", 2}, pair, ""},
					{[]any{"a", 1, "b", nil}, singleton, ""},
					{[]any{"a", 1, "a", nil}, nil, "duplicate"},
					{[]any{"a", 1, nil, nil}, nil, "null"},
					{[]any{"a", 1, "b", 2}, pair, ""},
				} {
					if frame.bad != "" {
						binary := returning == " RETURNING jsonb"
						d := sqlJSONObjectAggregateDuplicate(binary, `"a"`)
						if frame.bad == "null" {
							family := "JSON"
							if binary {
								family = "JSONB"
							}
							d = jsonObjectAggregateNullKey(family)
						}
						for _, query := range []bool{true, false} {
							s := fixture.ErrorDiagnostic(q, d, frame.args...)
							s.SelectRows, s.ErrorStage = query, "execute"
							fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
						}
					} else {
						fixture.RunStep(t, p.Ctx, x, b.Recorder, jsonArrayAggregateSelect(q, typ, frame.want, frame.args...))
					}
				}
			})
		}
		t.Run("held-format", func(t *testing.T) {
			const q = "SELECT json_objectagg('a':$1::text FORMAT JSON) AS v"
			stmt, err := b.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x := fixture.HeldStatement{Executor: b.DB, Statement: stmt, SQL: q}
			fixture.RunStep(t, p.Ctx, x, b.Recorder, jsonArrayAggregateSelect(q, "JSON", `{ "a" : [1] }`, "[1]"))
			d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
			d.Detail, d.Context = `Token "no" is invalid.`, "JSON data, line 1: no"
			s := fixture.ErrorDiagnostic(q, d, "no")
			s.SelectRows, s.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			fixture.RunStep(t, p.Ctx, x, b.Recorder, jsonArrayAggregateSelect(q, "JSON", `{ "a" : [2] }`, "[2]"))
		})
	})
}

func TestReferenceSQLJSONObjectAggregateMetadata(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN sjo_meta_jsonb AS jsonb", 0))
		for _, tc := range []struct {
			clause, typ string
			width       int64
			value       any
		}{
			{"", "JSON", -1, `{ "a" : 1 }`}, {" RETURNING jsonb", "JSONB", -1, `{"a": 1}`},
			{" RETURNING jsonb FORMAT JSON", "JSONB", -1, `{"a": 1}`},
			{" RETURNING text", "TEXT", -1, `{ "a" : 1 }`}, {" RETURNING bytea", "BYTEA", -1, `{ "a" : 1 }`},
			{" RETURNING varchar(11)", "VARCHAR", 11, `{ "a" : 1 }`},
			{" RETURNING char(13)", "BPCHAR", 13, `{ "a" : 1 }  `},
			{" RETURNING sjo_meta_jsonb", "JSONB", -1, `{"a": 1}`},
		} {
			for _, mode := range []string{"populated", "global-empty", "grouped-empty", "filter-empty", "all-null", "all-omitted"} {
				t.Run(tc.clause+"/"+mode, func(t *testing.T) {
					pair, policy, suffix, filter := "k:n", "", "", ""
					want := [][]any{{tc.value}}
					switch mode {
					case "global-empty":
						suffix, want = " WHERE FALSE", [][]any{{nil}}
					case "grouped-empty":
						suffix, want = " WHERE FALSE GROUP BY k", nil
					case "filter-empty":
						filter, want = " FILTER(WHERE FALSE)", [][]any{{nil}}
					case "all-null":
						// Narrow RETURNING targets cannot fit this longer result;
						// length failures are independently covered in Returning.
						if tc.width >= 0 {
							return
						}
						pair = "k:NULL::int"
						value := `{ "a" : null }`
						if tc.typ == "JSONB" {
							value = `{"a": null}`
						}
						want = [][]any{{value}}
					case "all-omitted":
						pair, policy = "k:NULL::int", " ABSENT ON NULL"
						value := `{  }`
						if tc.typ == "JSONB" {
							value = `{}`
						}
						if tc.typ == "BPCHAR" {
							value += "         "
						}
						want = [][]any{{value}}
					}
					name := "v"
					expr := "json_objectagg(" + pair + policy + tc.clause + ")" + filter + " AS v"
					if tc.clause == "" && mode == "populated" {
						expr, name = "json_objectagg("+pair+")", "json_objectagg"
					}
					q := "SELECT " + expr + " FROM (VALUES ('a',1)) t(k,n)" + suffix
					s := fixture.Select(q, []string{name}, []string{tc.typ}, want)
					s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
						jsonArrayAggregateMetadata(t, rows, b.PG, s.Columns, s.Types, []int64{tc.width})
					}
					s.CheckScan = func(t *testing.T, rows *sql.Rows, rowIndex int) {
						var raw any
						if err := rows.Scan(&raw); err != nil {
							t.Fatal(err)
						}
						if want[rowIndex][0] == nil {
							if raw != nil {
								t.Fatalf("raw NULL=%#v", raw)
							}
							return
						}
						fixture.AssertRawValue(t, b.PG, tc.typ, raw)
						if !reflect.DeepEqual(fixture.Normalize(raw), want[rowIndex][0]) {
							t.Fatalf("raw=%#v want=%#v", raw, want[rowIndex][0])
						}
					}
					jsonArrayAggregateRoutes(t, p, b, s)
				})
			}
		}
	})
}
