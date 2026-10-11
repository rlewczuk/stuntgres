package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSimilarAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"similar_reader"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		conn, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		func() {
			defer conn.Close()
			defer conn.ExecContext(p.Ctx, `RESET SESSION AUTHORIZATION`)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, b.Recorder, s) }
			run(fixture.Exec(`CREATE USER similar_reader`, 0))
			run(fixture.Exec(`CREATE TABLE source(id INTEGER,v TEXT,p TEXT,e TEXT)`, 0))
			run(fixture.Exec(`INSERT INTO source VALUES(1,'a','a','#')`, 1))
			if b.PG {
				run(fixture.Exec(`GRANT USAGE ON SCHEMA public TO similar_reader`, 0))
			}
			query := `SELECT v SIMILAR TO p ESCAPE e AS matched,substring(v SIMILAR p ESCAPE e) AS piece FROM source`
			stmt, err := conn.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{conn, stmt, query}
			for _, empty := range []bool{false, true} {
				if empty {
					run(fixture.Exec(`DELETE FROM source`, 1))
				}
				run(fixture.Exec(`SET SESSION AUTHORIZATION similar_reader`, 0))
				for _, q := range []string{query, `SELECT id FROM source WHERE FALSE AND v SIMILAR TO p ESCAPE e`, `SELECT 'a' SIMILAR TO p ESCAPE e FROM source WHERE FALSE`, `SELECT substring('a' SIMILAR p ESCAPE e) FROM source WHERE FALSE`} {
					for _, prepared := range []bool{false, true} {
						s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
						s.SelectRows = true
						s.Prepared = prepared
						s.ErrorStage = "execute"
						run(s)
					}
				}
				s := fixture.ImplicitError(query, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
				s.SelectRows = true
				s.ErrorStage = "execute"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Select(query, []string{"matched", "piece"}, []string{"BOOL", "TEXT"}, nil))
			run(fixture.Exec(`GRANT SELECT(id,p) ON source TO similar_reader`, 0))
			run(fixture.Exec(`SET SESSION AUTHORIZATION similar_reader`, 0))
			s := fixture.ImplicitError(`SELECT id FROM source WHERE 'a' SIMILAR TO p ESCAPE e`, fixture.Diagnostic{Code: "42501", Message: "permission denied for table source"})
			s.SelectRows = true
			s.ErrorStage = "execute"
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
			run(fixture.Exec(`GRANT SELECT(e) ON source TO similar_reader`, 0))
			run(fixture.Exec(`SET SESSION AUTHORIZATION similar_reader`, 0))
			q := `SELECT id FROM source WHERE 'a' SIMILAR TO p ESCAPE e`
			run(fixture.Select(q, []string{"id"}, []string{"INT4"}, nil))
			run(fixture.Prepared(fixture.Select(q, []string{"id"}, []string{"INT4"}, nil)))
			run(fixture.Exec(`RESET SESSION AUTHORIZATION`, 0))
		}()
	}
}
