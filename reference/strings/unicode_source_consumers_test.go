package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceUnicodeEscapesSourceComposition(t *testing.T) {
	p := fixture.NewScenario(t)
	step := func(s fixture.Step) { s.NonContext = true; p.Step(t, s) }
	step(fixture.Exec("CREATE TABLE t(x TEXT)", 0))
	step(fixture.Exec("INSERT INTO t VALUES ('z'),(NULL),('a')", 3))
	for _, tc := range unicodeEscapeCompositionCases() {
		if tc.code != "42601" {
			continue
		}
		for _, op := range []string{"exec", "query", "prepare"} {
			t.Run(op+"/"+tc.query, func(t *testing.T) {
				s := fixture.ErrorDiagnostic(tc.query, fixture.Diagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code,
					Message: tc.message, Position: tc.position, Hint: tc.hint,
				})
				s.ErrorStage = "execute"
				if op == "query" {
					s.SelectRows = true
				}
				if op == "prepare" {
					s.Prepared = true
					s.ErrorStage = "prepare"
				}
				step(s)
				step(fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
				for _, backend := range p.Backends() {
					peer := backend.DB
					if !backend.PG {
						peer = p.Native
					}
					sentinel := fixture.Select("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
					sentinel.NonContext = true
					fixture.RunStep(t, p.Ctx, peer, backend.Recorder, sentinel)
				}
			})
		}
	}
	supported := fixture.Select(`SELECT x FROM t ORDER BY U&"\0078"`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
	step(supported)
	step(fixture.Prepared(supported))
	for _, backend := range p.Backends() {
		rows, err := backend.DB.Query(supported.Query)
		if err != nil {
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, supported, nil)
	}
	step(fixture.Exec(`CREATE TABLE U&"UESCAPE"(id INT,U&"FIRST" TEXT)`, 0))
	step(fixture.Exec(`INSERT INTO U&"UESCAPE"(id,U&"FIRST") VALUES (1,U&'\0041')`, 1))
	step(fixture.Prepared(fixture.Exec(`INSERT INTO "UESCAPE"(id,"FIRST") VALUES (2,U&'\+01F600')`, 1)))
	keywordRows := [][]any{{int64(1), "A"}, {int64(2), "😀"}}
	keywordSelect := fixture.Select(`SELECT id,U&"FIRST" FROM U&"UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows)
	step(keywordSelect)
	step(fixture.Prepared(keywordSelect))
	for _, backend := range p.Backends() {
		stmt, err := backend.DB.Prepare(keywordSelect.Query)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		fixture.LexicalRows(t, backend.PG, rows, keywordSelect, nil)
		stmt.Close()
	}
	step(fixture.Select(`SELECT id,"FIRST" FROM "UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows))
}

func TestReferenceUnicodeEscapesSourceCancellation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) {
				s.NonContext = true
				fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
			}
			run(fixture.Exec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\+01F600'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
			canceled, cancel := context.WithCancel(p.Ctx)
			cancel()
			_, err := backend.DB.ExecContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			fixture.LexicalOrdinary(t, err, context.Canceled)
			rows, err := backend.DB.QueryContext(canceled, `SELECT * FROM U&"\0041"`)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled cursor")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			stmt, err := backend.DB.PrepareContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled statement")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			for _, q := range []string{`INSERT INTO "A" VALUES (6,U&'\0041')`, `SELECT * FROM U&"\0041"`} {
				stmt, err := backend.DB.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.ExecContext(canceled)
				fixture.LexicalOrdinary(t, err, context.Canceled)
				rows, err = stmt.QueryContext(canceled)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled prepared cursor")
				}
				fixture.LexicalOrdinary(t, err, context.Canceled)
				stmt.Close()
			}
			run(fixture.Select(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}}))
		})
	}
}
