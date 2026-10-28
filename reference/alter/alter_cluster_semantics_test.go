package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAlterClusterResolution(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "cluster_ns")
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		for _, q := range []string{"CREATE SCHEMA cluster_ns", "CREATE TABLE cluster_ns.t(id int)", "CREATE INDEX a ON cluster_ns.t(id)", "SET search_path=cluster_ns,public", "ALTER TABLE public.t CLUSTER ON a", "SET search_path=public", `CREATE INDEX "Quoted index" ON t((id+1) DESC,note) INCLUDE(id)`, `ALTER TABLE public.t CLUSTER ON "Quoted index"`} {
			persistenceExec(t, p, b, c, q)
		}
		persistenceRun(t, p, b, c, clusterState("cluster_ns.t", [][]any{{"a", false}}))
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"Quoted index", true}, {"a", false}, {"b", false}}))
		held, err := c.PrepareContext(p.Ctx, "ALTER TABLE t CLUSTER ON a")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		persistenceExec(t, p, b, c, "ALTER INDEX a RENAME TO renamed")
		want := persistenceDiagnostic("42704", `index "a" for table "t" does not exist`)
		_, err = held.ExecContext(p.Ctx)
		fixture.AssertDiagnostic(t, err, &want)
		persistenceExec(t, p, b, c, "CREATE INDEX a ON t(note)")
		if _, err = held.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		}
		persistenceExec(t, p, b, c, "DROP INDEX a")
		_, err = held.ExecContext(p.Ctx)
		fixture.AssertDiagnostic(t, err, &want)
		persistenceExec(t, p, b, c, "CREATE INDEX a ON t(id)")
		if _, err = held.ExecContext(p.Ctx); err != nil {
			t.Fatal(err)
		}
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"Quoted index", false}, {"a", true}, {"b", false}, {"renamed", false}}))
		for _, tc := range []struct {
			query, token string
			position     int
		}{
			{"ALTER TABLE t CLUSTER ON public.a", ".", 32},
			{"ALTER TABLE t CLUSTER ON b trailing", "trailing", 28},
		} {
			want := persistenceDiagnostic("42601", `syntax error at or near "`+tc.token+`"`)
			want.Position = fixture.Position(tc.position)
			for _, prepared := range []bool{false, true} {
				step := fixture.ErrorDiagnostic(tc.query, want)
				step.ErrorStage = "execute"
				if prepared {
					step = fixture.Prepared(step)
					step.ErrorStage = "prepare"
				}
				persistenceRun(t, p, b, c, step)
				persistenceRun(t, p, b, c, clusterState("t", [][]any{{"Quoted index", false}, {"a", true}, {"b", false}, {"renamed", false}}))
				persistenceRun(t, p, b, c, clusterRows())
			}
		}
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"Quoted index", false}, {"a", true}, {"b", false}, {"renamed", false}}))
		persistenceExec(t, p, b, c, "ALTER INDEX a RENAME TO preferred")
		persistenceExec(t, p, b, c, "ALTER TABLE t ALTER id TYPE bigint")
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"Quoted index", false}, {"b", false}, {"preferred", true}, {"renamed", false}}))
		persistenceExec(t, p, b, c, "DROP INDEX preferred")
		persistenceExec(t, p, b, c, "CREATE INDEX preferred ON t(id)")
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"Quoted index", false}, {"b", false}, {"preferred", false}, {"renamed", false}}))
		persistenceExec(t, p, b, c, "DROP TABLE cluster_ns.t")
		persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON preferred")
		persistenceExec(t, p, b, c, "ALTER TABLE t SET SCHEMA cluster_ns")
		persistenceRun(t, p, b, c, clusterState("cluster_ns.t", [][]any{{"Quoted index", false}, {"b", false}, {"preferred", true}, {"renamed", false}}))
	})
}

func TestReferenceAlterClusterAtomicity(t *testing.T) {
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		clusterSetup(t, p, b, c)
		persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON a")
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON a")
			persistenceExec(t, p, b, c, "BEGIN")
			persistenceExec(t, p, b, c, "ALTER TABLE t CLUSTER ON b")
			clusterCheck(t, p, b, c, false, true)
			clusterCheck(t, p, b, peer, true, false)
			persistenceExec(t, p, b, c, end)
			clusterCheck(t, p, b, peer, end != "COMMIT", end == "COMMIT")
		}
		for _, q := range []string{"BEGIN", "SAVEPOINT before_cluster", "ALTER TABLE t CLUSTER ON b", "ROLLBACK TO before_cluster"} {
			persistenceExec(t, p, b, c, q)
		}
		clusterCheck(t, p, b, c, true, false)
		persistenceExec(t, p, b, c, "COMMIT")
		for _, held := range []bool{false, true} {
			persistenceFailure(t, p, b, c, "ALTER TABLE t CLUSTER ON b, CLUSTER ON missing_idx", held, persistenceDiagnostic("42704", `index "missing_idx" for table "t" does not exist`))
			clusterCheck(t, p, b, c, true, false)
			want := persistenceDiagnostic("23514", `check constraint "reject" of relation "t" is violated by some row`)
			want.Schema, want.Table, want.Constraint = "public", "t", "reject"
			persistenceFailure(t, p, b, c, "ALTER TABLE t CLUSTER ON b, ADD CONSTRAINT reject CHECK(id<0)", held, want)
			clusterCheck(t, p, b, c, true, false)
			persistenceFailure(t, p, b, c, "ALTER TABLE t ADD COLUMN extra int, ADD CONSTRAINT staged UNIQUE(id), CLUSTER ON staged, CLUSTER ON missing_idx", held, persistenceDiagnostic("42704", `index "missing_idx" for table "t" does not exist`))
			clusterCheck(t, p, b, c, true, false)
			persistenceRun(t, p, b, c, fixture.Select("SELECT attname FROM pg_attribute WHERE attrelid='t'::regclass AND attname='extra' AND NOT attisdropped", []string{"attname"}, []string{"NAME"}, nil))
			persistenceRun(t, p, b, c, fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='t'::regclass AND conname IN('staged','reject')", []string{"conname"}, []string{"NAME"}, nil))
		}
		persistenceExec(t, p, b, c, "ALTER TABLE t ADD CONSTRAINT staged UNIQUE(id), CLUSTER ON staged")
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", false}, {"b", false}, {"staged", true}}))
		persistenceFailure(t, p, b, c, "ALTER TABLE t DROP CONSTRAINT staged, CLUSTER ON staged", false, persistenceDiagnostic("42704", `index "staged" for table "t" does not exist`))
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", false}, {"b", false}, {"staged", true}}))
		persistenceExec(t, p, b, c, "ALTER TABLE t ALTER id TYPE bigint, CLUSTER ON a")
		persistenceRun(t, p, b, c, clusterState("t", [][]any{{"a", true}, {"b", false}, {"staged", false}}))
	})
}

