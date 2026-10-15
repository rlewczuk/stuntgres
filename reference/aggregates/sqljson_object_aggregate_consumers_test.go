package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func sqlJSONObjectAggregateConsumerSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE sjo_input(id int,g int,k text,v text)", 0),
		fixture.Exec(`INSERT INTO sjo_input VALUES(1,1,'a','1'),(2,2,'a','[2]'),(3,2,'b',NULL)`, 3),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func sqlJSONObjectAggregateConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, q := range []string{
		"SELECT json_objectagg(k VALUE v FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb) AS v FROM sjo_input WHERE g=2",
		"SELECT json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input WHERE g=2 HAVING count(*)=2",
		"WITH a AS(SELECT json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input WHERE g=2) SELECT v FROM a",
		"SELECT v FROM(SELECT json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input WHERE g=2) a",
	} {
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, "JSONB", `{"a": [2]}`))
	}
	grouped := fixture.Select("SELECT g,json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input GROUP BY g ORDER BY g", []string{"g", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), `{"a": 1}`}, {int64(2), `{"a": [2]}`}})
	jsonArrayAggregateRoutes(t, p, b, grouped)
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT coalesce(json_objectagg(k:v FORMAT JSON RETURNING jsonb),'{}'::jsonb) AS v FROM sjo_input WHERE FALSE", "JSONB", `{}`))
	jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT json_objectagg(k:v RETURNING jsonb) AS a,json_objectagg(k:v RETURNING jsonb) AS b FROM sjo_input WHERE g=1", []string{"a", "b"}, []string{"JSONB", "JSONB"}, [][]any{{`{"a": "1"}`, `{"a": "1"}`}}))
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE sjo_destination(g int,v jsonb)", 0),
		fixture.Exec(`INSERT INTO sjo_destination VALUES(9,'{"sentinel":9}')`, 1),
		fixture.Exec("CREATE TABLE sjo_ctas AS SELECT json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input WHERE g=2", 1),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT v FROM sjo_ctas", "JSONB", `{"a": [2]}`))
	// First group can finish before a duplicate in the second group. No result of
	// the failing statement may be published to the destination.
	fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO sjo_input VALUES(4,2,'a','3')", 1))
	q := "INSERT INTO sjo_destination SELECT g,json_objectagg(k:v FORMAT JSON WITH UNIQUE RETURNING jsonb) FROM sjo_input GROUP BY g ORDER BY g"
	jsonArrayAggregateError(t, p, b, q, sqlJSONObjectAggregateDuplicate(true, `"a"`), false)
	jsonArrayAggregateRoutes(t, p, b, fixture.Select("SELECT g,v FROM sjo_destination", []string{"g", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(9), `{"sentinel": 9}`}}))
}

func TestReferenceSQLJSONObjectAggregateConsumers(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		sqlJSONObjectAggregateConsumerSetup(t, p, b)
		sqlJSONObjectAggregateConsumers(t, p, b)
	})
}

func sqlJSONObjectAggregateHeld(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	conn, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	const q = "SELECT json_objectagg(k:v FORMAT JSON ABSENT ON NULL WITH UNIQUE KEYS RETURNING jsonb) AS v FROM sjo_input WHERE id<=$1"
	stmt, err := conn.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: conn, Statement: stmt, SQL: q}
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, held, b.Recorder, s) }
	good := jsonArrayAggregateSelect(q, "JSONB", `{"a": 1}`, int64(1))
	run(good)
	duplicate := fixture.ErrorDiagnostic(q, sqlJSONObjectAggregateDuplicate(true, `"a"`), int64(2))
	duplicate.SelectRows = true
	duplicate.ErrorStage = "execute"
	run(duplicate)
	run(good)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("UPDATE sjo_input SET k=NULL WHERE id=1", 1))
	bad := fixture.ErrorDiagnostic(q, jsonObjectAggregateNullKey("JSONB"), int64(1))
	bad.SelectRows = true
	bad.ErrorStage = "execute"
	run(bad)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("UPDATE sjo_input SET k='a',v='no' WHERE id=1", 1))
	d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type json")
	d.Detail = `Token "no" is invalid.`
	d.Context = "JSON data, line 1: no"
	bad = fixture.ErrorDiagnostic(q, d, int64(1))
	bad.SelectRows = true
	bad.ErrorStage = "execute"
	run(bad)
	fixture.RunStep(t, p.Ctx, conn, b.Recorder, fixture.Exec("UPDATE sjo_input SET v='[3]' WHERE id=1", 1))
	good.Rows = [][]any{{`{"a": [3]}`}}
	run(good)
	empty := jsonArrayAggregateSelect(q, "JSONB", nil, int64(0))
	run(empty)
	run(good)
	// A separate retained statement must not share uniqueness state with this one.
	jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg(k:v FORMAT JSON WITH UNIQUE RETURNING jsonb) AS v FROM sjo_input WHERE id=1", "JSONB", `{"a": [3]}`))
}

func TestReferenceSQLJSONObjectAggregateLifecycle(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		sqlJSONObjectAggregateConsumerSetup(t, p, b)
		sqlJSONObjectAggregateHeld(t, p, b)
	})
}
