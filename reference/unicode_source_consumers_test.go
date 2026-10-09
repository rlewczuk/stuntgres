package reference_test

import (
	"context"
	"testing"
)

func TestReferenceUnicodeEscapesSourceComposition(t *testing.T) {
	p := newReferenceScenario(t)
	step := func(s referenceStep) { s.nonContext = true; p.step(t, s) }
	step(referenceExec("CREATE TABLE t(x TEXT)", 0))
	step(referenceExec("INSERT INTO t VALUES ('z'),(NULL),('a')", 3))
	for _, tc := range unicodeEscapeCompositionCases() {
		if tc.code != "42601" {
			continue
		}
		for _, op := range []string{"exec", "query", "prepare"} {
			t.Run(op+"/"+tc.query, func(t *testing.T) {
				s := referenceErrorDiagnostic(tc.query, referenceDiagnostic{
					Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: tc.code,
					Message: tc.message, Position: tc.position, Hint: tc.hint,
				})
				s.errorStage = "execute"
				if op == "query" {
					s.selectRows = true
				}
				if op == "prepare" {
					s.prepared = true
					s.errorStage = "prepare"
				}
				step(s)
				step(referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}}))
				for _, backend := range p.backends() {
					peer := backend.db
					if !backend.pg {
						peer = p.native
					}
					sentinel := referenceSelect("SELECT x FROM t ORDER BY x", []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
					sentinel.nonContext = true
					referenceRunStep(t, p.ctx, peer, backend.recorder, sentinel)
				}
			})
		}
	}
	supported := referenceSelect(`SELECT x FROM t ORDER BY U&"\0078"`, []string{"x"}, []string{"TEXT"}, [][]any{{"a"}, {"z"}, {nil}})
	step(supported)
	step(referencePrepared(supported))
	for _, backend := range p.backends() {
		rows, err := backend.db.Query(supported.query)
		if err != nil {
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, supported, nil)
	}
	step(referenceExec(`CREATE TABLE U&"UESCAPE"(id INT,U&"FIRST" TEXT)`, 0))
	step(referenceExec(`INSERT INTO U&"UESCAPE"(id,U&"FIRST") VALUES (1,U&'\0041')`, 1))
	step(referencePrepared(referenceExec(`INSERT INTO "UESCAPE"(id,"FIRST") VALUES (2,U&'\+01F600')`, 1)))
	keywordRows := [][]any{{int64(1), "A"}, {int64(2), "😀"}}
	keywordSelect := referenceSelect(`SELECT id,U&"FIRST" FROM U&"UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows)
	step(keywordSelect)
	step(referencePrepared(keywordSelect))
	for _, backend := range p.backends() {
		stmt, err := backend.db.Prepare(keywordSelect.query)
		if err != nil {
			t.Fatal(err)
		}
		rows, err := stmt.Query()
		if err != nil {
			stmt.Close()
			t.Fatal(err)
		}
		lexicalRows(t, backend.pg, rows, keywordSelect, nil)
		stmt.Close()
	}
	step(referenceSelect(`SELECT id,"FIRST" FROM "UESCAPE" ORDER BY id`, []string{"id", "FIRST"}, []string{"INT4", "TEXT"}, keywordRows))
}

func TestReferenceUnicodeEscapesSourceCancellation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec(`CREATE TABLE U&"\0041"(id INT NOT NULL,U&"\+01F600" TEXT)`, 0))
			run(referenceExec(`INSERT INTO "A" VALUES (1,U&'\0041'),(2,U&'\+01F600'),(3,U&''),(4,NULL),(5,U&'O''Brien\\\0001')`, 5))
			canceled, cancel := context.WithCancel(p.ctx)
			cancel()
			_, err := backend.db.ExecContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			lexicalOrdinary(t, err, context.Canceled)
			rows, err := backend.db.QueryContext(canceled, `SELECT * FROM U&"\0041"`)
			if rows != nil {
				rows.Close()
				t.Fatal("canceled cursor")
			}
			lexicalOrdinary(t, err, context.Canceled)
			stmt, err := backend.db.PrepareContext(canceled, `INSERT INTO "A" VALUES (6,U&'\0041')`)
			if stmt != nil {
				stmt.Close()
				t.Fatal("canceled statement")
			}
			lexicalOrdinary(t, err, context.Canceled)
			for _, q := range []string{`INSERT INTO "A" VALUES (6,U&'\0041')`, `SELECT * FROM U&"\0041"`} {
				stmt, err := backend.db.Prepare(q)
				if err != nil {
					t.Fatal(err)
				}
				_, err = stmt.ExecContext(canceled)
				lexicalOrdinary(t, err, context.Canceled)
				rows, err = stmt.QueryContext(canceled)
				if rows != nil {
					rows.Close()
					t.Fatal("canceled prepared cursor")
				}
				lexicalOrdinary(t, err, context.Canceled)
				stmt.Close()
			}
			run(referenceSelect(`SELECT * FROM "A" ORDER BY id`, []string{"id", "😀"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "A"}, {int64(2), "😀"}, {int64(3), ""}, {int64(4), nil}, {int64(5), "O'Brien\\\x01"}}))
		})
	}
}
