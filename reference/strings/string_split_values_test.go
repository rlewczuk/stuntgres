package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

// Expected values are independent PostgreSQL 18 release/manual expectations.
// Each SQL body uses the established strings scenario, recorder and both routes.
func TestReferenceStringSplitScalar(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			args string
			want any
		}{
			{"'a,b,c,d', ',', 2", "b"}, {"'a,b,c,d', ',', -2", "c"},
			{"'a,b', ',', 3", ""}, {"'a,b', ',', -3", ""}, {"'a,b', ',', -2147483648", ""},
			{"'abc', '', 1", "abc"}, {"'abc', '', -1", "abc"}, {"'abc', '', 2", ""}, {"'abc', '', -2", ""},
			{"'abc', 'absent', -1", "abc"}, {"'', ',', 7", ""}, {"'', '', -1", ""},
			{"NULL, ',', 1", nil}, {"'abc', NULL, 1", nil}, {"'abc', ',', NULL", nil}, {"NULL, ',', 0", nil},
			{"NULL::text, ',', 0", nil}, {"'abc', NULL::text, 0", nil}, {"'abc', ',', NULL::int", nil},
			{"',x,,x,', ',', 1", ""}, {"',x,,x,', ',', 3", ""}, {"',x,,x,', ',', -2", "x"}, {"',x,,x,', ',', -1", ""},
			{"'aaa', 'aa', 2", "a"}, {"'é🙂é', 'é', 2", "🙂"}, {"'a,b  '::char(6), ',', -1", "b"},
			{"'a,b'::varchar(8), ','::name, 2::smallint", "b"},
		} {
			t.Run(tc.args, func(t *testing.T) { stringReplaceValue(t, p, b, c, "split_part("+tc.args+")", "TEXT", tc.want) })
		}
		stringReplaceValue(t, p, b, c, `pg_catalog.split_part('a,b', ',', -1)`, "TEXT", "b")
		stringReplaceValue(t, p, b, c, `"split_part"('a,b', ',', 1)`, "TEXT", "a")
	})
}

type stringSplitVector struct {
	args   string
	array  any
	fields []any
}

func stringSplitVectors() []stringSplitVector {
	return []stringSplitVector{
		{"'x,x,,', ','", `{x,x,"",""}`, []any{"x", "x", "", ""}},
		{"'x,x,,', ',', ''", `{x,x,NULL,NULL}`, []any{"x", "x", nil, nil}},
		{"'NULL,x,NULL', ','", `{"NULL",x,"NULL"}`, []any{"NULL", "x", "NULL"}},
		{"'NULL,x,NULL', ',', 'NULL'", `{NULL,x,NULL}`, []any{nil, "x", nil}},
		{"'1||2|3||', '||'", `{1,2|3,""}`, []any{"1", "2|3", ""}},
		{"'aaa', 'aa'", `{"",a}`, []any{"", "a"}},
		{"'abc', ''", `{abc}`, []any{"abc"}}, {"'abc', '', 'abc'", `{NULL}`, []any{nil}},
		{"'abc', '', NULL", `{abc}`, []any{"abc"}},
		{"'é🙂é', NULL", `{é,🙂,e,́}`, []any{"é", "🙂", "e", "\u0301"}},
		{"'é🙂é', NULL, 'é'", `{NULL,🙂,NULL}`, []any{nil, "🙂", nil}},
		{"'', ','", `{}`, nil}, {"'', ''", `{}`, nil}, {"'', NULL", `{}`, nil},
		{"'', '', ''", `{}`, nil}, {"'', NULL, ''", `{}`, nil}, {"'', ',', ''", `{}`, nil},
		{"NULL, ','", nil, nil}, {"NULL, NULL", nil, nil}, {"NULL, '', ''", nil, nil},
		{"'~~', '~~'", `{"",""}`, []any{"", ""}}, {"'x', 'long'", `{x}`, []any{"x"}},
		{`E'a"|{b}|c,d|e\\f', '|'`, `{"a\"","{b}","c,d","e\\f"}`, []any{`a"`, `{b}`, `c,d`, `e\f`}},
	}
}

