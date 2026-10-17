package reference_test

import (
	"context"
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryTableCreateReadOnlyPrepare(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			tx, err := conn.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			const q = "CREATE TABLE qc_ro_prepare AS SELECT $1::integer AS i"
			stmt, err := tx.PrepareContext(p.Ctx, q)
			if b.PG {
				if err != nil || stmt == nil {
					t.Fatal("read-only Prepare", stmt, err)
				}
				defer stmt.Close()
				want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute CREATE TABLE AS in a read-only transaction")
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: tx, Statement: stmt, SQL: q}, b.Recorder, fixture.ErrorDiagnostic(q, want, 7))
			} else {
				if stmt != nil {
					stmt.Close()
					t.Fatal("excluded Prepare exposed statement")
				}
				want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
				fixture.AssertDiagnostic(t, err, &want)
			}
			fixture.RunStep(t, p.Ctx, tx, b.Recorder, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			fixture.QueryTableCreateAbsent(t, run, "qc_ro_prepare")
			run(fixture.Select("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceQueryTableCreateReadOnlyLocks(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					a, c := fixture.LockConnections(t, p.Ctx, b.DB)
					run := func(x fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, s) }
					run(a, fixture.Exec("CREATE TABLE qc_ro_locked(i INT)", 0))
					run(a, fixture.Exec("INSERT INTO qc_ro_locked VALUES(7)", 1))
					q := "CREATE TABLE qc_ro_target AS SELECT i FROM qc_ro_locked"
					var stmt *sql.Stmt
					if held {
						var err error
						stmt, err = c.PrepareContext(p.Ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					run(a, fixture.Exec("BEGIN", 0))
					defer a.ExecContext(context.Background(), "ROLLBACK")
					run(a, fixture.Exec("LOCK TABLE qc_ro_locked IN ACCESS EXCLUSIVE MODE", 0))
					run(c, fixture.Exec("SET lock_timeout='100ms'", 0))
					run(c, fixture.Exec("BEGIN READ ONLY", 0))
					defer c.ExecContext(context.Background(), "ROLLBACK")
					want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute CREATE TABLE AS in a read-only transaction")
					if b.PG {
						want = fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						if !held {
							want.Position = "44"
						}
					}
					x := fixture.Executor(c)
					if held {
						x = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
					}
					fixture.RunStep(t, p.Ctx, x, b.Recorder, fixture.ErrorDiagnostic(q, want))
					run(c, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(c, fixture.Exec("ROLLBACK", 0))
					run(a, fixture.Exec("ROLLBACK", 0))
					run(c, fixture.Exec("RESET lock_timeout", 0))
					fixture.QueryTableCreateAbsent(t, func(s fixture.Step) { run(c, s) }, "qc_ro_target")
					run(c, fixture.Select("SELECT i FROM qc_ro_locked", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

func TestReferenceQueryTableCreateSnapshotAdmission(t *testing.T) {
	for _, form := range []string{"ctas", "into"} {
		for _, route := range []string{"direct", "prepare", "held"} {
			t.Run(form+"/"+route, func(t *testing.T) {
				p := fixture.NewScenario(t)
				for _, b := range p.Backends() {
					t.Run(b.Name, func(t *testing.T) {
						writer, reader := fixture.LockConnections(t, p.Ctx, b.DB)
						run := func(x fixture.Executor, step fixture.Step) { fixture.RunStep(t, p.Ctx, x, b.Recorder, step) }
						run(writer, fixture.Exec("CREATE TABLE qc_snapshot_source(i INT)", 0))
						run(writer, fixture.Exec("INSERT INTO qc_snapshot_source VALUES(7)", 1))
						q := "CREATE TABLE qc_snapshot_target AS SELECT 1 AS i"
						tag := "CREATE TABLE AS"
						if form == "into" {
							q = "SELECT 1 AS i INTO qc_snapshot_target"
							tag = "SELECT INTO"
						}
						var statement *sql.Stmt
						if route == "held" {
							var err error
							statement, err = reader.PrepareContext(p.Ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
						}
						run(reader, fixture.Exec("SET default_transaction_deferrable=on", 0))
						run(reader, fixture.Exec("SET statement_timeout='200ms'", 0))
						writerTx, err := writer.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
						if err != nil {
							t.Fatal(err)
						}
						defer writerTx.Rollback()
						run(writerTx, fixture.Select("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(writerTx, fixture.Exec("UPDATE qc_snapshot_source SET i=8", 1))
						// The synchronous SELECT and staged UPDATE admitted a genuine
						// unresolved writer before the reader's first query operation.
						tx, err := reader.BeginTx(p.Ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
						if route != "direct" {
							want = fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
						}
						if b.PG {
							// PostgreSQL Parse/simple-query analysis requires the initial
							// safe snapshot before utility execution or prepared analysis.
							want = fixture.ControlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
						}
						step := fixture.ErrorDiagnostic(q, want)
						step.ErrorStage = "execute"
						if route == "held" {
							held := tx.StmtContext(p.Ctx, statement)
							defer held.Close()
							run(fixture.HeldStatement{Executor: tx, Statement: held, SQL: q}, step)
						} else {
							if route == "prepare" {
								step.Prepared = true
								step.ErrorStage = "prepare"
							}
							run(tx, step)
						}
						run(tx, fixture.ErrorDiagnostic("SELECT 1", fixture.ControlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(reader, fixture.Exec("RESET statement_timeout", 0))
						run(reader, fixture.Exec("RESET default_transaction_deferrable", 0))
						fixture.QueryTableCreateAbsent(t, func(step fixture.Step) { run(reader, step) }, "qc_snapshot_target")
						run(reader, fixture.Select("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(writerTx, fixture.Select("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}))
						if err := writerTx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(reader, fixture.Select("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if route == "held" {
							run(fixture.HeldStatement{Executor: reader, Statement: statement, SQL: q}, fixture.Exec(q, 1))
						} else {
							run(reader, fixture.Prepared(fixture.Exec(q, 1)))
						}
						run(reader, fixture.Select("SELECT i FROM qc_snapshot_target", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
