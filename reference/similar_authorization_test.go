package reference_test

import "testing"

func TestReferenceSimilarAuthorization(t *testing.T) {
	roleReferenceCleanup(t, []string{"similar_reader"})
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		conn, err := b.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(p.ctx, `RESET SESSION AUTHORIZATION`)
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			run(referenceExec(`CREATE USER similar_reader`, 0))
			run(referenceExec(`CREATE TABLE source(id INTEGER,v TEXT,p TEXT,e TEXT)`, 0))
			run(referenceExec(`INSERT INTO source VALUES(1,'a','a','#')`, 1))
			if b.pg {
				run(referenceExec(`GRANT USAGE ON SCHEMA public TO similar_reader`, 0))
			}
			query := `SELECT v SIMILAR TO p ESCAPE e AS matched,substring(v SIMILAR p ESCAPE e) AS piece FROM source`
			stmt, err := conn.PrepareContext(p.ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := referenceHeldStatement{conn, stmt, query}
			for _, empty := range []bool{false, true} {
				if empty {
					run(referenceExec(`DELETE FROM source`, 1))
				}
				run(referenceExec(`SET SESSION AUTHORIZATION similar_reader`, 0))
				for _, q := range []string{query, `SELECT id FROM source WHERE FALSE AND v SIMILAR TO p ESCAPE e`, `SELECT 'a' SIMILAR TO p ESCAPE e FROM source WHERE FALSE`, `SELECT substring('a' SIMILAR p ESCAPE e) FROM source WHERE FALSE`} {
					for _, prepared := range []bool{false, true} {
						s := referenceImplicitError(q, referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
						s.selectRows = true
						s.prepared = prepared
						s.errorStage = "execute"
						run(s)
					}
				}
				s := referenceImplicitError(query, referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
				s.selectRows = true
				s.errorStage = "execute"
				referenceRunStep(t, p.ctx, held, b.recorder, s)
				run(referenceExec(`RESET SESSION AUTHORIZATION`, 0))
			}
			referenceRunStep(t, p.ctx, held, b.recorder, referenceSelect(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, nil))
				run(referenceExec(`GRANT SELECT(id,p) ON source TO similar_reader`, 0))
				run(referenceExec(`SET SESSION AUTHORIZATION similar_reader`, 0))
				s := referenceImplicitError(`SELECT id FROM source WHERE 'a' SIMILAR TO p ESCAPE e`, referenceDiagnostic{Code: "42501", Message: "permission denied for table source"})
				s.selectRows = true
				s.errorStage = "execute"
				run(s)
				run(referencePrepared(s))
				run(referenceExec(`RESET SESSION AUTHORIZATION`, 0))
				run(referenceExec(`GRANT SELECT(e) ON source TO similar_reader`, 0))
				run(referenceExec(`SET SESSION AUTHORIZATION similar_reader`, 0))
				q := `SELECT id FROM source WHERE 'a' SIMILAR TO p ESCAPE e`
				run(referenceSelect(q, []string{"id"}, []string{"INT4"}, nil))
				run(referencePrepared(referenceSelect(q, []string{"id"}, []string{"INT4"}, nil)))
				run(referenceExec(`RESET SESSION AUTHORIZATION`, 0))
		}()
	}
}
