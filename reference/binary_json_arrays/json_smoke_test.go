package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONFeatureSmoke(t *testing.T) {
	p := fixture.NewScenario(t)
	const first = ` {"b":2,"a":1,"a":3} `
	const second = "\n{\"a\":3, \"a\":1,\"b\":2}\t"
	want := [][]any{{int64(1), first, nil}, {int64(2), second, "null"}}
	for _, backend := range p.Backends() {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
		run(fixture.Prepared(fixture.Exec("CREATE TABLE json_smoke(id INTEGER,v JSON,n JSON)", 0)))
		run(fixture.Exec("CREATE TABLE json_smoke_empty(v JSON)", 0))
		run(fixture.Exec("INSERT INTO json_smoke VALUES(1,$1,NULL)", 1, []byte(first)))
		q := "INSERT INTO json_smoke VALUES($1,$2,$3)"
		stmt, err := backend.DB.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, 2, second, "null"))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Prepared(fixture.Select("SELECT id,v,n FROM json_smoke ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want)))
		metadata := fixture.Select("SELECT v FROM json_smoke ORDER BY id", []string{"v"}, []string{"JSON"}, [][]any{{first}, {second}})
		metadata.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonReferenceMetadata(t, rows, backend.PG, true) }
		run(metadata)
		empty := fixture.Prepared(fixture.Select("SELECT v FROM json_smoke_empty", []string{"v"}, []string{"JSON"}, nil))
		empty.CheckMetadata = metadata.CheckMetadata
		run(empty)
		for _, q := range []string{`SELECT json 'null' AS v`, `SELECT CAST('null' AS JSON) AS v`, `SELECT 'null'::JSON AS v`, `SELECT pg_catalog.json 'null' AS v`} {
			run(fixture.Prepared(fixture.Select(q, []string{"v"}, []string{"JSON"}, [][]any{{"null"}})))
		}
		run(fixture.Select(`SELECT '"\u0000\uD800"'::JSON AS v`, []string{"v"}, []string{"JSON"}, [][]any{{`"\u0000\uD800"`}}))
		diagnostic := fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: `"\u" must be followed by four hexadecimal digits.`, Context: "JSON data, line 1: \"\\u000g...\nunnamed portal parameter $1 = '...'"}
		for _, prepared := range []bool{false, true} {
			failure := fixture.ErrorDiagnostic("INSERT INTO json_smoke VALUES(3,$1,NULL)", diagnostic, `"\u000g"`)
			failure.ErrorStage = "execute"
			if prepared {
				failure = fixture.Prepared(failure)
			}
			run(failure)
			run(fixture.Select("SELECT id,v,n FROM json_smoke ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
		}
	}
	// The native dump also contains smoke tables. Clear their PostgreSQL copies
	// before replay rather than inventing a PostgreSQL Reset/Restore API.
	if p.Postgres != nil {
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("DROP TABLE json_smoke", 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("DROP TABLE json_smoke_empty", 0))
	}
	jsonReferenceControls(t, p)
}
