package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRowLockViewAggregateComposition(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			query     string
			want      []int64
			blockedID string
			pgLocks   bool
		}{
			{"SELECT count(*) AS n FROM stored WHERE id=1", []int64{1}, "1", true},
			{"SELECT count(*) AS n FROM stored", []int64{3}, "2", true},
			{"SELECT n FROM grouped ORDER BY id", []int64{1, 1, 1}, "3", true},
			{"SELECT n FROM hidden", nil, "2", false},
			{"SELECT n FROM grouped WHERE FALSE", nil, "2", false},
			{"SELECT n FROM runtime_hidden", nil, "2", true},
			{"SELECT n FROM grouped WHERE n=0", nil, "2", true},
			{"SELECT count(*) AS n FROM grouped", []int64{3}, "2", true},
			{"SELECT n FROM grouped", []int64{2}, "1", true},
		} {
			t.Run(tc.query+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := referenceViewFixture(t)
				grouped := "CREATE VIEW grouped AS SELECT id,count(*) AS n FROM stored GROUP BY id"
				subset := tc.query == "SELECT n FROM grouped"
				if subset {
					grouped = "CREATE VIEW grouped AS SELECT count(*) AS n FROM stored WHERE id<3"
				}
				for _, step := range []fixture.Step{fixture.Exec("CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", 0), fixture.Exec(grouped, 0), fixture.Exec("CREATE VIEW hidden AS SELECT count(*) AS n FROM stored HAVING FALSE", 0)} {
					p.Step(t, step)
				}
				p.Step(t, fixture.Exec("CREATE VIEW runtime_hidden AS SELECT count(*) AS n FROM stored HAVING count(*)=0", 0))
				values := make([][]any, len(tc.want))
				for i, value := range tc.want {
					values[i] = []any{value}
				}
				step := fixture.Select(tc.query, []string{"n"}, []string{"INT8"}, values)
				step.Prepared = prepared
				p.Step(t, step)
				for _, backend := range p.Backends() {
					t.Run(backend.Name, func(t *testing.T) {
						a, b := fixture.LockConnections(t, p.Ctx, backend.DB)
						holder, err := a.BeginTx(p.Ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						fixture.RunStep(t, p.Ctx, holder, backend.Recorder, step)
						if tc.blockedID == "1" && !subset {
							fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
							fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						}
						if subset {
							fixture.RowAffected(t, p.Ctx, b, "UPDATE t SET v=31 WHERE id=3", prepared, 1)
							if !backend.PG {
								fixture.ViewNativeBlocked(t, p.Ctx, b, "DELETE FROM t WHERE id=2", prepared)
							} else {
								ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
								fixture.LockExec(t, p.Ctx, b, "SET lock_timeout='100ms'", 0)
								await := fixture.LockOperation(t, p.Ctx, func(ctx context.Context) fixture.RelationOutcome {
									return fixture.RowExecute(ctx, b, "DELETE FROM t WHERE id=2", prepared)
								})
								fixture.RowWait(t, p.Ctx, bp, ap)
								out := await()
								if out.Result != nil {
									t.Fatal("failed subset DELETE returned result")
								}
								referenceRowTimeoutDiagnostic(t, out.Err, true, "55P03", "canceling statement due to lock timeout", "deleting")
								fixture.LockExec(t, p.Ctx, b, "RESET lock_timeout", 0)
							}
						}
						query := "DELETE FROM t WHERE id=" + tc.blockedID
						if !backend.PG {
							fixture.ViewNativeBlocked(t, p.Ctx, b, query, prepared)
							holder.Rollback()
							fixture.RowAffected(t, p.Ctx, b, query, prepared, 1)
						} else if !tc.pgLocks {
							// PostgreSQL constant gating skips the lower marked input;
							// native deliberately retains its documented eager input frontier.
							fixture.RowAffected(t, p.Ctx, b, query, prepared, 1)
							holder.Rollback()
						} else {
							ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
							done := fixture.RowAsync(t, p.Ctx, b, query, prepared, false)
							fixture.RowWait(t, p.Ctx, bp, ap)
							holder.Rollback()
							out := fixture.LockResult(t, done)
							if out.Err != nil {
								t.Fatal(out.Err)
							}
							fixture.LockAffected(t, out.Result, 1)
						}
					})
				}
			})
		}
	}
	if *fixture.Mode {
		referenceViewAggregateReadCommitted(t)
	}
}

func referenceViewAggregateReadCommitted(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct {
			name, body, change, query, column, typ string
			commit                                 bool
			want                                   []int64
		}{
			{"definition_reject", "SELECT id,v FROM t WHERE v=10 FOR UPDATE", "UPDATE t SET v=20 WHERE id=1", "SELECT count(*) AS n FROM stored", "n", "INT8", true, []int64{0}},
			{"delete", "SELECT id,v FROM t WHERE v=10 FOR UPDATE", "DELETE FROM t WHERE id=1", "SELECT count(*) AS n FROM stored", "n", "INT8", true, []int64{0}},
			{"rollback", "SELECT id,v FROM t WHERE v=10 FOR UPDATE", "UPDATE t SET v=20 WHERE id=1", "SELECT count(*) AS n FROM stored", "n", "INT8", false, []int64{1}},
			{"group_replacement", "SELECT id,v FROM t WHERE id=1 FOR UPDATE", "UPDATE t SET v=20 WHERE id=1", "SELECT v FROM grouped", "v", "INT4", true, []int64{20}},
		} {
			t.Run("RC/"+tc.name+map[bool]string{false: "/direct", true: "/prepared"}[prepared], func(t *testing.T) {
				p := fixture.NewScenario(t)
				a, b := fixture.LockConnections(t, p.Ctx, p.Postgres)
				for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE VIEW stored AS " + tc.body, "CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v"} {
					if _, err := a.ExecContext(p.Ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				ap, bp := fixture.RowPIDs(t, p.Ctx, a, b)
				holder, err := a.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				fixture.RowValues(t, fixture.RowQuery(p.Ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, fixture.RawPostgres), 10)
				fixture.RowAffected(t, p.Ctx, holder, tc.change, false, 1)
				done := fixture.RowAsync(t, p.Ctx, b, tc.query, prepared, true, fixture.RawPostgres)
				fixture.RowWait(t, p.Ctx, bp, ap)
				fixture.RowAffected(t, p.Ctx, p.Postgres, "INSERT INTO t VALUES(3,10)", false, 1)
				if tc.commit {
					err = holder.Commit()
				} else {
					err = holder.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				fixture.ViewValues(t, fixture.LockResult(t, done), tc.column, tc.typ, tc.want...)
			})
		}
	}
}
