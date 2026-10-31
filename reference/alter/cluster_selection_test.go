package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func clusterCommandSeed(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	for _, q := range []string{
		"CREATE TABLE t(id int,k int,payload text)",
		"CREATE INDEX a ON t(k)",
		"CREATE INDEX b ON t((lower(payload)) DESC NULLS FIRST,k ASC) INCLUDE(id)",
		"CREATE UNIQUE INDEX u ON t(id)",
	} {
		persistenceExec(t, p, b, c, q)
	}
	persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(1,2,'b'),(2,1,'a'),(3,2,'b'),(4,NULL,NULL),(5,3,'c')", 5))
}

func clusterCommandRows() fixture.Step {
	return fixture.Select("SELECT id,k,payload FROM t ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{
		{int64(1), int64(2), "b"}, {int64(2), int64(1), "a"}, {int64(3), int64(2), "b"}, {int64(4), nil, nil}, {int64(5), int64(3), "c"},
	})
}

func clusterCommandBits(a, b bool) fixture.Step {
	return clusterState("t", [][]any{{"a", a}, {"b", b}, {"u", false}})
}

func clusterCommandHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string) *sql.Stmt {
	t.Helper()
	s, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal("prepare", q, err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func clusterCommandExecuteHeld(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, s *sql.Stmt, q string) {
	t.Helper()
	persistenceRun(t, p, b, fixture.HeldStatement{Executor: c, Statement: s, SQL: q}, fixture.Exec(q, 0))
}

func TestReferenceClusterSelection(t *testing.T) {
	t.Run("heap_bearing_kinds", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE TEMP TABLE cluster_temp(id int)", "CREATE INDEX cluster_temp_idx ON cluster_temp(id)", "CREATE UNLOGGED TABLE cluster_unlogged(id int)", "CREATE INDEX cluster_unlogged_idx ON cluster_unlogged(id)"} {
				persistenceExec(t, p, b, c, q)
			}
			for _, table := range []string{"cluster_temp", "cluster_unlogged"} {
				persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO "+table+" VALUES(2),(1)", 2))
				persistenceRun(t, p, b, c, fixture.Exec("CLUSTER "+table+" USING "+table+"_idx", 0))
				persistenceRun(t, p, b, c, fixture.Prepared(fixture.Exec("CLUSTER "+table, 0)))
				persistenceRun(t, p, b, c, fixture.Select("SELECT id FROM "+table+" ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
				persistenceRun(t, p, b, c, clusterState(table, [][]any{{table + "_idx", true}}))
			}
			persistenceRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW cluster_mv AS SELECT id FROM cluster_unlogged", 2))
			persistenceExec(t, p, b, c, "CREATE INDEX cluster_mv_idx ON cluster_mv(id)")
			persistenceExec(t, p, b, c, "CLUSTER cluster_mv USING cluster_mv_idx")
			persistenceRun(t, p, b, c, fixture.Prepared(fixture.Exec("CLUSTER cluster_mv", 0)))
			persistenceRun(t, p, b, c, fixture.Select("SELECT id FROM cluster_mv ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			persistenceRun(t, p, b, c, clusterState("cluster_mv", [][]any{{"cluster_mv_idx", true}}))
		})
	})
	t.Run("identities", func(t *testing.T) {
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			clusterCommandSeed(t, p, b, c)
			reuse := clusterCommandHeld(t, p, b, c, "CLUSTER t")
			persistenceRun(t, p, b, c, clusterCommandBits(false, false))
			persistenceRun(t, p, b, c, fixture.Exec("CLUSTER t USING a", 0))
			clusterCommandExecuteHeld(t, p, b, c, reuse, "CLUSTER t")
			persistenceRun(t, p, b, c, clusterCommandBits(true, false))
			persistenceRun(t, p, b, c, fixture.Prepared(fixture.Exec("CLUSTER public.t USING b", 0)))
			persistenceRun(t, p, b, c, clusterCommandBits(false, true))
			persistenceExec(t, p, b, c, "ALTER INDEX b RENAME TO renamed_b")
			clusterCommandExecuteHeld(t, p, b, c, reuse, "CLUSTER t")
			persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", false}, {"renamed_b", true}, {"u", false}}))
			persistenceExec(t, p, b, c, "DROP INDEX renamed_b")
			persistenceExec(t, p, b, c, "CREATE INDEX renamed_b ON t(payload)")
			_, err := reuse.ExecContext(p.Ctx)
			want := persistenceDiagnostic("42704", `there is no previously clustered index for table "t"`)
			fixture.AssertDiagnostic(t, err, &want)
			persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", false}, {"renamed_b", false}, {"u", false}}))
			persistenceRun(t, p, b, c, clusterCommandRows())
			// Utility Query has no result columns, independently of Exec count.
			persistenceRun(t, p, b, c, fixture.Select("CLUSTER t USING a", []string{}, []string{}, [][]any{}))
		})
	})
	t.Run("namespace_and_quoted_names", func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t, "cluster_scope")
		persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			for _, q := range []string{"CREATE SCHEMA cluster_scope", `CREATE TABLE cluster_scope."Té"(id int)`, `CREATE INDEX "Ié" ON cluster_scope."Té"(id)`, "CREATE TABLE decoy(id int)", `CREATE INDEX "Ié" ON decoy(id)`, "SET search_path=public,cluster_scope"} {
				persistenceExec(t, p, b, c, q)
			}
			defer c.ExecContext(p.Ctx, "RESET search_path")
			for _, prepared := range []bool{false, true} {
				s := fixture.Exec(`CLUSTER cluster_scope."Té" USING "Ié"`, 0)
				if prepared {
					s = fixture.Prepared(s)
				}
				persistenceRun(t, p, b, c, s)
				persistenceRun(t, p, b, c, clusterState(`cluster_scope."Té"`, [][]any{{"Ié", true}}))
				persistenceRun(t, p, b, c, clusterState("decoy", [][]any{{"Ié", false}}))
			}
		})
	})
}