func TestReferenceStringSplitArrays(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range stringSplitVectors() {
			t.Run(tc.args, func(t *testing.T) { stringReplaceValue(t, p, b, c, "string_to_array("+tc.args+")", "_TEXT", tc.array) })
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE ss_arrays(id int,v text[])", 0),
			fixture.Exec("INSERT INTO ss_arrays VALUES(1,string_to_array('x,x,,',',','')),(2,string_to_array('',NULL)),(3,string_to_array(NULL,',')),(4,string_to_array('NULL,NULL',',','NULL'))", 4),
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT id,v FROM ss_arrays ORDER BY id", []string{"id", "v"}, []string{"INT4", "_TEXT"}, [][]any{{int64(1), `{x,x,NULL,NULL}`}, {int64(2), `{}`}, {int64(3), nil}, {int64(4), `{NULL,NULL}`}}))
	})
}

func TestReferenceStringSplitFramesAndMetadata(t *testing.T) {
	stringReplaceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ss_rows_identifier(a int,rows text)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ss_rows_identifier VALUES(7,'kept')", 1))
		for _, q := range []string{"SELECT rows FROM ss_rows_identifier", "SELECT rows FROM (VALUES(7,'kept')) s(a,rows)"} {
			stringReplaceBoth(t, p, b, c, fixture.Select(q, []string{"rows"}, []string{"TEXT"}, [][]any{{"kept"}}))
		}
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT a,rows FROM (VALUES(7,'kept')) s(a,rows)", []string{"a", "rows"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "kept"}}))
		q := "SELECT split_part($1,$2,$3),string_to_array($1,$2,$4)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for _, tc := range []struct {
			args []any
			row  []any
		}{
			{[]any{"a,b,c", ",", -1, nil}, []any{"c", `{a,b,c}`}},
			{[]any{"x,x,,", ",", 2, ""}, []any{"x", `{x,x,NULL,NULL}`}},
			{[]any{"é🙂", nil, 1, nil}, []any{nil, `{é,🙂}`}},
			{[]any{nil, ",", 0, ""}, []any{nil, nil}},
			{[]any{"a,b,c", ",", -1, nil}, []any{"c", `{a,b,c}`}},
		} {
			s := fixture.Select(q, []string{"split_part", "string_to_array"}, []string{"TEXT", "_TEXT"}, [][]any{tc.row}, tc.args...)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				ct, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for i, col := range ct {
					wantScan := reflect.TypeFor[string]()
					if b.PG && i == 1 {
						wantScan = reflect.TypeFor[any]()
					}
					if col.ScanType() != wantScan {
						t.Fatalf("ScanType %v want %v", col.ScanType(), wantScan)
					}
					if n, k := col.Nullable(); n || k {
						t.Fatalf("computed nullable %v/%v", n, k)
					}
					if _, _, k := col.DecimalSize(); k {
						t.Fatal("computed decimal size")
					}
				}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN ss_text AS text", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE DOMAIN ss_char AS char(6)", 0))
		stringReplaceValue(t, p, b, c, "split_part('a,b  '::ss_char,','::ss_text,-1)", "TEXT", "b")
		stringReplaceValue(t, p, b, c, "string_to_array('a,b'::ss_text,','::ss_text)", "_TEXT", `{a,b}`)
		stringReplaceBoth(t, p, b, c, fixture.Select("SELECT t.t,t.ordinality FROM string_to_table('',NULL) WITH ORDINALITY AS t", []string{"t", "ordinality"}, []string{"TEXT", "INT8"}, nil))
		q = "SELECT string_to_table($1,$2,$3) AS v"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		for _, tc := range []struct {
			args []any
			rows [][]any
		}{
			{[]any{"a,a", ",", nil}, [][]any{{"a"}, {"a"}}}, {[]any{"", ",", ""}, nil}, {[]any{nil, nil, nil}, nil}, {[]any{"x,,", ",", ""}, [][]any{{"x"}, {nil}, {nil}}},
		} {
			s := fixture.Select(q, []string{"v"}, []string{"TEXT"}, tc.rows, tc.args...)
			s.Bag = true
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: st, SQL: q}, b.Recorder, s)
		}
	})
}
