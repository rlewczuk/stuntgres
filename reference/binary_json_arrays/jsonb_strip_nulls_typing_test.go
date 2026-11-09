package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBStripNullsTyping(t *testing.T) {
	jsonbStripTypingVariadic(t)
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{"target => $1", jsonbStripDefault}, {"$1,strip_in_arrays => true", jsonbStripArrays},
			{"strip_in_arrays => true,target => $1", jsonbStripArrays}, {"target := $1,strip_in_arrays := false", jsonbStripDefault},
			{"strip_in_arrays := true,target := $1", jsonbStripArrays},
		} {
			jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls("+tc.args+") AS stripped", tc.want, jsonbStripInput))
		}
		for _, name := range []string{"JsOnB_StRiP_NuLlS", "pg_catalog.jsonb_strip_nulls", `"jsonb_strip_nulls"`} {
			jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT "+name+"($1,true) AS stripped", jsonbStripArrays, jsonbStripInput))
		}
		jsonTypeBoth(t, p, b, c, fixture.Select("SELECT jsonb_strip_nulls($1) AS a,jsonb_strip_nulls($1,true) AS b", []string{"a", "b"}, []string{"JSONB", "JSONB"}, [][]any{{jsonbStripDefault, jsonbStripArrays}}, jsonbStripInput))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jbs_bool AS bool", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE DOMAIN jbs_doc AS jsonb", 0))
		jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls($1::jbs_doc,true::jbs_bool) AS stripped", jsonbStripArrays, jsonbStripInput))
	})
}

func jsonbStripTypingVariadic(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct{ args, want string }{
			{jsonTypeQuote(jsonbStripInput) + "::jsonb,VARIADIC true", jsonbStripArrays},
			{"VARIADIC " + jsonTypeQuote(jsonbStripInput) + "::jsonb", jsonbStripDefault},
			{"target => " + jsonTypeQuote(jsonbStripInput) + "::jsonb,VARIADIC strip_in_arrays => true", jsonbStripArrays},
		} {
			t.Run(tc.args, func(t *testing.T) {
				jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls("+tc.args+") AS stripped", tc.want))
			})
		}
		t.Run("reordered_named_variadic", func(t *testing.T) {
			q := "SELECT jsonb_strip_nulls(strip_in_arrays => true,VARIADIC target => '{}'::jsonb)"
			jsonTypeAnalysisError(t, p, b, c, jsonTypeSignature(q, "jsonb_strip_nulls(strip_in_arrays => boolean, target => jsonb)", 8))
		})
	})
}

func TestReferenceJSONBStripNullsHeld(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbStripHeldOwnership(t, p, b, c)
		jsonbStripConcurrentHeld(t, p, b)
		for _, q := range []string{"SELECT jsonb_strip_nulls($1,$2) AS stripped", "SELECT jsonb_strip_nulls(strip_in_arrays => $2,target => $1) AS stripped"} {
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			func() {
				defer stmt.Close()
				held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
				for _, tc := range []struct{ input, flag, want any }{
					{jsonbStripInput, true, jsonbStripArrays}, {[]byte(jsonbStripInput), false, jsonbStripDefault}, {nil, true, nil}, {[]byte(nil), false, nil}, {jsonbStripInput, nil, nil}, {`null`, true, `null`},
				} {
					jsonTypeRun(t, p, b, held, jsonbStripMetadata(jsonbStripSelect(q, tc.want, tc.input, tc.flag), b.PG))
				}
				s := jsonTypeError(q, "22P02", "invalid input syntax for type json", 0, "oops", true)
				s.Diagnostic.Detail = `Token "oops" is invalid.`
				s.Diagnostic.Context = "JSON data, line 1: oops\nunnamed portal parameter $1 = '...'"
				jsonTypeRun(t, p, b, held, s)
				jsonTypeRun(t, p, b, held, jsonbStripSelect(q, jsonbStripArrays, jsonbStripInput, true))
				for _, target := range []any{jsonbStripInput, nil} {
					badFlag := jsonTypeError(q, "22P02", `invalid input syntax for type boolean: "truth"`, 0, target, "truth")
					badFlag.Diagnostic.Context = "unnamed portal parameter $2 = '...'"
					jsonTypeRun(t, p, b, held, badFlag)
					for _, recovery := range []struct{ flag, want any }{{true, jsonbStripArrays}, {false, jsonbStripDefault}, {nil, nil}} {
						jsonTypeRun(t, p, b, held, jsonbStripMetadata(jsonbStripSelect(q, recovery.want, jsonbStripInput, recovery.flag), b.PG))
					}
				}
				s.Args = []any{"oops", nil}
				jsonTypeRun(t, p, b, held, s)
				jsonTypeRun(t, p, b, held, jsonbStripSelect(q, jsonbStripArrays, jsonbStripInput, true))
			}()
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_bytes(v jsonb)", 0))
		input := []byte(jsonbStripInput)
		jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbs_bytes VALUES($1)", 1, input))
		input[2] = 'z'
		jsonTypeBoth(t, p, b, c, jsonbStripSelect("SELECT jsonb_strip_nulls(v) AS stripped FROM jbs_bytes", jsonbStripDefault))
	})
}

