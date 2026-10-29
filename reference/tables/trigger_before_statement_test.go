package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// This half of ViewForeign belongs to the shipped write participant's fixture;
// it is selected by exactly the same public family selector as the view half.
func TestReferenceTriggerBeforeStatementViewForeign(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "held"
		}
		t.Run(name, func(t *testing.T) {
			fdwWriteScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, reader *fdwSQLReader, run func(fixture.Step)) {
				fdwLive(t, p, b, reader, run)
				run(fixture.Exec("CREATE TABLE bst_foreign_log(seq integer GENERATED ALWAYS AS IDENTITY,op text,n bigint)", 0))
				run(fixture.Exec("CREATE FUNCTION bst_foreign() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN INSERT INTO bst_foreign_log(op,n) VALUES(TG_OP,(SELECT count(*) FROM fdw_data)); RETURN NULL; END$$", 0))
				run(fixture.Exec("CREATE TRIGGER s BEFORE INSERT OR UPDATE OR DELETE ON fdw_data EXECUTE FUNCTION bst_foreign()", 0))
				held := make(map[string]*sql.Stmt)
				defer func() {
					for _, h := range held {
						h.Close()
					}
				}()
				modify := func(step fixture.Step) {
					var x fixture.Executor = c
					if prepared {
						h := held[step.Query]
						if h == nil {
							var err error
							h, err = c.PrepareContext(p.Ctx, step.Query)
							if err != nil {
								t.Fatal(err)
							}
							held[step.Query] = h
						}
						x = fixture.HeldStatement{Executor: c, Statement: h, SQL: step.Query}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, step)
				}
				modify(fixture.Exec("INSERT INTO fdw_data VALUES($1,'new',true)", 1, 4))
				modify(fixture.Exec("INSERT INTO fdw_data VALUES($1,'new',true)", 1, 5))
				modify(fixture.Exec("UPDATE fdw_data SET label='changed' WHERE id>3", 2))
				modify(fixture.Exec("DELETE FROM fdw_data WHERE id>3", 2))
				modify(fixture.Exec("UPDATE fdw_data SET label=label WHERE false", 0))
				modify(fixture.Exec("DELETE FROM fdw_data WHERE false", 0))
				run(fixture.Select("SELECT op,n FROM bst_foreign_log ORDER BY seq", []string{"op", "n"}, []string{"TEXT", "INT8"}, [][]any{{"INSERT", int64(3)}, {"INSERT", int64(4)}, {"UPDATE", int64(5)}, {"DELETE", int64(5)}, {"UPDATE", int64(3)}, {"DELETE", int64(3)}}))
				run(fixture.Exec("CREATE FUNCTION bst_foreign_fail() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN PERFORM 1/0; RETURN NULL; END$$", 0))
				run(fixture.Exec("CREATE TRIGGER z BEFORE INSERT ON fdw_data EXECUTE FUNCTION bst_foreign_fail()", 0))
				d := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
				d.Context = "SQL statement \"SELECT 1/0\"\nPL/pgSQL function bst_foreign_fail() line 1 at PERFORM"
				modify(fixture.ErrorDiagnostic("INSERT INTO fdw_data VALUES(6,'failure',false)", d))
				want := [][]any{{int64(1), "one", true}, {int64(2), nil, false}, {int64(3), "three", nil}}
				run(fdwRows("SELECT id,label,flag FROM fdw_data ORDER BY id", want))
				fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data ORDER BY id", want))
				run(fixture.Select("SELECT count(*) AS n FROM bst_foreign_log", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
			})
		})
	}
}
