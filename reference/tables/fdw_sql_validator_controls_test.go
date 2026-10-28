package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwCheckerObjects(run func(fixture.Step)) {
	run(fixture.Exec(fdwCheckerDefinition, 0))
	run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_checker OPTIONS(mode 'good')", 0))
	run(fixture.Prepared(fixture.Exec("CREATE SERVER fdw_server FOREIGN DATA WRAPPER fdw_meta OPTIONS(mode 'good')", 0)))
	run(fixture.Exec("CREATE USER MAPPING FOR PUBLIC SERVER fdw_server OPTIONS(mode 'good')", 0))
	run(fixture.Prepared(fixture.Exec("CREATE FOREIGN TABLE fdw_checked(id integer OPTIONS(mode 'good')) SERVER fdw_server OPTIONS(mode 'good')", 0)))
}

func fdwCheckerMetadata(run func(fixture.Step), prepared bool, serverOption string) {
	for _, s := range []fixture.Step{
		fixture.Select("SELECT w.fdwname,w.fdwvalidator=p.oid AS same_validator,w.fdwhandler=0 AS no_handler,w.fdwoptions::text AS options FROM pg_foreign_data_wrapper w JOIN pg_proc p ON p.proname='fdw_checker' WHERE w.fdwname='fdw_meta'", []string{"fdwname", "same_validator", "no_handler", "options"}, []string{"NAME", "BOOL", "BOOL", "TEXT"}, [][]any{{"fdw_meta", true, true, "{mode=good}"}}),
		fixture.Select("SELECT s.srvname,w.fdwname,s.srvoptions::text AS options FROM pg_foreign_server s JOIN pg_foreign_data_wrapper w ON w.oid=s.srvfdw WHERE s.srvname='fdw_server'", []string{"srvname", "fdwname", "options"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"fdw_server", "fdw_meta", serverOption}}),
		fixture.Select("SELECT umuser=0 AS public_mapping,umoptions::text AS options FROM pg_user_mapping WHERE umserver=(SELECT oid FROM pg_foreign_server WHERE srvname='fdw_server')", []string{"public_mapping", "options"}, []string{"BOOL", "TEXT"}, [][]any{{true, "{mode=good}"}}),
		fixture.Select("SELECT c.relname,s.srvname,f.ftoptions::text AS options FROM pg_foreign_table f JOIN pg_class c ON c.oid=f.ftrelid JOIN pg_foreign_server s ON s.oid=f.ftserver WHERE c.relname='fdw_checked'", []string{"relname", "srvname", "options"}, []string{"NAME", "NAME", "TEXT"}, [][]any{{"fdw_checked", "fdw_server", "{mode=good}"}}),
		fixture.Select("SELECT attname,attfdwoptions::text AS options FROM pg_attribute WHERE attrelid='fdw_checked'::regclass::oid AND attnum>0 ORDER BY attnum", []string{"attname", "options"}, []string{"NAME", "TEXT"}, [][]any{{"id", "{mode=good}"}}),
	} {
		s.Prepared = prepared
		run(s)
	}
}

func TestReferenceFDWSQLValidatorControls(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwCheckerObjects(run)
		for _, prepared := range []bool{false, true} {
			fdwCheckerMetadata(run, prepared, "{mode=good}")
		}
		if b.PG {
			return
		}
		before := r.snapshot()
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal("SQL validator dependency replay", err)
		}
		fdwCheckerMetadata(run, true, "{mode=good}")
		run(fixture.Exec("CREATE TABLE fdw_sentinel(v integer)", 0))
		run(fixture.Exec("INSERT INTO fdw_sentinel VALUES(91),(92)", 2))
		q := "SELECT v FROM fdw_sentinel ORDER BY v"
		held := fdwHeld(t, p, b, c, q)
		// This stream first recreates the entire valid graph, then fails through
		// its restored SQL validator. Nothing from the candidate may publish.
		invalid := dump.String() + "; CREATE SERVER fdw_rejected FOREIGN DATA WRAPPER fdw_meta OPTIONS(mode 'bad');"
		fixture.AssertControlDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(invalid)), fdwCheckerDiagnostic())
		run(fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}, {int64(92)}}))
		fixture.RunStep(t, p.Ctx, held, held.recorder, fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}, {int64(92)}}))
		fdwCheckerMetadata(run, false, "{mode=good}")
		// Existing options become invalid without being revalidated on installation.
		// Dump must collect declarations, not execute the newly installed checker.
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta NO VALIDATOR", 0))
		run(fixture.Exec("ALTER SERVER fdw_server OPTIONS(SET mode 'bad')", 0))
		run(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_meta VALIDATOR fdw_checker", 0))
		var rejected bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &rejected); err != nil {
			t.Fatal("Dump invoked SQL validator", err)
		}
		fixture.AssertControlDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(rejected.String())), fdwCheckerDiagnostic())
		run(fixture.Select(q, []string{"v"}, []string{"INT4"}, [][]any{{int64(91)}, {int64(92)}}))
		fdwCheckerMetadata(run, false, "{mode=bad}")
		if after := r.snapshot(); after != before {
			t.Fatal("SQL-only controls invoked a remote provider", before, after)
		}
	})
}

func TestReferenceFDWSQLValidatorSmoke(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwCheckerObjects(run)
		run(fixture.Prepared(fixture.Exec("ALTER SERVER fdw_server OPTIONS(SET mode 'ready')", 0)))
		prove := func() {
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("ALTER SERVER fdw_server OPTIONS(SET mode 'bad')", fdwCheckerDiagnostic())
				s.Prepared, s.ErrorStage = prepared, "execute"
				run(s)
				fdwCheckerMetadata(run, prepared, "{mode=ready}")
				for _, q := range []string{"SELECT id FROM fdw_checked", "INSERT INTO fdw_checked VALUES(1)"} {
					boundary := fixture.ErrorDiagnostic(q, fdwDiagnostic("55000", `foreign-data wrapper "fdw_meta" has no handler`))
					boundary.Prepared = prepared
					boundary.SelectRows = strings.HasPrefix(q, "SELECT")
					run(boundary)
				}
			}
		}
		prove()
		if b.PG {
			return
		}
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		prove()
	})
}