func jsonbStripMetadata(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
		columns, err := r.Columns()
		if err != nil || !reflect.DeepEqual(columns, s.Columns) {
			t.Fatal(columns, err)
		}
		types, err := r.ColumnTypes()
		if err != nil || len(types) != 1 {
			t.Fatal(types, err)
		}
		scan := reflect.TypeFor[string]()
		if pg {
			scan = reflect.TypeFor[any]()
		}
		if types[0].Name() != s.Columns[0] || types[0].DatabaseTypeName() != "JSONB" || types[0].ScanType() != scan {
			t.Fatal(types[0])
		}
		if _, known := types[0].Length(); known {
			t.Fatal("unexpected JSON length")
		}
		if _, _, known := types[0].DecimalSize(); known {
			t.Fatal("unexpected JSON precision")
		}
		if nullable, known := types[0].Nullable(); nullable || known {
			t.Fatal("computed JSON nullability must be unknown")
		}
	}
	s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
		var raw any
		if err := r.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, pg, "JSONB", raw)
		if !reflect.DeepEqual(fixture.Normalize(raw), s.Rows[i][0]) {
			t.Fatalf("raw=%#v want=%#v", raw, s.Rows[i][0])
		}
		var n sql.NullString
		if err := r.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n.Valid != (s.Rows[i][0] != nil) || n.Valid && n.String != s.Rows[i][0] {
			t.Fatalf("NullString=%#v want=%#v", n, s.Rows[i][0])
		}
		var data []byte
		if err := r.Scan(&data); err != nil {
			t.Fatal(err)
		}
		if s.Rows[i][0] == nil {
			if data != nil {
				t.Fatal(data)
			}
			return
		}
		if string(data) != s.Rows[i][0] {
			t.Fatal(string(data))
		}
		data[0] = 'x'
		var again string
		if err := r.Scan(&again); err != nil || again != s.Rows[i][0] {
			t.Fatal(again, err)
		}
	}
	return s
}

func TestReferenceJSONBStripNullsMetadata(t *testing.T) {
	jsonbStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbs_meta(id int,v jsonb)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO jbs_meta VALUES(1,'null'),(2,NULL)`, 2))
		for _, tail := range []string{" ORDER BY id", " WHERE id=2", " WHERE false"} {
			expected := [][]any{{"null"}, {nil}}
			if tail == " WHERE id=2" {
				expected = [][]any{{nil}}
			} else if tail == " WHERE false" {
				expected = nil
			}
			s := fixture.Select("SELECT jsonb_strip_nulls(v) FROM jbs_meta"+tail, []string{"jsonb_strip_nulls"}, []string{"JSONB"}, expected)
			jsonTypeBoth(t, p, b, c, jsonbStripMetadata(s, b.PG))
		}
		for _, tc := range []struct {
			tail string
			rows [][]any
		}{{" ORDER BY id", [][]any{{"null"}, {nil}}}, {" WHERE id=2", [][]any{{nil}}}, {" WHERE false", nil}} {
			s := jsonbStripMetadata(fixture.Select("SELECT jsonb_strip_nulls(v) AS stripped FROM jbs_meta"+tc.tail, []string{"stripped"}, []string{"JSONB"}, tc.rows), b.PG)
			jsonTypeBoth(t, p, b, c, s)
			stmt, err := c.PrepareContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			jsonTypeRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: s.Query}, s)
			stmt.Close()
		}
	})
}

func jsonbStripHeldOwnership(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	const q = "SELECT jsonb_strip_nulls(strip_in_arrays => $2,target => $1) AS stripped"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	input := []byte(jsonbStripInput)
	rows, err := stmt.QueryContext(p.Ctx, input, true)
	if err != nil {
		t.Fatal(err)
	}
	defer rows.Close()
	jsonbStripMetadata(jsonbStripSelect(q, jsonbStripArrays), b.PG).CheckMetadata(t, rows)
	types, err := rows.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	for i := range input {
		input[i] = 'x'
	}
	if !rows.Next() {
		t.Fatal("missing owned result", rows.Err())
	}
	var text string
	var data []byte
	if err := rows.Scan(&text); err != nil {
		t.Fatal(err)
	}
	if err := rows.Scan(&data); err != nil {
		t.Fatal(err)
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatal("unexpected owned rows", rows.Err())
	}
	rows.Close()
	if text != jsonbStripArrays || string(data) != jsonbStripArrays {
		t.Fatalf("detached values %q/%q", text, data)
	}
	data[0] = 'x'
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	jsonTypeRun(t, p, b, held, jsonbStripSelect(q, jsonbStripDefault, jsonbStripInput, false))
	if types[0].Name() != "stripped" || types[0].DatabaseTypeName() != "JSONB" {
		t.Fatal("retained metadata changed", types)
	}
}

func jsonbStripConcurrentHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	stmt, err := b.DB.PrepareContext(p.Ctx, "SELECT jsonb_strip_nulls($1,$2)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	type outcome struct {
		value string
		want  string
		err   error
	}
	done := make(chan outcome, 8)
	for i := range cap(done) {
		flag := i%2 == 0
		want := jsonbStripDefault
		if flag {
			want = jsonbStripArrays
		}
		go func() {
			var value string
			err := stmt.QueryRowContext(p.Ctx, jsonbStripInput, flag).Scan(&value)
			done <- outcome{value: value, want: want, err: err}
		}()
	}
	// Join every worker before reporting failures or fixture teardown.
	results := make([]outcome, cap(done))
	for i := range results {
		results[i] = <-done
	}
	for _, result := range results {
		if result.err != nil || result.value != result.want {
			t.Errorf("concurrent held result=%q want=%q error=%v", result.value, result.want, result.err)
		}
	}
}
