package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceClusterTransactions(t *testing.T) {
	for _, driver := range []bool{false, true} {
		t.Run(map[bool]string{false: "routine_sql_commit", true: "routine_driver_commit"}[driver], func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				for _, q := range []string{
					"CREATE FUNCTION public.cluster_key(integer) RETURNS integer LANGUAGE SQL IMMUTABLE AS $$SELECT 0; SELECT $1+10$$",
					"CREATE INDEX routine_btree ON t(public.cluster_key(k))",
					"CREATE INDEX routine_hash ON t USING hash(public.cluster_key(k))",
					"CREATE INDEX routine_brin ON t USING brin(public.cluster_key(k)) WITH(pages_per_range=1)",
					"CREATE INDEX routine_partial ON t(public.cluster_key(k)) WHERE public.cluster_key(k)>11",
				} {
					persistenceExec(t, p, b, c, q)
				}
				var executor fixture.Executor = c
				var tx *sql.Tx
				if driver {
					var err error
					tx, err = c.BeginTx(p.Ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer tx.Rollback()
					executor = tx
				} else {
					persistenceExec(t, p, b, c, "BEGIN")
					defer c.ExecContext(p.Ctx, "ROLLBACK")
				}
				persistenceRun(t, p, b, executor, fixture.Exec("INSERT INTO t VALUES(6,0,'own')", 1))
				persistenceRun(t, p, b, executor, fixture.Exec("UPDATE t SET k=4 WHERE id=2", 1))
				for _, q := range []string{"CLUSTER t USING routine_btree", "SAVEPOINT outputs", "CREATE INDEX late_routine ON t(public.cluster_key(id))", "ROLLBACK TO outputs", "CREATE INDEX late_routine ON t(public.cluster_key(id))"} {
					persistenceExec(t, p, b, executor, q)
				}
				persistenceRun(t, p, b, executor, fixture.Exec("UPDATE t SET k=9 WHERE id=6", 1))
				persistenceRun(t, p, b, executor, fixture.Exec("INSERT INTO t VALUES(7,-1,'later')", 1))
				if driver {
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
				} else {
					persistenceExec(t, p, b, c, "COMMIT")
				}
				persistenceRun(t, p, b, c, fixture.Select("SELECT id,k FROM t ORDER BY id", []string{"id", "k"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}, {int64(2), int64(4)}, {int64(3), int64(2)}, {int64(4), nil}, {int64(5), int64(3)}, {int64(6), int64(9)}, {int64(7), int64(-1)}}))
				persistenceRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT id FROM t WHERE public.cluster_key(k)=19 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}})))
				persistenceRun(t, p, b, c, fixture.Select("SELECT id FROM t WHERE public.cluster_key(k)=9 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(7)}}))
				persistenceRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT id FROM t WHERE public.cluster_key(k)>11 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}, {int64(3)}, {int64(5)}, {int64(6)}})))
			})
		})
	}
	for _, commit := range []bool{false, true} {
		t.Run(map[bool]string{false: "driver_rollback", true: "driver_commit"}[commit], func(t *testing.T) {
			persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				clusterCommandSeed(t, p, b, c)
				persistenceExec(t, p, b, c, "CLUSTER t USING a")
				tx, err := c.BeginTx(p.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				persistenceRun(t, p, b, tx, fixture.Exec("INSERT INTO t VALUES(6,0,'private')", 1))
				persistenceRun(t, p, b, tx, fixture.Prepared(fixture.Exec("CLUSTER t USING b", 0)))
				persistenceRun(t, p, b, tx, clusterCommandBits(false, true))
				persistenceRun(t, p, b, tx, fixture.Select("SELECT id FROM t WHERE k=0", []string{"id"}, []string{"INT4"}, [][]any{{int64(6)}}))
				if commit {
					err = tx.Commit()
				} else {
					err = tx.Rollback()
				}
				if err != nil {
					t.Fatal(err)
				}
				persistenceRun(t, p, b, c, clusterCommandBits(!commit, commit))
				rows := [][]any{}
				if commit {
					rows = [][]any{{int64(6)}}
				}
				persistenceRun(t, p, b, c, fixture.Select("SELECT id FROM t WHERE k=0", []string{"id"}, []string{"INT4"}, rows))
			})
		})
	}
	t.Run("sql_savepoint_readonly_and_abort", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			clusterCommandSeed(t, p, b, c)
			persistenceExec(t, p, b, c, "CLUSTER t USING a")
			defer c.ExecContext(p.Ctx, "ROLLBACK")
			for _, q := range []string{"BEGIN", "SAVEPOINT s", "CLUSTER t USING b"} {
				persistenceExec(t, p, b, c, q)
			}
			persistenceRun(t, p, b, c, clusterCommandBits(false, true))
			persistenceExec(t, p, b, c, "ROLLBACK TO s")
			persistenceRun(t, p, b, c, clusterCommandBits(true, false))
			for _, q := range []string{"CLUSTER t USING b", "COMMIT"} {
				persistenceExec(t, p, b, c, q)
			}
			persistenceRun(t, p, b, c, clusterCommandBits(false, true))
			for _, q := range []string{"BEGIN READ ONLY", "CLUSTER t USING a", "COMMIT"} {
				persistenceExec(t, p, b, c, q)
			}
			persistenceRun(t, p, b, c, clusterCommandBits(true, false))
			readOnly, err := c.BeginTx(p.Ctx, &sql.TxOptions{ReadOnly: true})
			if err != nil {
				t.Fatal(err)
			}
			defer readOnly.Rollback()
			persistenceRun(t, p, b, readOnly, fixture.Exec("CLUSTER t", 0))
			if err = readOnly.Commit(); err != nil {
				t.Fatal(err)
			}
			held := clusterCommandHeld(t, p, b, c, "CLUSTER")
			persistenceExec(t, p, b, c, "BEGIN")
			_, err = held.ExecContext(p.Ctx)
			want := persistenceDiagnostic("25001", "CLUSTER cannot run inside a transaction block")
			fixture.AssertDiagnostic(t, err, &want)
			persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("SELECT id FROM t", persistenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")))
			persistenceExec(t, p, b, c, "ROLLBACK")
			persistenceExec(t, p, b, c, "BEGIN")
			persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("CLUSTER", want))
			persistenceExec(t, p, b, c, "ROLLBACK")
			persistenceRun(t, p, b, c, clusterCommandRows())
		})
	})
}

