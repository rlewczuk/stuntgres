package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTablePassing(t *testing.T) {
	t.Run("typed scopes", func(t *testing.T) {
		fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'UTC'`, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET DateStyle TO 'ISO, YMD'`, 0))
			for _, x := range []int{10, 3} {
				rows := [][]any{{int64(1), `1`}, {int64(2), `2`}, {int64(3), nil}}
				if x == 3 {
					rows = rows[:2]
				}
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT * FROM JSON_TABLE('[1,2,3]','$[*]?(@ < $x)' PASSING $1::int AS x,3 AS y COLUMNS(r FOR ORDINALITY,a text FORMAT JSON PATH '$?(@ < $y)')) ORDER BY r`, []string{"r", "a"}, []string{"INT4", "TEXT"}, rows, x))
			}
			for _, tc := range []struct {
				binding string
				want    any
			}{{`2::int`, true}, {`'2'::text`, nil}, {`NULL::int`, false}, {`'2'::json`, true}, {`'2'::jsonb`, true}} {
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT e FROM JSON_TABLE('2','$' PASSING `+tc.binding+` AS x COLUMNS(e boolean PATH '$ == $x'))`, []string{"e"}, []string{"BOOL"}, [][]any{{tc.want}}))
			}
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT v FROM JSON_TABLE('null','$' PASSING NULL::int AS x COLUMNS(v boolean PATH '$ == $x'))`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT * FROM JSON_TABLE('{"a":[1,2,3]}','$' PASSING 3 AS x COLUMNS(NESTED '$.a[*]?(@ < $x)' COLUMNS(v int PATH '$'))) ORDER BY v`, []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			for _, tc := range []struct{ value, want string }{{`DATE '2018-02-21'`, `2018-02-21`}, {`TIME '12:34:56'`, `12:34:56`}, {`TIMETZ '12:34:56+03'`, `12:34:56+03`}, {`TIMESTAMP '2018-02-21 12:34:56'`, `2018-02-21 12:34:56`}, {`TIMESTAMPTZ '2018-02-21 12:34:56+00'`, `2018-02-21 12:34:56+00`}} {
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT v FROM JSON_TABLE('{}','$' PASSING `+tc.value+` AS x COLUMNS(v text PATH '$x'))`, []string{"v"}, []string{"TEXT"}, [][]any{{tc.want}}))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`SET TIME ZONE 'Asia/Tokyo'`, 0))
			fixture.SQLJSONBoth(t, p, b, c, fixture.Select(`SELECT v FROM JSON_TABLE('{}','$' PASSING TIMESTAMPTZ '2018-02-21 12:34:56+00' AS x COLUMNS(v text PATH '$x'))`, []string{"v"}, []string{"TEXT"}, [][]any{{`2018-02-21 21:34:56+09`}}))
		})
	})
	// Each independent sequence case resets to one, so failures cannot hide the
	// other initialization, duplicate-binding or NULL-extension observations.
	for _, tc := range []struct {
		name, q     string
		args        []any
		cols, types []string
		rows        [][]any
		last        int64
	}{
		{"column reevaluation", `SELECT * FROM JSON_TABLE($1::jsonb,'$[*]' PASSING nextval('jt_pass_seq') AS p COLUMNS(r FOR ORDINALITY,a int PATH '$p',b int PATH '$p')) ORDER BY r`, []any{`[10,20]`}, []string{"r", "a", "b"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(3)}, {int64(2), int64(4), int64(5)}}, 5},
		{"null document", `SELECT * FROM JSON_TABLE($1::jsonb,'$[*]' PASSING nextval('jt_pass_seq') AS p COLUMNS(r FOR ORDINALITY,a int PATH '$p'))`, []any{nil}, []string{"r", "a"}, []string{"INT4", "INT4"}, nil, 1},
		{"passing before document", `SELECT * FROM JSON_TABLE(jsonb_build_array(nextval('jt_pass_seq')),'$[*]' PASSING nextval('jt_pass_seq') AS p COLUMNS(r FOR ORDINALITY,doc int PATH '$',a int PATH '$p'))`, nil, []string{"r", "doc", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(4)}}, 4},
		{"nested initial binding", `SELECT * FROM JSON_TABLE($1::jsonb,'$' PASSING nextval('jt_pass_seq') AS p COLUMNS(NESTED '$.a[*]?(@ == $p)' COLUMNS(o FOR ORDINALITY,v int PATH '$',p int PATH '$p')))`, []any{`{"a":[1,2]}`}, []string{"o", "v", "p"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(1), int64(3)}}, 3},
		{"missing child", `SELECT * FROM JSON_TABLE($1::jsonb,'$' PASSING nextval('jt_pass_seq') AS p COLUMNS(r FOR ORDINALITY,NESTED '$.absent[*]' COLUMNS(o FOR ORDINALITY,a int PATH '$p')))`, []any{`{}`}, []string{"r", "o", "a"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), nil, nil}}, 1},
		{"duplicates unused ordered", `SELECT * FROM JSON_TABLE($1::jsonb,'$' PASSING nextval('jt_pass_seq') AS p,nextval('jt_pass_seq') AS p,nextval('jt_pass_seq') AS unused COLUMNS(a int PATH '$p'))`, []any{`{}`}, []string{"a"}, []string{"INT4"}, [][]any{{int64(4)}}, 6},
		{"parent defaults repeat", `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(d bigint PATH '$.absent' DEFAULT nextval('jt_pass_seq') ON EMPTY,NESTED '$.a[*]' COLUMNS(v int PATH '$'))) ORDER BY v`, []any{`{"a":[10,20]}`}, []string{"d", "v"}, []string{"INT8", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), int64(20)}}, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE SEQUENCE jt_pass_seq START 1`, 0))
				// Pinned PostgreSQL18.6 oracle round1 observed these exact rows/counts.
				// Keep currval in a separate subtest to expose sequence state after row failure.
				t.Run("rows", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select(tc.q, tc.cols, tc.types, tc.rows, tc.args...))
				})
				t.Run("sequence", func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.SQLJSONQueryOne(`SELECT currval('jt_pass_seq') AS v`, "INT8", tc.last))
				})
			})
		})
	}
}
