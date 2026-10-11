package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceCharacterHeldWidthParameters(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE widths(a CHAR(2),b CHAR(4))", 0))
	for _, backend := range p.Backends() {
		func() {
			q := "INSERT INTO widths VALUES($1,$1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, arg := range []any{"a", nil} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, arg))
			}
			failure := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22001", Message: "value too long for type character(2)"}, "abc")
			failure.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, failure)
			cast := fixture.ImplicitError("INSERT INTO widths(a) VALUES(CAST('abcd' AS varchar(3)))", fixture.Diagnostic{Code: "22001", Message: "value too long for type character(2)"})
			cast.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, fixture.Prepared(cast))
			read := fixture.Select("SELECT * FROM widths", []string{"a", "b"}, []string{"BPCHAR", "BPCHAR"}, [][]any{{"a ", "a   "}, {nil, nil}})
			read.Bag = true
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, read)
		}()
	}
}

func TestReferenceCharacterUnicodeAssignmentBoundaries(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, tc := range []struct{ declaration, typ, name string }{{"varchar(2)", "VARCHAR", "character varying(2)"}, {"char(2)", "BPCHAR", "character(2)"}} {
		p.Step(t, fixture.Exec("CREATE TABLE c(v "+tc.declaration+")", 0))
		p.Step(t, fixture.Exec("INSERT INTO c VALUES('é🙂'),('é')", 2))
		read := fixture.Select("SELECT v FROM c", []string{"v"}, []string{tc.typ}, [][]any{{"é🙂"}, {"e\u0301"}})
		read.Bag = true
		p.Step(t, read)
		for _, value := range []string{"é🙂x", "e\u0301x", "a🙂é"} {
			for _, prepared := range []bool{false, true} {
				var arg any = value
				if prepared {
					arg = []byte(value)
				}
				s := fixture.ImplicitError("INSERT INTO c VALUES($1)", fixture.Diagnostic{Code: "22001", Message: "value too long for type " + tc.name}, arg)
				s.Prepared, s.ErrorStage = prepared, "execute"
				p.Step(t, s)
				p.Step(t, read)
			}
		}
		p.Step(t, fixture.Exec("INSERT INTO c VALUES($1)", 1, "é🙂   "))
		read.Rows = [][]any{{"é🙂"}, {"e\u0301"}, {"é🙂"}}
		p.Step(t, read)
		p.Step(t, fixture.Exec("DROP TABLE c", 0))
	}
}

func TestReferenceCharacterAffectedCountsAndAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	p.Step(t, fixture.Exec("CREATE TABLE c(v VARCHAR(2))", 0))
	p.Step(t, fixture.Exec("INSERT INTO c VALUES('ok'),('é🙂'),(NULL)", 3))
	for _, backend := range p.Backends() {
		for _, prepared := range []bool{false, true} {
			ctx := context.WithValue(p.Ctx, fixture.RecorderKey{}, backend.Recorder)
			q := "INSERT INTO c VALUES('a'),('bad')"
			var result sql.Result
			var err error
			if prepared {
				stmt, prepareErr := backend.DB.PrepareContext(ctx, q)
				if prepareErr != nil {
					t.Fatal("valid INSERT rejected at Prepare", prepareErr)
				}
				result, err = stmt.ExecContext(ctx)
				stmt.Close()
			} else {
				result, err = backend.DB.ExecContext(ctx, q)
			}
			if result != nil {
				if n, e := result.RowsAffected(); e == nil && n != 0 {
					t.Fatal("failed statement exposed successful count", n)
				}
			}
			fixture.State(t, err, "22001")
			fixture.AssertDiagnostic(t, err, &fixture.Diagnostic{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22001", Message: "value too long for type character varying(2)"})
			read := fixture.Select("SELECT * FROM c", []string{"v"}, []string{"VARCHAR"}, [][]any{{"ok"}, {"é🙂"}, {nil}})
			read.Bag = true
			fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, read)
		}
	}
}
