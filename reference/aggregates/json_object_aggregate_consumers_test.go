package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectAggregateConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, q := range []string{
		"WITH a AS(SELECT jsonb_object_agg_strict(k,v ORDER BY id) AS v FROM joa_input) SELECT v FROM a",
		"SELECT v FROM(SELECT jsonb_object_agg_strict(k,v ORDER BY id) AS v FROM joa_input) AS a",
		"SELECT jsonb_object_agg_strict(k,v ORDER BY id) AS v FROM joa_input HAVING count(*)=3",
	} {
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSONB", `{"a": 2, "b": 1}`))
	}
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE joa_destination(v jsonb)", 0),
		fixture.Exec("INSERT INTO joa_destination SELECT jsonb_object_agg_strict(k,v ORDER BY id) FROM joa_input", 1),
		fixture.Exec("INSERT INTO joa_destination SELECT jsonb_object_agg(k,v) FROM joa_input WHERE FALSE", 1),
		fixture.Exec("INSERT INTO joa_destination SELECT jsonb_object_agg(k,v) FROM joa_input WHERE FALSE GROUP BY g", 0),
		fixture.Exec("CREATE TABLE joa_ctas AS SELECT json_object_agg(k,v ORDER BY id) AS v FROM joa_input", 1),
		fixture.Exec("CREATE MATERIALIZED VIEW joa_materialized AS SELECT jsonb_object_agg_strict(k,v ORDER BY id) AS v FROM joa_input", 1),
		fixture.Exec("CREATE VIEW joa_grouped AS SELECT g,json_object_agg(k,v ORDER BY id) AS j,jsonb_object_agg_strict(k,v ORDER BY id) AS b FROM joa_input GROUP BY g", 0),
		fixture.Exec("CREATE TABLE joa_bad(id int,g int,k text,v int)", 0),
		fixture.Exec("INSERT INTO joa_bad VALUES(1,1,'a',1),(2,2,NULL,NULL)", 2),
		fixture.Exec("CREATE TABLE joa_atomic(g int,v jsonb)", 0),
		fixture.Exec(`INSERT INTO joa_atomic VALUES(9,'{"sentinel":9}')`, 1),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	destination := fixture.Select("SELECT v FROM joa_destination", []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": 2, "b": 1}`}, {nil}})
	destination.Bag = true
	jsonArrayAggregateRoutes(t, p, b, destination)
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT v FROM joa_ctas", "JSON", `{ "b" : 1, "a" : 2, "a" : null }`))
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT v FROM joa_materialized", "JSONB", `{"a": 2, "b": 1}`))
	jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,j,b FROM joa_grouped", []string{"g", "j", "b"}, []string{"TEXT", "JSON", "JSONB"}, [][]any{{"a", `{ "b" : 1, "a" : 2, "a" : null }`, `{"a": 2, "b": 1}`}}))
	for _, f := range jsonObjectAggregateFamilies {
		q := "INSERT INTO joa_atomic SELECT g," + f.name + "(k,v ORDER BY id) FROM joa_bad GROUP BY g ORDER BY g"
		// Destination JSONB also admits JSON aggregates through ordinary assignment.
		jsonArrayAggregateError(t, p, b, q, jsonObjectAggregateNullKey(f.typ), false)
		jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,v FROM joa_atomic", []string{"g", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(9), `{"sentinel": 9}`}}))
	}
	for _, prefix := range []string{"CREATE TABLE joa_failed_ctas AS ", "CREATE MATERIALIZED VIEW joa_failed_materialized AS "} {
		q := prefix + "SELECT jsonb_object_agg(k,v ORDER BY id) AS v FROM joa_bad"
		jsonArrayAggregateError(t, p, b, q, jsonObjectAggregateNullKey("JSONB"), false)
		name := "joa_failed_ctas"
		if prefix == "CREATE MATERIALIZED VIEW joa_failed_materialized AS " {
			name = "joa_failed_materialized"
		}
		absent := "SELECT * FROM " + name
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
		d.Position = "15"
		jsonArrayAggregateError(t, p, b, absent, d, true)
	}
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE joa_bad_conversion(id int,g int,k text,v json)", 0),
		fixture.Exec(`INSERT INTO joa_bad_conversion VALUES(1,1,'a','1'),(2,2,'a','{"x":"\u0000"}')`, 2),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	q := "INSERT INTO joa_atomic SELECT g,jsonb_object_agg(k,v ORDER BY id) FROM joa_bad_conversion GROUP BY g ORDER BY g"
	conversion := fixture.ControlsReferenceDiagnostic("22P05", "unsupported Unicode escape sequence")
	conversion.Detail, conversion.Context = `\u0000 cannot be converted to text.`, `JSON data, line 1: {"x":"\u0000...`
	jsonArrayAggregateError(t, p, b, q, conversion, false)
	jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,v FROM joa_atomic", []string{"g", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(9), `{"sentinel": 9}`}}))
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if rows, err := b.DB.QueryContext(ctx, "SELECT json_object_agg(k,v ORDER BY id) FROM joa_input"); rows != nil || !errors.Is(err, context.Canceled) {
		if rows != nil {
			rows.Close()
		}
		t.Fatalf("precanceled query=%v/%v", rows, err)
	}
	if result, err := b.DB.ExecContext(ctx, "INSERT INTO joa_destination SELECT jsonb_object_agg(k,v) FROM joa_input"); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled insert=%v/%v", result, err)
	}
	jsonArrayAggregateRoutes(t, p, b, destination)
}