func clusterCommandValueScenario(t *testing.T) {
	t.Helper()
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterCommandSeed(t, p, b, c)
		for _, q := range []string{"CLUSTER t USING a", "CLUSTER t", "CLUSTER t USING b"} {
			persistenceRun(t, p, b, c, fixture.Exec(q, 0))
			persistenceRun(t, p, b, c, fixture.Prepared(clusterCommandRows()))
			persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM t WHERE k=2", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
			persistenceRun(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM t WHERE k IS NULL", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		}
		want := persistenceDiagnostic("23505", `duplicate key value violates unique constraint "u"`)
		want.Detail, want.Schema, want.Table, want.Constraint = "Key (id)=(1) already exists.", "public", "t", "u"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO t VALUES(6,4,'d'),(1,9,'bad')", want)
			if prepared {
				s = fixture.Prepared(s)
			}
			persistenceRun(t, p, b, c, s)
			persistenceRun(t, p, b, c, clusterCommandRows())
		}
		persistenceRun(t, p, b, c, fixture.Exec("INSERT INTO t VALUES(6,4,'d')", 1))
		persistenceRun(t, p, b, c, fixture.Prepared(fixture.Exec("UPDATE t SET k=7,payload='changed' WHERE id=3", 1)))
		persistenceRun(t, p, b, c, fixture.Exec("DELETE FROM t WHERE id=2", 1))
		persistenceRun(t, p, b, c, fixture.Exec("CLUSTER t", 0))
		persistenceRun(t, p, b, c, fixture.Select("SELECT id,k,payload FROM t ORDER BY id", []string{"id", "k", "payload"}, []string{"INT4", "INT4", "TEXT"}, [][]any{{int64(1), int64(2), "b"}, {int64(3), int64(7), "changed"}, {int64(4), nil, nil}, {int64(5), int64(3), "c"}, {int64(6), int64(4), "d"}}))
		persistenceRun(t, p, b, c, fixture.Prepared(fixture.Select("SELECT id FROM t WHERE k=2 ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}})))
	})
}

func TestReferenceClusterValues(t *testing.T) { clusterCommandValueScenario(t) }
