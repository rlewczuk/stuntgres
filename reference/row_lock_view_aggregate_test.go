package reference_test

import (
	"context"
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
				for _, step := range []referenceStep{referenceExec("CREATE VIEW stored AS SELECT id,v FROM t FOR UPDATE", 0), referenceExec(grouped, 0), referenceExec("CREATE VIEW hidden AS SELECT count(*) AS n FROM stored HAVING FALSE", 0)} {
					p.step(t, step)
				}
				p.step(t, referenceExec("CREATE VIEW runtime_hidden AS SELECT count(*) AS n FROM stored HAVING count(*)=0", 0))
				values := make([][]any, len(tc.want))
				for i, value := range tc.want {
					values[i] = []any{value}
				}
				step := referenceSelect(tc.query, []string{"n"}, []string{"INT8"}, values)
				step.prepared = prepared
				p.step(t, step)
				for _, backend := range p.backends() {
					t.Run(backend.name, func(t *testing.T) {
						a, b := referenceLockConnections(t, p.ctx, backend.db)
						holder, err := a.BeginTx(p.ctx, nil)
						if err != nil {
							t.Fatal(err)
						}
						defer holder.Rollback()
						referenceRunStep(t, p.ctx, holder, backend.recorder, step)
						if tc.blockedID == "1" && !subset {
							referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=2", prepared, 1)
							referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=v WHERE id=3", prepared, 1)
						}
						if subset {
							referenceRowAffected(t, p.ctx, b, "UPDATE t SET v=31 WHERE id=3", prepared, 1)
							if !backend.pg {
								referenceViewNativeBlocked(t, p.ctx, b, "DELETE FROM t WHERE id=2", prepared)
							} else {
								ap, bp := referenceRowPIDs(t, p.ctx, a, b)
								referenceLockExec(t, p.ctx, b, "SET lock_timeout='100ms'", 0)
								await := referenceLockOperation(t, p.ctx, func(ctx context.Context) relationOutcome {
									return referenceRowExecute(ctx, b, "DELETE FROM t WHERE id=2", prepared)
								})
								referenceRowWait(t, p.ctx, bp, ap)
								out := await()
								if out.result != nil {
									t.Fatal("failed subset DELETE returned result")
								}
								referenceRowTimeoutDiagnostic(t, out.err, true, "55P03", "canceling statement due to lock timeout", "deleting")
								referenceLockExec(t, p.ctx, b, "RESET lock_timeout", 0)
							}
						}
						query := "DELETE FROM t WHERE id=" + tc.blockedID
						if !backend.pg {
							referenceViewNativeBlocked(t, p.ctx, b, query, prepared)
							holder.Rollback()
							referenceRowAffected(t, p.ctx, b, query, prepared, 1)
						} else if !tc.pgLocks {
							// PostgreSQL constant gating skips the lower marked input;
							// native deliberately retains its documented eager input frontier.
							referenceRowAffected(t, p.ctx, b, query, prepared, 1)
							holder.Rollback()
						} else {
							ap, bp := referenceRowPIDs(t, p.ctx, a, b)
							done := referenceRowAsync(t, p.ctx, b, query, prepared, false)
							referenceRowWait(t, p.ctx, bp, ap)
							holder.Rollback()
							out := referenceLockResult(t, done)
							if out.err != nil {
								t.Fatal(out.err)
							}
							referenceLockAffected(t, out.result, 1)
						}
					})
				}
			})
		}
	}
	if *referenceMode {
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
				p := newReferenceScenario(t)
				a, b := referenceLockConnections(t, p.ctx, p.postgres)
				for _, q := range []string{"CREATE TABLE t(id INT,v INT)", "INSERT INTO t VALUES(1,10),(2,20)", "CREATE VIEW stored AS " + tc.body, "CREATE VIEW grouped AS SELECT v,count(*) AS n FROM stored GROUP BY v"} {
					if _, err := a.ExecContext(p.ctx, q); err != nil {
						t.Fatal(err)
					}
				}
				ap, bp := referenceRowPIDs(t, p.ctx, a, b)
				holder, err := a.BeginTx(p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer holder.Rollback()
				referenceRowValues(t, referenceRowQuery(p.ctx, holder, "SELECT v FROM t WHERE id=1 FOR UPDATE", false, referenceRawPostgres), 10)
				referenceRowAffected(t, p.ctx, holder, tc.change, false, 1)
				done := referenceRowAsync(t, p.ctx, b, tc.query, prepared, true, referenceRawPostgres)
				referenceRowWait(t, p.ctx, bp, ap)
				referenceRowAffected(t, p.ctx, p.postgres, "INSERT INTO t VALUES(3,10)", false, 1)
				if tc.commit {
					err = holder.Commit()
				} else {
					err = holder.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				referenceViewValues(t, referenceLockResult(t, done), tc.column, tc.typ, tc.want...)
			})
		}
	}
}
