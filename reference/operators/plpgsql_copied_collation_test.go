package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePLpgSQLCoreCopiedCollation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "pl_copy_saved")
	plpgsqlScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		userOperatorExec(t, p, b, c,
			"CREATE SCHEMA pl_copy_saved",
			"CREATE TABLE pl_copy_saved.source(s text COLLATE pg_catalog.pg_unicode_fast)",
			"CREATE FUNCTION pl_copy_saved.upper_copy() RETURNS text LANGUAGE plpgsql AS $$DECLARE x pl_copy_saved.source.s%TYPE := 'Straße'; BEGIN RETURN upper(x); END$$")
		q := "SELECT pl_copy_saved.upper_copy() AS v"
		held, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		sourceRound, postgresReplay := 0, 0
		check := func(conn *sql.Conn, backend fixture.Backend) {
			phase := ""
			if conn == c {
				phase = fmt.Sprintf("source_round_%d", sourceRound)
				sourceRound++
			} else {
				postgresReplay++
				phase = fmt.Sprintf("postgres_replay_%d", postgresReplay)
			}
			if !t.Run(phase, func(t *testing.T) {
				plpgsqlValue(t, p, backend, conn, q, "TEXT", "STRASSE")
				if conn == c {
					// Retain this original statement through both native replacements.
					for range 2 {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"STRASSE"}}))
					}
				} else {
					replayed, err := conn.PrepareContext(p.Ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					defer replayed.Close()
					for range 2 {
						fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: replayed, SQL: q}, backend.Recorder, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"STRASSE"}}))
					}
				}
			}) {
				t.FailNow()
			}
		}
		parametersReplay(t, p, b, c, "pl_copy_saved", check)
	})
}
