package reference_test

import (
	"context"
	"errors"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectUniqueAggregateConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, q := range []string{
		"WITH a AS(SELECT jsonb_object_agg_unique_strict(k,v ORDER BY id) AS v FROM jou_input) SELECT v FROM a",
		"SELECT v FROM(SELECT jsonb_object_agg_unique_strict(k,v ORDER BY id) AS v FROM jou_input) a",
		"SELECT jsonb_object_agg_unique_strict(k,v ORDER BY id) AS v FROM jou_input HAVING count(*)=3",
	} {
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSONB", `{"a": 2, "b": 1}`))
	}
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE jou_destination(v jsonb)", 0),
		fixture.Exec("INSERT INTO jou_destination SELECT jsonb_object_agg_unique_strict(k,v ORDER BY id) FROM jou_input", 1),
		fixture.Exec("INSERT INTO jou_destination SELECT jsonb_object_agg_unique(k,v) FROM jou_input WHERE FALSE", 1),
		fixture.Exec("INSERT INTO jou_destination SELECT jsonb_object_agg_unique(k,v) FROM jou_input WHERE FALSE GROUP BY g", 0),
		fixture.Exec("CREATE TABLE jou_ctas AS SELECT json_object_agg_unique(k,v ORDER BY id) AS v FROM jou_input WHERE id<3", 1),
		fixture.Exec("CREATE MATERIALIZED VIEW jou_materialized AS SELECT jsonb_object_agg_unique_strict(k,v ORDER BY id) AS v FROM jou_input", 1),
		fixture.Exec("CREATE VIEW jou_grouped AS SELECT g,jsonb_object_agg_unique_strict(k,v ORDER BY id) AS v FROM jou_input GROUP BY g", 0),
		fixture.Exec("CREATE TABLE jou_bad(id int,g int,k text,v int)", 0),
		fixture.Exec("INSERT INTO jou_bad VALUES(1,1,'b',1),(2,2,'a',NULL),(3,2,'a',2)", 3),
		fixture.Exec("CREATE TABLE jou_atomic(g int,v jsonb)", 0),
		fixture.Exec(`INSERT INTO jou_atomic VALUES(9,'{"sentinel":9}')`, 1),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	destination := fixture.Select("SELECT v FROM jou_destination", []string{"v"}, []string{"JSONB"}, [][]any{{`{"a": 2, "b": 1}`}, {nil}})
	destination.Bag = true
	jsonArrayAggregateRoutes(t, p, b, destination)
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT v FROM jou_ctas", "JSON", `{ "b" : 1, "a" : 2 }`))
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT v FROM jou_materialized", "JSONB", `{"a": 2, "b": 1}`))
	jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,v FROM jou_grouped ORDER BY g", []string{"g", "v"}, []string{"TEXT", "JSONB"}, [][]any{{"a", `{"a": 2, "b": 1}`}, {"b", `{}`}}))
	for _, f := range jsonObjectUniqueAggregateFamilies {
		q := "INSERT INTO jou_atomic SELECT g," + f.name + "(k,v ORDER BY id) FROM jou_bad GROUP BY g ORDER BY g"
		jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(f.typ == "JSONB", `"a"`), false)
		jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,v FROM jou_atomic", []string{"g", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(9), `{"sentinel": 9}`}}))
	}
	for _, prefix := range []string{"CREATE TABLE jou_failed_ctas AS ", "CREATE MATERIALIZED VIEW jou_failed_materialized AS "} {
		jsonArrayAggregateError(t, p, b, prefix+"SELECT jsonb_object_agg_unique(k,v ORDER BY id) AS v FROM jou_bad", sqlJSONObjectAggregateDuplicate(true, `"a"`), false)
		name := "jou_failed_ctas"
		if prefix == "CREATE MATERIALIZED VIEW jou_failed_materialized AS " {
			name = "jou_failed_materialized"
		}
		d := fixture.ControlsReferenceDiagnostic("42P01", `relation "`+name+`" does not exist`)
		d.Position = "15"
		jsonArrayAggregateError(t, p, b, "SELECT * FROM "+name, d, true)
	}
	ctx, cancel := context.WithCancel(p.Ctx)
	cancel()
	if rows, err := b.DB.QueryContext(ctx, "SELECT json_object_agg_unique(k,v) FROM jou_input"); rows != nil || !errors.Is(err, context.Canceled) {
		if rows != nil {
			rows.Close()
		}
		t.Fatalf("precanceled query=%v/%v", rows, err)
	}
	if result, err := b.DB.ExecContext(ctx, "INSERT INTO jou_destination SELECT jsonb_object_agg_unique(k,v) FROM jou_input"); result != nil || !errors.Is(err, context.Canceled) {
		t.Fatalf("precanceled insert=%v/%v", result, err)
	}
	jsonArrayAggregateRoutes(t, p, b, destination)
}

func TestReferenceJSONObjectUniqueAggregateConsumers(t *testing.T) {
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectUniqueAggregateSetup(t, p, b)
		jsonObjectUniqueAggregateConsumers(t, p, b)
	})
	t.Run("ACL", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"jou_reader"})
		jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			jsonObjectUniqueAggregateSetup(t, p, b)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE ROLE jou_reader", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE ON SCHEMA public TO jou_reader", 0))
			}
			run(fixture.Exec("GRANT SELECT(v) ON jou_input TO jou_reader", 0))
			conn, err := b.DB.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("SET SESSION AUTHORIZATION jou_reader", 0))
			denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table jou_input")
			checkDenied := func(q string) {
				for _, prepared := range []bool{false, true} {
					s := fixture.ErrorDiagnostic(q, denied)
					s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
					fixture.RunStep(t, p.Ctx, conn, b.Recorder, s)
				}
			}
			checkDenied("SELECT json_object_agg_unique_strict(k,v) FROM jou_input WHERE FALSE")
			run(fixture.Exec("GRANT SELECT(k) ON jou_input TO jou_reader", 0))
			run(fixture.Exec("REVOKE SELECT(v) ON jou_input FROM jou_reader", 0))
			checkDenied("SELECT json_object_agg_unique_strict(k,v) FROM jou_input WHERE FALSE")
			run(fixture.Exec("GRANT SELECT(v) ON jou_input TO jou_reader", 0))
			checkDenied("SELECT json_object_agg_unique_strict(k,NULL ORDER BY id) FROM jou_input")
			checkDenied("SELECT json_object_agg_unique_strict(k,NULL) FILTER(WHERE g='a') FROM jou_input")
			run(fixture.Exec("GRANT SELECT(id,g) ON jou_input TO jou_reader", 0))
			const q = "SELECT jsonb_object_agg_unique_strict(k,v ORDER BY id) FILTER(WHERE g='a') AS v FROM jou_input"
			stmt, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{conn, stmt, q}
			s := jsonArrayAggregateSelect(q, "JSONB", `{"a": 2, "b": 1}`)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("REVOKE SELECT(k) ON jou_input FROM jou_reader", 0))
			failure := fixture.ErrorDiagnostic(q, denied)
			failure.SelectRows, failure.ErrorStage = true, "execute"
			fixture.RunStep(t, p.Ctx, held, b.Recorder, failure)
			run(fixture.Exec("GRANT SELECT(k) ON jou_input TO jou_reader", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		})
	})
}
