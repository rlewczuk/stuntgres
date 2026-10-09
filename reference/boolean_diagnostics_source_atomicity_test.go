package reference_test

import "testing"

func TestReferenceBooleanDiagnosticsSourceAtomicity(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		t.Run(backend.name, func(t *testing.T) {
			run := func(s referenceStep) {
				s.nonContext = true
				referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
			}
			run(referenceExec("CREATE TABLE b(id INT,flag BOOL)", 0))
			run(referenceExec("INSERT INTO b VALUES (99,TRUE)", 1))
			sentinel := referenceSelect("SELECT id,flag FROM b", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(99), true}})
			for _, query := range []bool{false, true} {
				var err error
				if query {
					rows, e := backend.db.Query("INSERT INTO b VALUES (1,TRUE),(2,'o')")
					err = e
					if rows != nil {
						rows.Close()
						t.Fatal("failed Query exposed rows")
					}
				} else {
					result, e := backend.db.Exec("INSERT INTO b VALUES (1,TRUE),(2,'o')")
					err = e
					if result != nil {
						t.Fatal("failed Exec exposed result")
					}
				}
				want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
				want.Position = "34"
				booleanWrappedDiagnostic(t, err, want)
				run(sentinel)
				meta := referenceSelect("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}})
				rows, err := backend.db.Query(meta.query)
				if err != nil {
					t.Fatal(err)
				}
				lexicalRows(t, backend.pg, rows, meta, nil)
			}
			literalStmt, literalErr := backend.db.PrepareContext(p.ctx, "INSERT INTO b VALUES (1,TRUE),(2,'o')")
			if literalStmt != nil {
				literalStmt.Close()
				t.Fatal("invalid literal exposed a statement")
			}
			literalWant := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
			literalWant.Position = "34"
			booleanWrappedDiagnostic(t, literalErr, literalWant)
			run(sentinel)
			meta := referenceSelect("SELECT flag FROM b", []string{"flag"}, []string{"BOOL"}, [][]any{{true}})
			metaRows, metaErr := backend.db.Query(meta.query)
			if metaErr != nil {
				t.Fatal(metaErr)
			}
			lexicalRows(t, backend.pg, metaRows, meta, nil)
			q := "INSERT INTO b VALUES (1,TRUE),(2,$1)"
			want := controlsReferenceDiagnostic("22P02", `invalid input syntax for type boolean: "o"`)
			want.Context = "unnamed portal parameter $1 = '...'"
			for _, query := range []bool{false, true} {
				var err error
				if query {
					rows, e := backend.db.Query(q, "o")
					err = e
					if rows != nil {
						rows.Close()
						t.Fatal("failed Query exposed rows")
					}
				} else {
					result, e := backend.db.Exec(q, "o")
					err = e
					if result != nil {
						t.Fatal("failed Exec exposed result")
					}
				}
				booleanWrappedDiagnostic(t, err, want)
				run(sentinel)
			}
			stmt, err := backend.db.PrepareContext(p.ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			result, err := stmt.Exec("o")
			if result != nil {
				stmt.Close()
				t.Fatal("failed held Exec exposed result")
			}
			booleanWrappedDiagnostic(t, err, want)
			stmt.Close()
			run(sentinel)
			stmt, err = backend.db.Prepare("INSERT INTO b VALUES ($1,$2)")
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			result, err = stmt.Exec(1, "o")
			if result != nil {
				t.Fatal("failed reused statement exposed result")
			}
			want.Context = "unnamed portal parameter $2 = '...'"
			booleanWrappedDiagnostic(t, err, want)
			run(sentinel)
			for i, value := range []any{true, false, nil} {
				result, err = stmt.Exec(i+1, value)
				if err != nil {
					t.Fatal(err)
				}
				if n, e := result.RowsAffected(); e != nil || n != 1 {
					t.Fatalf("affected %d: %v", n, e)
				}
				if _, e := result.LastInsertId(); e == nil {
					t.Fatal("LastInsertId unexpectedly supported")
				}
			}
			run(referenceSelect("SELECT id,flag FROM b ORDER BY id", []string{"id", "flag"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), nil}, {int64(99), true}}))
		})
	}
}
