package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBooleanDiagnosticsPreservation(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE accepted(flag BOOL)", 0))
			q := "INSERT INTO accepted VALUES ($1)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			for _, group := range []struct {
				values []string
				want   bool
			}{
				{[]string{"t", "tr", "tru", "true", "y", "ye", "yes", "on", "1", "TrUe", " YES ", "\tOn\n"}, true},
				{[]string{"f", "fa", "fal", "fals", "false", "n", "no", "of", "off", "0", "FaLsE", " NO ", "\tOfF\n"}, false},
			} {
				for index, value := range group.values {
					for _, prepared := range []bool{false, true} {
						for _, arg := range []any{value, []byte(value)} {
							s := fixture.Exec(q, 1, arg)
							if prepared {
								fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
							} else {
								run(s)
							}
						}
					}
					rows := make([][]any, 4*(index+1))
					for i := range rows {
						rows[i] = []any{group.want}
					}
					run(fixture.Select("SELECT flag FROM accepted WHERE flag=$1", []string{"flag"}, []string{"BOOL"}, rows, value))
				}
			}
			run(fixture.Exec("CREATE TABLE native(flag BOOL)", 0))
			run(fixture.Exec("INSERT INTO native VALUES (TRUE),(FALSE),(NULL)", 3))
			for _, value := range []any{true, false, nil, int64(1), int64(0)} {
				run(fixture.Exec("INSERT INTO native VALUES ($1)", 1, value))
			}
			s := fixture.Select("SELECT flag FROM native ORDER BY flag", []string{"flag"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}})
			run(s)
			rows, err := backend.DB.QueryContext(p.Ctx, s.Query)
			if err != nil {
				t.Fatal(err)
			}
			fixture.LexicalRows(t, backend.PG, rows, s, nil)
			run(fixture.Select("SELECT flag FROM native WHERE flag=NULL", []string{"flag"}, []string{"BOOL"}, nil))
		})
	}
}

func TestReferenceBooleanDiagnosticsAtomicity(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s) }
			run(fixture.Exec("CREATE TABLE b(id INT,flag BOOL)", 0))
			run(fixture.Exec("INSERT INTO b VALUES (99,TRUE)", 1))
			sentinel := fixture.Select("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
			for _, op := range []string{"exec", "query", "prepare"} {
				s := fixture.ImplicitError("INSERT INTO b VALUES (1,TRUE),(2,'o')", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "o"`, Position: "34"})
				s.Prepared = op == "prepare"
				s.SelectRows = op == "query"
				s.ErrorStage = "execute"
				if s.Prepared {
					s.ErrorStage = "prepare"
				}
				run(s)
				run(sentinel)
				meta := fixture.Select("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}})
				rows, err := backend.DB.QueryContext(p.Ctx, meta.Query)
				if err != nil {
					t.Fatal(err)
				}
				fixture.LexicalRows(t, backend.PG, rows, meta, nil)
			}
			for _, prepared := range []bool{false, true} {
				for _, query := range []bool{false, true} {
					s := fixture.ImplicitError("INSERT INTO b VALUES (1,TRUE),(2,$1)", fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "o"`, Context: "unnamed portal parameter $1 = '...'"}, "o")
					s.Prepared = prepared
					s.SelectRows = query
					s.ErrorStage = "execute"
					run(s)
					run(sentinel)
				}
			}
			q := "INSERT INTO b VALUES ($1,$2)"
			stmt, err := backend.DB.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: backend.DB, Statement: stmt, SQL: q}
			s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "o"`, Context: "unnamed portal parameter $2 = '...'"}, 1, "o")
			s.ErrorStage = "execute"
			fixture.RunStep(t, p.Ctx, held, backend.Recorder, s)
			for i, value := range []any{true, false, nil} {
				fixture.RunStep(t, p.Ctx, held, backend.Recorder, fixture.Exec(q, 1, i+1, value))
			}
			run(fixture.Select("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}}))
		})
	}
}
