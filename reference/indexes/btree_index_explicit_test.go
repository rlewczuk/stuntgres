package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
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
							fixture.RoleReferenceCleanup(t, []string{"reader"})
							p := fixture.NewScenario(t)
							for _, backend := range p.Backends() {
								t.Run(backend.Name, func(t *testing.T) {
									run := func(s fixture.Step) {
										s.NonContext = nonContext
										fixture.RunStep(t, p.Ctx, backend.DB, backend.Recorder, s)
									}
									run(fixture.Exec("CREATE TABLE t(n INT)", 0))
									run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
									writer, builder := fixture.LockConnections(t, p.Ctx, backend.DB)
									if target == "denied" {
										fixture.LockExec(t, p.Ctx, builder, "CREATE USER reader", 0)
										if backend.PG {
											fixture.LockExec(t, p.Ctx, builder, "GRANT USAGE ON SCHEMA public TO reader", 0)
										}
										fixture.LockExec(t, p.Ctx, builder, "SET ROLE reader", 0)
										defer builder.ExecContext(p.Ctx, "RESET ROLE")
									}
									if target == "writer-blocked" {
										blocker, err := writer.BeginTx(p.Ctx, nil)
										if err != nil {
											t.Fatal(err)
										}
										defer blocker.Rollback()
										fixture.LockExec(t, p.Ctx, blocker, "LOCK TABLE t IN ROW EXCLUSIVE MODE", 0)
									}
									fixture.LockExec(t, p.Ctx, builder, "SET lock_timeout='20ms'", 0)
									query := "CREATE INDEX idx ON t(n)"
									if target == "missing" {
										query = "CREATE INDEX idx ON missing(n)"
									}
									var held *sql.Stmt
									if prepared {
										var err error
										held, err = builder.PrepareContext(p.Ctx, query)
										if err != nil {
											t.Fatal("syntax-only held Prepare", err)
										}
										defer held.Close()
									}
									tx, err := builder.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: readOnly})
									if err != nil {
										t.Fatal(err)
									}
									defer tx.Rollback()
									if prepared {
										active := tx.StmtContext(p.Ctx, held)
										_, err = active.ExecContext(p.Ctx)
										active.Close()
									} else {
										_, err = tx.ExecContext(p.Ctx, query)
									}
									want := fixture.ControlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
									if readOnly {
										want = fixture.ControlsReferenceDiagnostic("25006", "cannot execute CREATE INDEX in a read-only transaction")
									} else if backend.PG {
										switch target {
										case "missing":
											want = fixture.ControlsReferenceDiagnostic("42P01", `relation "missing" does not exist`)
										case "denied":
											want = fixture.ControlsReferenceDiagnostic("42501", "must be owner of table t")
										case "writer-blocked":
											want = fixture.ControlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
										}
									}
									fixture.AssertDiagnostic(t, err, &want)
									if err := tx.Rollback(); err != nil {
										t.Fatal(err)
									}
									run(fixture.Select("SELECT relname FROM pg_class WHERE relname='idx'", []string{"relname"}, []string{"NAME"}, nil))
									run(fixture.Select("SELECT n FROM t", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
								})
							}
						})
					}
				}
			}
		})
	}
}
