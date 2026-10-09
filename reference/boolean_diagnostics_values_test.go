package reference_test

import "testing"

func TestReferenceBooleanDiagnosticsPreservation(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE accepted(flag BOOL)", 0))
			q := "INSERT INTO accepted VALUES ($1)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
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
							s := referenceExec(q, 1, arg)
							if prepared {
								referenceRunStep(t, p.ctx, held, backend.recorder, s)
							} else {
								run(s)
							}
						}
					}
					rows := make([][]any, 4*(index+1))
					for i := range rows {
						rows[i] = []any{group.want}
					}
					run(referenceSelect("SELECT flag FROM accepted WHERE flag=$1", []string{"flag"}, []string{"BOOL"}, rows, value))
				}
			}
			run(referenceExec("CREATE TABLE native(flag BOOL)", 0))
			run(referenceExec("INSERT INTO native VALUES (TRUE),(FALSE),(NULL)", 3))
			for _, value := range []any{true, false, nil, int64(1), int64(0)} {
				run(referenceExec("INSERT INTO native VALUES ($1)", 1, value))
			}
			s := referenceSelect("SELECT flag FROM native ORDER BY flag", []string{"flag"}, []string{"BOOL"}, [][]any{{false}, {false}, {false}, {true}, {true}, {true}, {nil}, {nil}})
			run(s)
			rows, err := backend.db.QueryContext(p.ctx, s.query)
			if err != nil {
				t.Fatal(err)
			}
			lexicalRows(t, backend.pg, rows, s, nil)
			run(referenceSelect("SELECT flag FROM native WHERE flag=NULL", []string{"flag"}, []string{"BOOL"}, nil))
		})
	}
}

func TestReferenceBooleanDiagnosticsAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, backend.db, backend.recorder, s) }
			run(referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
			run(referenceExec("INSERT INTO b VALUES (99,TRUE)", 1))
			sentinel := referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
			for _, op := range []string{"exec", "query", "prepare"} {
				s := referenceImplicitError("INSERT INTO b VALUES (1,TRUE),(2,'o')", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "o"`, Position: "34"})
				s.prepared = op == "prepare"
				s.selectRows = op == "query"
				s.errorStage = "execute"
				if s.prepared {
					s.errorStage = "prepare"
				}
				run(s)
				run(sentinel)
				meta := referenceSelect("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}})
				rows, err := backend.db.QueryContext(p.ctx, meta.query)
				if err != nil {
					t.Fatal(err)
				}
				lexicalRows(t, backend.pg, rows, meta, nil)
			}
			for _, prepared := range []bool{false, true} {
				for _, query := range []bool{false, true} {
					s := referenceImplicitError("INSERT INTO b VALUES (1,TRUE),(2,$1)", referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "o"`, Context: "unnamed portal parameter $1 = '...'"}, "o")
					s.prepared = prepared
					s.selectRows = query
					s.errorStage = "execute"
					run(s)
					run(sentinel)
				}
			}
			q := "INSERT INTO b VALUES ($1,$2)"
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{referenceExecutor: backend.db, statement: stmt, query: q}
			s := referenceImplicitError(q, referenceDiagnostic{Code: "22P02", Message: `invalid input syntax for type boolean: "o"`, Context: "unnamed portal parameter $2 = '...'"}, 1, "o")
			s.errorStage = "execute"
			referenceRunStep(t, p.ctx, held, backend.recorder, s)
			for i, value := range []any{true, false, nil} {
				referenceRunStep(t, p.ctx, held, backend.recorder, referenceExec(q, 1, i+1, value))
			}
			run(referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}}))
		})
	}
}
