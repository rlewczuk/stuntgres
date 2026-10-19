package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func fdwWriteViewChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
	t.Helper()
	if b.PG {
		run(fixture.Exec("CREATE OR REPLACE FUNCTION fdw_source.rewrite_write() RETURNS trigger LANGUAGE plpgsql AS $$ BEGIN IF NEW.id=46 THEN NEW.label := 'blocked'; ELSE NEW.label := 'actually-stored'; END IF; RETURN NEW; END $$", 0))
	} else {
		r.writer.rewrite = func(row stuntgres.ForeignRow) (stuntgres.ForeignRow, bool) {
			if row[0] == int64(46) {
				row[1] = "blocked"
			} else {
				row[1] = "actually-stored"
			}
			return row, true
		}
		defer func() { r.writer.rewrite = nil }()
	}
	run(fixture.Exec("CREATE VIEW fdw_checked_view AS SELECT id,label,flag FROM fdw_data WHERE label='actually-stored' WITH CASCADED CHECK OPTION", 0))
	// Proposed NEW fails the view predicate, but the genuine stored/returned
	// tuple passes. Checking the proposal would incorrectly reject both actions.
	run(fdwRows("INSERT INTO fdw_checked_view VALUES(45,'proposal',true) RETURNING id,label,flag", [][]any{{int64(45), "actually-stored", true}}))
	run(fixture.Prepared(fdwRows("UPDATE fdw_checked_view SET label='proposal-again' WHERE id=45 RETURNING id,label,flag", [][]any{{int64(45), "actually-stored", true}})))
	want := fdwDiagnostic("44000", `new row violates check option for view "fdw_checked_view"`)
	want.Detail = "Failing row contains (46, blocked, t)."
	// Here the proposal passes and the actual provider-rewritten row fails.
	// The source must retain no INSERT and must preserve UPDATE's original key.
	run(fixture.Prepared(fixture.ErrorDiagnostic("INSERT INTO fdw_checked_view VALUES(46,'actually-stored',true) RETURNING id,label,flag", want)))
	run(fixture.ErrorDiagnostic("UPDATE fdw_checked_view SET id=46 WHERE id=45 RETURNING id,label,flag", want))
	fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=45 OR id=46 ORDER BY id", [][]any{{int64(45), "actually-stored", true}}))
	if !b.PG {
		before := len(r.writer.batches)
		run(fixture.Exec("INSERT INTO fdw_checked_view VALUES(47,'proposal',true),(48,'proposal',false)", 2))
		if len(r.writer.batches) != before {
			t.Fatal("view CHECK OPTION used batch callback")
		}
		fdwWriteSourceRows(t, p, b, r, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=47 OR id=48 ORDER BY id", [][]any{{int64(47), "actually-stored", true}, {int64(48), "actually-stored", false}}))
	}
}
