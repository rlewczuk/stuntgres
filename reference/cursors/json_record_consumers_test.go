package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordConsumers(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec(`CREATE TABLE jr_physical(a int NOT NULL CHECK(a>0),b text DEFAULT 'table-default')`, 0))
		// A row type does not inherit physical table constraints or defaults.
		jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_populate_record(NULL::jr_physical,'{}')`, [][]any{{nil, nil}}))
		jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT * FROM json_populate_record(NULL::jr_physical,'{"a":-1}')`, [][]any{{int64(-1), nil}}))
		run(fixture.Exec(`CREATE TABLE jr_saved(id int,j json)`, 0))
		run(fixture.Exec(`INSERT INTO jr_saved VALUES(1,'{"a":2,"b":"x"}'),(2,'{"a":3}')`, 2))
		run(fixture.Exec(`CREATE VIEW jr_typed_view AS SELECT s.id,r.a,r.b FROM jr_saved s,json_to_record(s.j) AS r(a int,b text)`, 0))
		run(fixture.Exec(`CREATE VIEW jr_named_view AS SELECT id,json_populate_record(NULL::jr_pair,j) AS r FROM jr_saved`, 0))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT id,a,b FROM jr_typed_view ORDER BY id`, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "x"}, {int64(2), int64(3), nil}}))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT id,(r).a AS a,(r).b AS b FROM jr_named_view ORDER BY id`, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "x"}, {int64(2), int64(3), nil}}))
		run(fixture.Exec(`CREATE TABLE jr_ctas AS SELECT * FROM json_to_recordset('[{"a":2},{"b":"x"}]') AS r(a int,b text)`, 2))
		jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT a,b FROM jr_ctas ORDER BY a NULLS LAST`, [][]any{{int64(2), nil}, {nil, "x"}}))
		run(fixture.Exec(`CREATE TABLE jr_destination(a int,b text)`, 0))
		run(fixture.Exec(`INSERT INTO jr_destination VALUES(99,'sentinel')`, 1))
		for _, prepared := range []bool{false, true} {
			q := `INSERT INTO jr_destination SELECT * FROM json_to_recordset($1::json) AS r(a int,b text)`
			args := []any{`[{"a":1,"b":"early"},{"a":"bad"}]`}
			var result sql.Result
			var err error
			if prepared {
				stmt, e := c.PrepareContext(p.Ctx, q)
				if e != nil {
					t.Fatal(e)
				}
				result, err = stmt.ExecContext(p.Ctx, args...)
				stmt.Close()
			} else {
				result, err = c.ExecContext(p.Ctx, q, args...)
			}
			if err == nil || result != nil {
				t.Errorf("late bad row published result=%v error=%v", result, err)
			}
			if err != nil {
				want := jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0)
				fixture.AssertDiagnostic(t, err, &want)
			}
			jsonRecordBoth(t, p, b, c, jsonRecordPair(`SELECT a,b FROM jr_destination`, [][]any{{int64(99), "sentinel"}}))
		}
		jsonRecordErrorBoth(t, p, b, c, `CREATE TABLE jr_bad_ctas AS SELECT * FROM json_to_recordset('[{"a":1},{"a":"bad"}]') AS r(a int)`, jsonRecordDiagnostic("22P02", `invalid input syntax for type integer: "bad"`, 0), false)
		jsonRecordErrorBoth(t, p, b, c, `SELECT * FROM jr_bad_ctas`, jsonRecordDiagnostic("42P01", `relation "jr_bad_ctas" does not exist`, 15), true)
		run(fixture.Exec(`CREATE TABLE jr_default(r jr_pair DEFAULT json_populate_record(NULL::jr_pair,'{"a":2}'))`, 0))
		run(fixture.Exec(`INSERT INTO jr_default DEFAULT VALUES`, 1))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT r::text AS r FROM jr_default`, []string{"r"}, []string{"TEXT"}, [][]any{{"(2,)"}}))
		run(fixture.Exec(`CREATE TABLE jr_check(j json CHECK ((json_populate_record(NULL::jr_pair,j)).a > 0))`, 0))
		run(fixture.Exec(`INSERT INTO jr_check VALUES('{"a":2}')`, 1))
		check := jsonRecordDiagnostic("23514", `new row for relation "jr_check" violates check constraint "jr_check_j_check"`, 0)
		check.Detail = `Failing row contains ({"a":-1}).`
		check.Schema, check.Table, check.Constraint = "public", "jr_check", "jr_check_j_check"
		coalesce := jsonRecordDiagnostic("0A000", "set-returning functions are not allowed in COALESCE", 17)
		coalesce.Hint = "You might be able to move the set-returning function into a LATERAL FROM item."
		for _, tc := range []struct {
			q        string
			want     fixture.Diagnostic
			analysis bool
		}{
			{`INSERT INTO jr_check VALUES('{"a":-1}')`, check, false},
			{`CREATE TABLE jr_generated(j json,a int GENERATED ALWAYS AS ((json_populate_record(NULL::jr_pair,j)).a) STORED)`, jsonRecordDiagnostic("42P17", "generation expression is not immutable", 0), false},
			{`CREATE INDEX jr_stable_index ON jr_saved (((json_populate_record(NULL::jr_pair,j)).a))`, jsonRecordDiagnostic("42P17", "functions in index expression must be marked IMMUTABLE", 0), false},
			{`SELECT coalesce(json_populate_recordset(NULL::jr_pair,'[]'),NULL::jr_pair)`, coalesce, true},
		} {
			t.Run(tc.q, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, tc.analysis) })
		}
		run(fixture.Exec(`ALTER TYPE jr_pair RENAME TO jr_pair_renamed`, 0))
		jsonRecordBoth(t, p, b, c, jsonRecordMetadata(fixture.Select(`SELECT json_populate_record(NULL::jr_pair_renamed,'{"a":2}') AS r`,
			[]string{"r"}, []string{""}, [][]any{{"(2,)"}}), b.PG))
		jsonRecordBoth(t, p, b, c, fixture.Select(`SELECT id,(r).a AS a,(r).b AS b FROM jr_named_view ORDER BY id`, []string{"id", "a", "b"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "x"}, {int64(2), int64(3), nil}}))
	})
}
