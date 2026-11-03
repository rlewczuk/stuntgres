package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceStatDatabaseCommandOutcomes(t *testing.T) {
	statDatabaseScenario(t, "commands", func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
		target := create("target")
		admissionBase := statDatabaseSetup(t, p, target, observer)
		writer, retire := statDatabaseConn(t, p, target)
		base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, 0)
		// Successfully parsed empty simple Q, idle COMMIT and idle ROLLBACK are
		// genuine backend command transactions, unlike physical idle cleanup.
		// EmptyQueryResponse carries no affected-row count in pq.
		if _, err := writer.ExecContext(p.Ctx, " ; -- an actual empty SQL command\n"); err != nil {
			t.Fatal(err)
		}
		statsExec(t, p, target, writer, "COMMIT", 0)
		statsExec(t, p, target, writer, "ROLLBACK", 0)
		statsExec(t, p, target, writer, "PREPARE TRANSACTION 'stat_db_idle_warning'", 0)
		_, err := writer.ExecContext(p.Ctx, "SELEC 1")
		fixture.State(t, err, "42601")
		stmt, err := writer.PrepareContext(p.Ctx, "SELECT id FROM stat_db_rows")
		if err != nil {
			t.Fatal(err)
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		// Neither driver accepts this Go value into SQL command processing.
		if _, err := writer.ExecContext(p.Ctx, "SELECT $1", make(chan int)); err == nil {
			t.Fatal("unsupported Go argument admitted")
		}
		retire()
		got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Rollback == base.Rollback+2 })
		statDatabaseWantRetiredDelta(t, base, got, 4, 2, 0, 0, 0, 0, 0, 0, 0)
	})
}
func TestReferenceStatDatabasePreparedOutcomes(t *testing.T) {
	for _, commit := range []bool{false, true} {
		label := "rollback"
		if commit {
			label = "commit"
		}
		t.Run(label, func(t *testing.T) {
			statDatabaseScenario(t, "prepared_outcomes_"+label, func(t *testing.T, p *fixture.Pair, b fixture.Backend, observer *sql.Conn, create func(string) fixture.Backend) {
				target := create("target")
				admissionBase := statDatabaseSetup(t, p, target, observer)
				writer, retire := statDatabaseConn(t, p, target)
				base := statDatabaseWriterBaseline(t, p, target, observer, writer, admissionBase, 0)
				fixture.PreparedReferenceCleanup(t, target, "stat_database_prepared")
				statsExec(t, p, target, writer, "BEGIN", 0)
				statsExec(t, p, target, writer, "INSERT INTO stat_db_rows VALUES(1)", 1)
				statsExec(t, p, target, writer, "PREPARE TRANSACTION 'stat_database_prepared'", 0)
				retire()
				beforeFinish := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Sessions == base.Sessions })
				if beforeFinish.Insert != base.Insert || beforeFinish.Commit != base.Commit || beforeFinish.Rollback != base.Rollback {
					t.Fatal("PREPARE transferred unresolved facts", base, beforeFinish)
				}
				finisher, end := statDatabaseConn(t, p, target)
				finishBase := statDatabaseWriterBaseline(t, p, target, observer, finisher, beforeFinish, 0)
				q := "ROLLBACK PREPARED 'stat_database_prepared'"
				if commit {
					q = "COMMIT PREPARED 'stat_database_prepared'"
				}
				statsExec(t, p, target, finisher, q, 0)
				end()
				got := statDatabaseAwait(t, p, observer, target.Name, func(v statDatabaseVector) bool { return v.Backends == 0 && v.Insert == finishBase.Insert+1 })
				// Released FinishPreparedTransaction explicitly reports the original
				// outcome after removal. The successful finisher separately commits.
				commits, rollbacks := int64(1), int64(1)
				if commit {
					commits, rollbacks = 2, 0
				}
				statDatabaseWantRetiredDelta(t, finishBase, got, commits, rollbacks, 1, 0, 0, 0, 0, 0, 0)
			})
		})
	}
}