func TestReferenceAlterClusterRelationKinds(t *testing.T) {
	clusterAdditionalKinds(t)
	clusterRouting(t)
	persistenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TABLE parent(id int)", "CREATE TABLE child() INHERITS(parent)", "CREATE INDEX pa ON parent(id)", "CREATE INDEX ch ON child(id)", "ALTER TABLE child CLUSTER ON ch"} {
			persistenceExec(t, p, b, c, q)
		}
		for _, target := range []string{"parent", "ONLY parent", "parent *"} {
			for _, action := range []string{"CLUSTER ON pa", "SET WITHOUT CLUSTER"} {
				persistenceExec(t, p, b, c, "ALTER TABLE "+target+" "+action)
				persistenceRun(t, p, b, c, clusterState("child", [][]any{{"ch", true}}))
			}
		}
		for _, q := range []string{"CREATE TABLE p(id int,CONSTRAINT pi PRIMARY KEY(id)) PARTITION BY RANGE(id)", "CREATE TABLE leaf PARTITION OF p DEFAULT", "CREATE INDEX li ON leaf(id)", "ALTER TABLE leaf CLUSTER ON li", "ALTER TABLE p SET WITHOUT OIDS", "CREATE VIEW v AS SELECT id FROM parent", "CREATE SEQUENCE seq", "CREATE TYPE pair AS(id int)"} {
			persistenceExec(t, p, b, c, q)
		}
		persistenceFailure(t, p, b, c, "ALTER TABLE p CLUSTER ON missing_idx", false, persistenceDiagnostic("42704", `index "missing_idx" for table "p" does not exist`))
		persistenceFailure(t, p, b, c, "ALTER TABLE p CLUSTER ON parent", false, persistenceDiagnostic("42809", `"parent" is not an index`))
		persistenceFailure(t, p, b, c, "ALTER TABLE p CLUSTER ON pa", false, persistenceDiagnostic("42809", `"pa" is not an index for table "p"`))
		for _, q := range []string{"ALTER TABLE p CLUSTER ON pi", "ALTER TABLE p SET WITHOUT CLUSTER"} {
			persistenceFailure(t, p, b, c, q, false, persistenceDiagnostic("0A000", "cannot mark index clustered in partitioned table"))
		}
		persistenceRun(t, p, b, c, clusterState("p", [][]any{{"pi", false}}))
		persistenceRun(t, p, b, c, clusterState("leaf", [][]any{{"leaf_pkey", false}, {"li", true}}))
		persistenceRun(t, p, b, c, fixture.Exec("CREATE MATERIALIZED VIEW mv AS SELECT id FROM parent", 0))
		for _, q := range []string{"CREATE INDEX mi ON mv(id)", "ALTER MATERIALIZED VIEW mv CLUSTER ON mi"} {
			persistenceExec(t, p, b, c, q)
		}
		persistenceRun(t, p, b, c, clusterState("mv", [][]any{{"mi", true}}))
		for _, v := range []struct{ name, kind string }{{"v", "views"}, {"pa", "indexes"}, {"seq", "sequences"}} {
			for _, action := range []string{"CLUSTER ON pa", "SET WITHOUT CLUSTER", "SET WITHOUT OIDS"} {
				label := action
				if action == "CLUSTER ON pa" {
					label = "CLUSTER ON"
				}
				want := persistenceDiagnostic("42809", "ALTER action "+label+` cannot be performed on relation "`+v.name+`"`)
				want.Detail = "This operation is not supported for " + v.kind + "."
				persistenceFailure(t, p, b, c, "ALTER TABLE "+v.name+" "+action, false, want)
			}
		}
		want := persistenceDiagnostic("42809", `ALTER action SET WITHOUT OIDS cannot be performed on relation "mv"`)
		want.Detail = "This operation is not supported for materialized views."
		persistenceFailure(t, p, b, c, "ALTER TABLE mv SET WITHOUT OIDS", false, want)
	})
}
