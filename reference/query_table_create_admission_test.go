package reference_test

import (
	"context"
	"database/sql"
	"testing"
)

func TestReferenceQueryTableCreateReadOnlyPrepare(t *testing.T) {
	p := newReferenceScenario(t)
	for _, b := range p.backends() {
		t.Run(b.name, func(t *testing.T) {
			conn, run := queryTableCreateSession(t, p, b)
			tx, err := conn.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer tx.Rollback()
			const q = "CREATE TABLE qc_ro_prepare AS SELECT $1::integer AS i"
			stmt, err := tx.PrepareContext(p.ctx, q)
			if b.pg {
				if err != nil || stmt == nil {
					t.Fatal("read-only Prepare", stmt, err)
				}
				defer stmt.Close()
				want := controlsReferenceDiagnostic("25006", "cannot execute CREATE TABLE AS in a read-only transaction")
				referenceRunStep(t, p.ctx, referenceHeldStatement{referenceExecutor: tx, statement: stmt, query: q}, b.recorder, referenceErrorDiagnostic(q, want, 7))
			} else {
				if stmt != nil {
					stmt.Close()
					t.Fatal("excluded Prepare exposed statement")
				}
				want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
				referenceAssertDiagnostic(t, err, &want)
			}
			referenceRunStep(t, p.ctx, tx, b.recorder, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			if err := tx.Rollback(); err != nil {
				t.Fatal(err)
			}
			queryTableCreateAbsent(t, run, b.pg, "qc_ro_prepare")
			run(referenceSelect("SELECT 1 AS recovered", []string{"recovered"}, []string{"INT4"}, [][]any{{int64(1)}}))
		})
	}
}

func TestReferenceQueryTableCreateReadOnlyLocks(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, b := range p.backends() {
				t.Run(b.name, func(t *testing.T) {
					a, c := referenceLockConnections(t, p.ctx, b.db)
					run := func(x referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, x, b.recorder, s) }
					run(a, referenceExec("CREATE TABLE qc_ro_locked(i INT)", 0))
					run(a, referenceExec("INSERT INTO qc_ro_locked VALUES(7)", 1))
					q := "CREATE TABLE qc_ro_target AS SELECT i FROM qc_ro_locked"
					var stmt *sql.Stmt
					if held {
						var err error
						stmt, err = c.PrepareContext(p.ctx, q)
						if err != nil {
							t.Fatal(err)
						}
						defer stmt.Close()
					}
					run(a, referenceExec("BEGIN", 0))
					defer a.ExecContext(context.Background(), "ROLLBACK")
					run(a, referenceExec("LOCK TABLE qc_ro_locked IN ACCESS EXCLUSIVE MODE", 0))
					run(c, referenceExec("SET lock_timeout='100ms'", 0))
					run(c, referenceExec("BEGIN READ ONLY", 0))
					defer c.ExecContext(context.Background(), "ROLLBACK")
					want := controlsReferenceDiagnostic("25006", "cannot execute CREATE TABLE AS in a read-only transaction")
					if b.pg {
						want = controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						if !held {
							want.Position = "44"
						}
					}
					x := referenceExecutor(c)
					if held {
						x = referenceHeldStatement{referenceExecutor: c, statement: stmt, query: q}
					}
					referenceRunStep(t, p.ctx, x, b.recorder, referenceErrorDiagnostic(q, want))
					run(c, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
					run(c, referenceExec("ROLLBACK", 0))
					run(a, referenceExec("ROLLBACK", 0))
					run(c, referenceExec("RESET lock_timeout", 0))
					queryTableCreateAbsent(t, func(s referenceStep) { run(c, s) }, b.pg, "qc_ro_target")
					run(c, referenceSelect("SELECT i FROM qc_ro_locked", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
				})
			}
		})
	}
}

func TestReferenceQueryTableCreateSnapshotAdmission(t *testing.T) {
	for _, form := range []string{"ctas", "into"} {
		for _, route := range []string{"direct", "prepare", "held"} {
			t.Run(form+"/"+route, func(t *testing.T) {
				p := newReferenceScenario(t)
				for _, b := range p.backends() {
					t.Run(b.name, func(t *testing.T) {
						writer, reader := referenceLockConnections(t, p.ctx, b.db)
						run := func(x referenceExecutor, step referenceStep) { referenceRunStep(t, p.ctx, x, b.recorder, step) }
						run(writer, referenceExec("CREATE TABLE qc_snapshot_source(i INT)", 0))
						run(writer, referenceExec("INSERT INTO qc_snapshot_source VALUES(7)", 1))
						q := "CREATE TABLE qc_snapshot_target AS SELECT 1 AS i"
						tag := "CREATE TABLE AS"
						if form == "into" {
							q = "SELECT 1 AS i INTO qc_snapshot_target"
							tag = "SELECT INTO"
						}
						var statement *sql.Stmt
						if route == "held" {
							var err error
							statement, err = reader.PrepareContext(p.ctx, q)
							if err != nil {
								t.Fatal(err)
							}
							defer statement.Close()
						}
						run(reader, referenceExec("SET default_transaction_deferrable=on", 0))
						run(reader, referenceExec("SET statement_timeout='200ms'", 0))
						writerTx, err := writer.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelSerializable})
						if err != nil {
							t.Fatal(err)
						}
						defer writerTx.Rollback()
						run(writerTx, referenceSelect("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(writerTx, referenceExec("UPDATE qc_snapshot_source SET i=8", 1))
						// The synchronous SELECT and staged UPDATE admitted a genuine
						// unresolved writer before the reader's first query operation.
						tx, err := reader.BeginTx(p.ctx, &sql.TxOptions{Isolation: sql.LevelSerializable, ReadOnly: true})
						if err != nil {
							t.Fatal(err)
						}
						defer tx.Rollback()
						want := controlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
						if route != "direct" {
							want = controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
						}
						if b.pg {
							// PostgreSQL Parse/simple-query analysis requires the initial
							// safe snapshot before utility execution or prepared analysis.
							want = controlsReferenceDiagnostic("57014", "canceling statement due to statement timeout")
						}
						step := referenceErrorDiagnostic(q, want)
						step.errorStage = "execute"
						if route == "held" {
							held := tx.StmtContext(p.ctx, statement)
							defer held.Close()
							run(referenceHeldStatement{referenceExecutor: tx, statement: held, query: q}, step)
						} else {
							if route == "prepare" {
								step.prepared = true
								step.errorStage = "prepare"
							}
							run(tx, step)
						}
						run(tx, referenceErrorDiagnostic("SELECT 1", controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
						if err := tx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(reader, referenceExec("RESET statement_timeout", 0))
						run(reader, referenceExec("RESET default_transaction_deferrable", 0))
						queryTableCreateAbsent(t, func(step referenceStep) { run(reader, step) }, b.pg, "qc_snapshot_target")
						run(reader, referenceSelect("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						run(writerTx, referenceSelect("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(8)}}))
						if err := writerTx.Rollback(); err != nil {
							t.Fatal(err)
						}
						run(reader, referenceSelect("SELECT i FROM qc_snapshot_source", []string{"i"}, []string{"INT4"}, [][]any{{int64(7)}}))
						if route == "held" {
							run(referenceHeldStatement{referenceExecutor: reader, statement: statement, query: q}, referenceExec(q, 1))
						} else {
							run(reader, referencePrepared(referenceExec(q, 1)))
						}
						run(reader, referenceSelect("SELECT i FROM qc_snapshot_target", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}}))
					})
				}
			})
		}
	}
}
