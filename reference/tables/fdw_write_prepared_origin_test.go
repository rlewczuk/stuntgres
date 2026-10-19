package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func fdwWritePreparedOriginReuse(t *testing.T, p *fixture.Pair, b fixture.Backend, origin *sql.Conn, reader *fdwSQLReader) {
	t.Helper()
	finisher, err := b.DB.Conn(p.Ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer finisher.Close()
	for _, entry := range []string{"empty", "rows"} {
		for _, ending := range []string{"COMMIT", "ROLLBACK"} {
			for _, concurrent := range []bool{false, true} {
				name := entry + "-" + strings.ToLower(ending)
				if concurrent {
					name += "-concurrent"
				} else {
					name += "-serial"
				}
				t.Run(name, func(t *testing.T) {
					run := func(x fixture.Executor, step fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, step) }
					local := "fdw_prepared_" + strings.ReplaceAll(name, "-", "_")
					run(origin, fixture.Exec("CREATE TABLE "+local+"(v INTEGER)", 0))
					run(origin, fixture.Exec("BEGIN", 0))
					defer func() {
						if _, err := origin.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
							t.Error("rolling back reused origin", err)
						}
						if ending == "COMMIT" {
							if _, err := origin.ExecContext(p.Ctx, "DELETE FROM fdw_data WHERE id=900"); err != nil {
								t.Error("cleaning committed origin witness", err)
							}
						}
					}()
					if entry == "rows" {
						run(origin, fixture.Exec("INSERT INTO "+local+" VALUES(7)", 1))
					}
					fixture.PreparedReferencePrepare(t, p, b, origin, local)
					run(origin, fixture.Exec("BEGIN", 0))
					run(origin, fdwRows("SELECT id,label,flag FROM fdw_data WHERE id=1", [][]any{{int64(1), "one", true}}))
					run(origin, fixture.Exec("INSERT INTO fdw_data VALUES(900,'origin-pending',true)", 1))
					fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=900", nil))
					commits, rollbacks := reader.writer.commits, reader.writer.rollbacks
					complete := "COMMIT PREPARED " + fixture.PreparedReferenceLiteral(local)
					if concurrent {
						// Independent sessions settle the detached old entry and its
						// reused origin together. No participant state belongs to both.
						start := make(chan struct{})
						type outcome struct {
							command string
							err     error
						}
						done := make(chan outcome, 2)
						go func() { <-start; _, err := finisher.ExecContext(p.Ctx, complete); done <- outcome{complete, err} }()
						go func() { <-start; _, err := origin.ExecContext(p.Ctx, ending); done <- outcome{ending, err} }()
						close(start)
						first, second := <-done, <-done
						if first.err != nil || second.err != nil {
							t.Fatalf("independent completions failed: %s: %v; %s: %v", first.command, first.err, second.command, second.err)
						}
					} else {
						run(finisher, fixture.Exec(complete, 0))
						if !b.PG && (reader.writer.commits != commits || reader.writer.rollbacks != rollbacks) {
							t.Fatal("detached completion settled the new origin participant")
						}
						run(origin, fdwRows("SELECT id,label,flag FROM fdw_data WHERE id=900", [][]any{{int64(900), "origin-pending", true}}))
						fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=900", nil))
						run(origin, fixture.Exec(ending, 0))
					}
					fixture.PreparedReferenceAbsent(t, p, finisher, local)
					var localRows [][]any
					if entry == "rows" {
						localRows = [][]any{{int64(7)}}
					}
					run(finisher, fixture.Select("SELECT v FROM "+local, []string{"v"}, []string{"INT4"}, localRows))
					if !b.PG {
						wantCommits, wantRollbacks := 0, 1
						if ending == "COMMIT" {
							wantCommits, wantRollbacks = 1, 0
						}
						if reader.writer.commits-commits != wantCommits || reader.writer.rollbacks-rollbacks != wantRollbacks {
							t.Fatal("reused origin did not exclusively settle its own participant", reader.writer.commits-commits, reader.writer.rollbacks-rollbacks)
						}
					}
					var remoteRows [][]any
					if ending == "COMMIT" {
						remoteRows = [][]any{{int64(900), "origin-pending", true}}
					}
					fdwWriteSourceRows(t, p, b, reader, fdwRows("SELECT id,label,flag FROM fdw_source.data WHERE id=900", remoteRows))
				})
			}
		}
	}
}
