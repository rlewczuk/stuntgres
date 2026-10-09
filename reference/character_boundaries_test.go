package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceCharacterHeldWidthParameters(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE widths(a CHAR(2),b CHAR(4))", 0))
	for _, backend := range p.backends() {
		func() {
			q := "INSERT INTO widths VALUES($1,$1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			for _, arg := range []any{"a", nil} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, arg))
			}
			failure := referenceImplicitError(q, referenceDiagnostic{Code: "22001", Message: "value too long for type character(2)"}, "abc")
			failure.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, backend.recorder, failure)
			cast := referenceImplicitError("INSERT INTO widths(a) VALUES(CAST('abcd' AS varchar(3)))", referenceDiagnostic{Code: "22001", Message: "value too long for type character(2)"})
			cast.errorStage = "execute"
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, referencePrepared(cast))
			read := referenceSelect("SELECT * FROM widths", []string{"a", "b"}, []string{"BPCHAR", "BPCHAR"}, [][]any{{"a ", "a   "}, {nil, nil}})
			read.bag = true
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, read)
		}()
	}
}

func TestReferenceCharacterUnicodeAssignmentBoundaries(t *testing.T) {
	p := newReferenceScenario(t)
	for _, tc := range []struct{ declaration, typ, name string }{{"varchar(2)", "VARCHAR", "character varying(2)"}, {"char(2)", "BPCHAR", "character(2)"}} {
		p.step(t, referenceExec("CREATE TABLE c(v "+tc.declaration+")", 0))
		p.step(t, referenceExec("INSERT INTO c VALUES('é🙂'),('é')", 2))
		read := referenceSelect("SELECT v FROM c", []string{"v"}, []string{tc.typ}, [][]any{{"é🙂"}, {"e\u0301"}})
		read.bag = true
		p.step(t, read)
		for _, value := range []string{"é🙂x", "e\u0301x", "a🙂é"} {
			for _, prepared := range []bool{false, true} {
				var arg any = value
				if prepared {
					arg = []byte(value)
				}
				s := referenceImplicitError("INSERT INTO c VALUES($1)", referenceDiagnostic{Code: "22001", Message: "value too long for type " + tc.name}, arg)
				s.prepared, s.errorStage = prepared, "execute"
				p.step(t, s)
				p.step(t, read)
			}
		}
		p.step(t, referenceExec("INSERT INTO c VALUES($1)", 1, "é🙂   "))
		read.rows = [][]any{{"é🙂"}, {"e\u0301"}, {"é🙂"}}
		p.step(t, read)
		p.step(t, referenceExec("DROP TABLE c", 0))
	}
}

func TestReferenceCharacterAffectedCountsAndAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	p.step(t, referenceExec("CREATE TABLE c(v VARCHAR(2))", 0))
	p.step(t, referenceExec("INSERT INTO c VALUES('ok'),('é🙂'),(NULL)", 3))
	for _, backend := range p.backends() {
		for _, prepared := range []bool{false, true} {
			ctx := context.WithValue(p.ctx, referenceRecorderKey{}, backend.recorder)
			q := "INSERT INTO c VALUES('a'),('bad')"
			var result sql.Result
			var err error
			if prepared {
				stmt, prepareErr := backend.db.PrepareContext(ctx, q)
				if prepareErr != nil {
					t.Fatal("valid INSERT rejected at Prepare", prepareErr)
				}
				result, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				result, err = backend.db.ExecContext(ctx, q)
			}
			if result != nil {
				if n, e := result.RowsAffected(); e == nil && n != 0 {
					t.Fatal("failed statement exposed successful count", n)
				}
			}
			referenceState(t, err, "22001")
			referenceAssertDiagnostic(t, err, &referenceDiagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22001", Message: "value too long for type character varying(2)"})
			read := referenceSelect("SELECT * FROM c", []string{"v"}, []string{"VARCHAR"}, [][]any{{"ok"}, {"é🙂"}, {nil}})
			read.bag = true
			referenceRunStep(t, p.ctx, backend.db, backend.recorder, read)
		}
	}
}