func TestReferenceJSONObjectAggregateConsumers(t *testing.T) {
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectAggregateSetup(t, p, b)
		jsonObjectAggregateConsumers(t, p, b)
	})
	jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectAggregateSetup(t, p, b)
		for _, tc := range []struct{ q, message string }{
			{"INSERT INTO joa_input(id,v) VALUES(8,json_object_agg('a',1))", "aggregate functions are not allowed in VALUES"},
			{"INSERT INTO joa_input(id,v) VALUES(8,1) RETURNING json_object_agg('a',v)", "aggregate functions are not allowed in RETURNING"},
			{"CREATE TABLE joa_check(v int CHECK(json_object_agg('a',v) IS NOT NULL))", "aggregate functions are not allowed in check constraints"},
			{"CREATE TABLE joa_generated(v int,j json GENERATED ALWAYS AS(json_object_agg('a',v)) STORED)", "aggregate functions are not allowed in column generation expressions"},
			{"CREATE TABLE joa_default(j json DEFAULT json_object_agg('a',1))", "aggregate functions are not allowed in DEFAULT expressions"},
			{"CREATE INDEX joa_index ON joa_input((json_object_agg(k,v)))", "aggregate functions are not allowed in index expressions"},
			{"SELECT v FROM joa_input LIMIT json_object_agg(k,v)", "aggregate functions are not allowed in LIMIT"},
			{"SELECT a.v FROM joa_input a JOIN joa_input b ON json_object_agg(a.k,a.v) IS NOT NULL", "aggregate functions are not allowed in JOIN conditions"},
		} {
			d := fixture.ControlsReferenceDiagnostic("42803", tc.message)
			d.Position = jsonArrayAggregatePosition(tc.q, "json_object_agg")
			jsonArrayAggregateError(t, p, b, tc.q, d, true)
			jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT id FROM joa_input ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}}))
		}
		for _, name := range []string{"joa_check", "joa_generated", "joa_default"} {
			d := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
			d.Position = "15"
			jsonArrayAggregateError(t, p, b, "SELECT * FROM "+name, d, true)
		}
	})
	t.Run("ACL", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"joa_reader"})
		jsonObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			jsonObjectAggregateSetup(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE joa_reader", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO joa_reader", 0))
			}
			run(fixture.Exec("GRANT SELECT(v) ON joa_input TO joa_reader", 0))
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION joa_reader", 0))
			denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table joa_input")
			checkDenied := func(q string) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, denied)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			}
			// Isolate each dependency: key, value, hidden ORDER, hidden FILTER.
			checkDenied("SELECT json_object_agg_strict(k,v) FROM joa_input WHERE FALSE")
			run(fixture.Exec("GRANT SELECT(k) ON joa_input TO joa_reader", 0))
			run(fixture.Exec("REVOKE SELECT(v) ON joa_input FROM joa_reader", 0))
			checkDenied("SELECT json_object_agg_strict(k,v) FROM joa_input WHERE FALSE")
			run(fixture.Exec("GRANT SELECT(v) ON joa_input TO joa_reader", 0))
			checkDenied("SELECT json_object_agg_strict(k,NULL ORDER BY id) FROM joa_input")
			checkDenied("SELECT json_object_agg_strict(k,NULL) FILTER(WHERE g='a') FROM joa_input")
			run(fixture.Exec("GRANT SELECT(id,g) ON joa_input TO joa_reader", 0))
			const q = "SELECT jsonb_object_agg_strict(k,v ORDER BY id) FILTER(WHERE g='a') AS v FROM joa_input"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			x := fixture.HeldStatement{conn, stmt, q}
			s := jsonArrayAggregateSelect(q, "JSONB", `{"a": 2, "b": 1}`)
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
			run(fixture.Exec("REVOKE SELECT(k) ON joa_input FROM joa_reader", 0))
			failure := fixture.ErrorDiagnostic(q, denied)
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, x, b.Recorder, failure)
			run(fixture.Exec("GRANT SELECT(k) ON joa_input TO joa_reader", 0))
			fixture.RunStep(t, p.Ctx, x, b.Recorder, s)
		})
	})
}
