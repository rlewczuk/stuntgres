package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceJSONFeatureSmoke(t *testing.T) {
	p := newReferenceScenario(t)
	const first = ` {"b":2,"a":1,"a":3} `
	const second = "\n{\"a\":3, \"a\":1,\"b\":2}\t"
	want := [][]any{{int64(1), first, nil}, {int64(2), second, "null"}}
	for _, backend := range p.backends() {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
		run(referencePrepared(referenceExec("CREATE TABLE json_smoke(id INTEGER,v JSON,n JSON)", 0)))
		run(referenceExec("CREATE TABLE json_smoke_empty(v JSON)", 0))
		run(referenceExec("INSERT INTO json_smoke VALUES(1,$1,NULL)", 1, []byte(first)))
		q := "INSERT INTO json_smoke VALUES($1,$2,$3)"
		stmt, err := backend.db.PrepareContext(p.ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
		referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, 2, second, "null"))
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(referencePrepared(referenceSelect("SELECT id,v,n FROM json_smoke ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want)))
		metadata := referenceSelect("SELECT v FROM json_smoke ORDER BY id", []string{"v"}, []string{"JSON"}, [][]any{{first}, {second}})
		metadata.checkMetadata = func(t *testing.T, rows *sql.Rows) { jsonReferenceMetadata(t, rows, backend.pg, true) }
		run(metadata)
		empty := referencePrepared(referenceSelect("SELECT v FROM json_smoke_empty", []string{"v"}, []string{"JSON"}, nil))
		empty.checkMetadata = metadata.checkMetadata
		run(empty)
		for _, q := range []string{`SELECT json 'null' AS v`, `SELECT CAST('null' AS JSON) AS v`, `SELECT 'null'::JSON AS v`, `SELECT pg_catalog.json 'null' AS v`} {
			run(referencePrepared(referenceSelect(q, []string{"v"}, []string{"JSON"}, [][]any{{"null"}})))
		}
		run(referenceSelect(`SELECT '"\u0000\uD800"'::JSON AS v`, []string{"v"}, []string{"JSON"}, [][]any{{`"\u0000\uD800"`}}))
		diagnostic := referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22P02", Message: "invalid input syntax for type json", Detail: `"\u" must be followed by four hexadecimal digits.`, Context: "JSON data, line 1: \"\\u000g...\nunnamed portal parameter $1 = '...'"}
		for _, prepared := range []bool{false, true} {
			failure := referenceErrorDiagnostic("INSERT INTO json_smoke VALUES(3,$1,NULL)", diagnostic, `"\u000g"`)
			failure.errorStage = "execute"
			if prepared {
				failure = referencePrepared(failure)
			}
			run(failure)
			run(referenceSelect("SELECT id,v,n FROM json_smoke ORDER BY id", []string{"id", "v", "n"}, []string{"INT4", "JSON", "JSON"}, want))
		}
	}
	// The native dump also contains smoke tables. Clear their PostgreSQL copies
	// before replay rather than inventing a PostgreSQL Reset/Restore API.
	if p.postgres != nil {
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DROP TABLE json_smoke", 0))
		referenceRunStep(t, p.ctx, p.postgres, p.postgresRecord, referenceExec("DROP TABLE json_smoke_empty", 0))
	}
	jsonReferenceControls(t, p)
}