func TestReferenceClusterPermissions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"cluster_cmd_owner", "cluster_cmd_maintainer", "cluster_cmd_reader"})
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterCommandSeed(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE cluster_cmd_owner", "CREATE ROLE cluster_cmd_maintainer", "CREATE ROLE cluster_cmd_reader",
			"GRANT USAGE ON SCHEMA public TO cluster_cmd_owner,cluster_cmd_maintainer,cluster_cmd_reader",
			"ALTER TABLE t OWNER TO cluster_cmd_owner", "GRANT MAINTAIN ON t TO cluster_cmd_maintainer", "GRANT SELECT,INSERT,UPDATE ON t TO cluster_cmd_reader",
		} {
			persistenceExec(t, p, b, c, q)
		}
		defer c.ExecContext(p.Ctx, "RESET ROLE")
		held := clusterCommandHeld(t, p, b, c, "CLUSTER t USING b")
		persistenceExec(t, p, b, c, "SET ROLE cluster_cmd_owner")
		persistenceExec(t, p, b, c, "CLUSTER t USING a")
		persistenceExec(t, p, b, c, "RESET ROLE")
		persistenceExec(t, p, b, c, "SET ROLE cluster_cmd_maintainer")
		clusterCommandExecuteHeld(t, p, b, c, held, "CLUSTER t USING b")
		persistenceExec(t, p, b, c, "RESET ROLE")
		persistenceRun(t, p, b, c, clusterCommandBits(false, true))
		persistenceExec(t, p, b, c, "REVOKE MAINTAIN ON t FROM cluster_cmd_maintainer")
		want := persistenceDiagnostic("42501", "permission denied for table t")
		for _, role := range []string{"cluster_cmd_reader", "cluster_cmd_maintainer"} {
			persistenceExec(t, p, b, c, "SET ROLE "+role)
			_, err := held.ExecContext(p.Ctx)
			fixture.AssertDiagnostic(t, err, &want)
			persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("CLUSTER t USING a", want))
			persistenceExec(t, p, b, c, "RESET ROLE")
		}
		// Rejection must happen before waiting for the target's heap lock.
		holder, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Close()
		tx, err := holder.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer tx.Rollback()
		persistenceExec(t, p, b, tx, "LOCK TABLE t IN ACCESS SHARE MODE")
		persistenceExec(t, p, b, c, "SET ROLE cluster_cmd_reader")
		persistenceExec(t, p, b, c, "SET lock_timeout='10ms'")
		persistenceRun(t, p, b, c, fixture.ErrorDiagnostic("CLUSTER t", want))
		persistenceExec(t, p, b, c, "RESET ROLE")
		if err = tx.Rollback(); err != nil {
			t.Fatal(err)
		}
		persistenceRun(t, p, b, c, clusterCommandBits(false, true))
		persistenceRun(t, p, b, c, clusterCommandRows())
	})
}
