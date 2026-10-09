package reference_test

import (
	"database/sql"
	"testing"
)

func TestReferenceBtreeIndexExplicitAdmissionRegression(t *testing.T) {
	for _, nonContext := range []bool{false, true} {
		t.Run(map[bool]string{false: "context setup", true: "original setup"}[nonContext], func(t *testing.T) {
			for _, readOnly := range []bool{false, true} {
				for _, prepared := range []bool{false, true} {
					for _, target := range []string{"missing", "denied", "writer-blocked"} {
						label := map[bool]string{false: "writable", true: "read-only"}[readOnly] + "/" + map[bool]string{false: "direct", true: "held-prepared"}[prepared] + "/" + target
						t.Run(label, func(t *testing.T) {
							roleReferenceCleanup(t, []string{"reader"})
							p := newReferenceScenario(t)
							for _, backend := range p.backends() {
								t.Run(backend.name, func(t *testing.T) {
									run := func(s referenceStep) {
										s.nonContext = nonContext
										referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
									}
									run(referenceExec("CREATE TABLE t(n INT)", 0))
									run(referenceExec("INSERT INTO t VALUES(1)", 1))
									writer, builder := referenceLockConnections(t, p.ctx, backend.db)
									if target == "denied" {
										referenceLockExec(t, p.ctx, builder, "CREATE USER reader", 0)
										if backend.pg {
											referenceLockExec(t, p.ctx, builder, "GRANT USAGE ON SCHEMA public TO reader", 0)
										}
										referenceLockExec(t, p.ctx, builder, "SET ROLE reader", 0)
										defer builder.ExecContext(p.ctx, "RESET ROLE")
									}
									if target == "writer-blocked" {
										blocker, err := writer.BeginTx(p.ctx, nil)
										if err != nil {
											t.Fatal(err)
										}
										defer blocker.Rollback()
										referenceLockExec(t, p.ctx, blocker, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
									}
									referenceLockExec(t, p.ctx, builder, "SET lock_timeout='20ms'", 0)
									query := "CREATE INDEX idx ON t(n)"
									if target == "missing" {
										query = "CREATE INDEX idx ON missing(n)"
									}
									var held *sql.Stmt
									if prepared {
										var err error
										held, err = builder.PrepareContext(p.ctx, query)
										if err != nil {
											t.Fatal("syntax-only held Prepare", err)
										}
										defer held.Close()
									}
									tx, err := builder.BeginTx(p.ctx, &sql.TxOptions{ReadOnly: readOnly})
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									if prepared {
										active := tx.StmtContext(p.ctx, held)
										_, err = active.ExecContext(p.ctx)
										active.Close()
									} else {
										_, err = tx.ExecContext(p.ctx, query)
									}
									want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
									if readOnly {
										want = controlsReferenceDiagnostic("25006", "cannot execute CREATE INDEX in a read-only transaction")
									} else if backend.pg {
										switch target {
										case "missing":
											want = controlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
										case "denied":
											want = controlsReferenceDiagnostic("42501", "must be owner of table t")
										case "writer-blocked":
											want = controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
										}
									}
									referenceAssertDiagnostic(t, err, &want)
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
									run(referenceSelect("SELECT relname FROM pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
									run(referenceSelect("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
								})
							}
						})
					}
				}
			}
		})
	}
}
