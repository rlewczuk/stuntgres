package reference_test

import (
	"fmt"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func (c *mathPowCase) held(query string, steps []fixture.Step) {
	c.t.Helper()
	c.t.Run("held/"+query, func(t *testing.T) {
		stmt, err := c.conn.PrepareContext(c.p.Ctx, query)
		if err != nil {
			t.Fatalf("quiet prepare: %v", err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c.conn, Statement: stmt, SQL: query}
		for i, s := range steps {
			t.Run(fmt.Sprint(i), func(t *testing.T) {
				// Independently assert direct, fresh prepare, and retained prepare;
				// all three consume the same expected metadata/rows/diagnostic.
				mathLogPowerBoth(func(s fixture.Step) {
					fixture.RunStep(t, c.p.Ctx, c.conn, c.backend.Recorder, s)
				}, s)
				fixture.RunStep(t, c.p.Ctx, held, c.backend.Recorder, s)
			})
		}
	})
}

func mathPowHeldSteps(query, typ string, numeric bool) []fixture.Step {
	value := func(f float64, n string) any {
		if numeric {
			return n
		}
		return f
	}
	steps := []fixture.Step{
		fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{value(8, "8.0000000000000000")}}, "2", "3"),
		fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{value(9, "9.0000000000000000")}}, "3", "2"),
		fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{nil}}, nil, "3"),
		fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{nil}}, "2", nil),
	}
	failure := fixture.ImplicitError(query, fixture.Diagnostic{Code: "2201F", Message: "zero raised to a negative power is undefined"}, "0", "-1")
	failure.SelectRows, failure.ErrorStage = true, "execute"
	steps = append(steps, failure,
		fixture.Select(query, []string{"result"}, []string{typ}, [][]any{{value(8, "8.0000000000000000")}}, "2", "3"))
	return steps
}

func TestReferenceMathLogPowerPowPrepared(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		for _, tc := range []struct{ query, typ string }{
			{"SELECT pow($1::float8,$2::float8) AS result", "FLOAT8"},
			{"SELECT pow($1::numeric,$2::numeric) AS result", "NUMERIC"},
			{"SELECT pow($1,$2) AS result", "FLOAT8"},
		} {
			steps := mathPowHeldSteps(tc.query, tc.typ, tc.typ == "NUMERIC")
			if tc.typ == "NUMERIC" {
				steps = append(steps, fixture.Select(tc.query, []string{"result"}, []string{"NUMERIC"}, [][]any{{"81129638414606699710187514626049"}}, "9007199254740993", "2"))
			}
			c.held(tc.query, steps)
		}
		division := "SELECT 10 / pow($1::float8,$2::float8) AS result"
		c.held(division, []fixture.Step{
			fixture.Select(division, []string{"result"}, []string{"FLOAT8"}, [][]any{{float64(1.25)}}, "2", "3"),
			fixture.Select(division, []string{"result"}, []string{"FLOAT8"}, [][]any{{float64(2.5)}}, "2", "2"),
		})
		ordered := "SELECT x FROM (VALUES (1),(2)) v(x) ORDER BY x / pow(x + $1::numeric, $2::numeric) DESC"
		c.held(ordered, []fixture.Step{
			fixture.Select(ordered, []string{"x"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}, "2", "1.4"),
			fixture.Select(ordered, []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}, "2", "3"),
		})
		fider := "SELECT 1 / pow((EXTRACT(EPOCH FROM current_timestamp - (current_timestamp - $1::interval))/3600) + 2, $2::numeric) > 0 AS f"
		c.held(fider, []fixture.Step{
			fixture.Select(fider, []string{"f"}, []string{"BOOL"}, [][]any{{true}}, "1 hour", "1.4"),
			fixture.Select(fider, []string{"f"}, []string{"BOOL"}, [][]any{{true}}, "2 hours", "2"),
		})
	})
}

func TestReferenceMathLogPowerPowConsumers(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		c.run(fixture.Exec("CREATE TABLE pow_stored (id integer, f float8 DEFAULT pow(2::float8,3::float8), n numeric DEFAULT pow(2::numeric,3::numeric))", 0))
		c.run(fixture.Exec("INSERT INTO pow_stored(id) VALUES (1)", 1))
		c.run(fixture.Prepared(fixture.Exec("INSERT INTO pow_stored(id) VALUES (2)", 1)))
		c.run(fixture.Exec("CREATE VIEW pow_view AS SELECT id,f,n,10 / pow(f,1::float8) AS quotient,pow(n,1::numeric) FROM pow_stored", 0))
		c.both(fixture.Select("SELECT * FROM pow_view ORDER BY id", []string{"id", "f", "n", "quotient", "pow"}, []string{"INT4", "FLOAT8", "NUMERIC", "FLOAT8", "NUMERIC"}, [][]any{
			{int64(1), float64(8), "8.0000000000000000", float64(1.25), "8.0000000000000000"},
			{int64(2), float64(8), "8.0000000000000000", float64(1.25), "8.0000000000000000"},
		}))
		c.t.Run("retained-cursor-metadata", func(t *testing.T) {
			stmt, err := c.conn.PrepareContext(c.p.Ctx, "SELECT pow(f,1::float8) FROM pow_stored ORDER BY id")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			rows, err := stmt.QueryContext(c.p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer rows.Close()
			check := func() {
				t.Helper()
				names, err := rows.Columns()
				if err != nil || !reflect.DeepEqual(names, []string{"pow"}) {
					t.Fatalf("retained names=%v error=%v", names, err)
				}
				mathLogPowerMetadata(t, rows, c.backend.PG)
				types, err := rows.ColumnTypes()
				if err != nil || len(types) != 1 || types[0].DatabaseTypeName() != "FLOAT8" {
					t.Fatalf("retained types=%v error=%v", types, err)
				}
			}
			check()
			for i := range 2 {
				if !rows.Next() {
					t.Fatalf("missing row %d: %v", i, rows.Err())
				}
				var raw any
				if err := rows.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				fixture.AssertRawValue(t, c.backend.PG, "FLOAT8", raw)
				if raw != float64(8) {
					t.Fatalf("retained result=%v want 8", raw)
				}
				check()
			}
			if rows.Next() || rows.Err() != nil {
				t.Fatalf("retained cursor end: %v", rows.Err())
			}
			if err := stmt.Close(); err != nil {
				t.Fatal(err)
			}
		})
	})
}

func TestReferenceMathLogPowerPowFeatureSmoke(t *testing.T) {
	mathPowScenario(t, func(c *mathPowCase) {
		c.both(fixture.Select("SELECT pow(2,3) AS scalar,10 / pow(2,3) AS quotient,pg_catalog.pow(2::float8,3::float8) AS qualified", []string{"scalar", "quotient", "qualified"}, []string{"FLOAT8", "FLOAT8", "FLOAT8"}, [][]any{{float64(8), float64(1.25), float64(8)}}))
		c.both(fixture.Select(mathPowOrder, []string{"x"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(1)}}))
		c.both(fixture.Select(mathPowFider, []string{"f"}, []string{"BOOL"}, [][]any{{true}}))
		query := "SELECT pow($1::float8,$2::float8) AS result"
		c.held(query, mathPowHeldSteps(query, "FLOAT8", false))
		c.catalog(fixture.Select("SELECT p.oid,p.prosrc,d.description FROM pg_proc p JOIN pg_description d ON d.objoid=p.oid AND d.classoid=1255 AND d.objsubid=0 WHERE p.proname='pow' ORDER BY p.oid", []string{"oid", "prosrc", "description"}, []string{"OID", "TEXT", "TEXT"}, [][]any{{int64(1346), "dpow", "exponentiation"}, {int64(1738), "numeric_power", "exponentiation"}}))
	})
}
