package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsDrops(t *testing.T) {
	partitionConstraintCase(t, "constraint_dependency_and_closure", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(run, "PRIMARY KEY(k,payload)")
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x'),(3,'y')", 2))
		for _, cascade := range []string{"", " CASCADE"} {
			run(referenceRangeError("DROP INDEX pc_a_pkey"+cascade, "2BP01", "cannot drop index pc_a_pkey because index pc_root_pkey requires it", fixture.Diagnostic{Hint: "You can drop index pc_root_pkey instead."}))
		}
		run(referenceRangeError("DROP INDEX pc_root_pkey", "2BP01", "cannot drop index pc_root_pkey because constraint pc_root_pkey on table pc_root requires it", fixture.Diagnostic{Hint: "You can drop constraint pc_root_pkey on table pc_root instead."}))
		run(referenceRangeError("ALTER TABLE pc_a DROP CONSTRAINT pc_a_pkey", "42P16", `cannot drop inherited constraint "pc_a_pkey" of relation "pc_a"`, fixture.Diagnostic{}))
		run(partitionConstraintEdge("pc_a_pkey", "pc_root_pkey", "pc_a"))
		run(fixture.Exec("ALTER TABLE pc_root DROP CONSTRAINT pc_root_pkey", 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE contype='p' AND (conrelid='pc_root'::regclass::OID OR conrelid='pc_a'::regclass::OID OR conrelid='pc_b'::regclass::OID OR conrelid='pc_def'::regclass::OID)", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_root_pkey' OR relname='pc_a_pkey' OR relname='pc_b_pkey' OR relname='pc_def_pkey'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x')", 1))
		run(referenceRangeError("INSERT INTO pc_root VALUES(1,NULL)", "23502", `null value in column "payload" of relation "pc_a" violates not-null constraint`, fixture.Diagnostic{Schema: "public", Table: "pc_a", Column: "payload", Detail: "Failing row contains (1, null)."}))
	})
	partitionConstraintCase(t, "standalone_subtree", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintManualTree(run)
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_b_idx", 0))
		run(fixture.Exec("DROP INDEX pc_root_idx", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_root_idx' OR relname='pc_a_idx' OR relname='pc_b_idx'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x'),(1,'x')", 2))
	})
	partitionConstraintCase(t, "standalone_local_constraint_child_oracle_decision", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root(CONSTRAINT pc_local UNIQUE(k,payload)) DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_root_idx ON ONLY pc_root(k,payload)", 0))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_local", 0))
		run(fixture.Select("SELECT conislocal,conparentid=0::OID AS local_parent FROM pg_constraint WHERE conname='pc_local'", []string{"conislocal", "local_parent"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		run(referenceRangeError("DROP INDEX pc_root_idx", "2BP01", "cannot drop index pc_root_idx because other objects depend on it", fixture.Diagnostic{Detail: "constraint pc_local on table pc_a depends on index pc_root_idx", Hint: "Use DROP ... CASCADE to drop the dependent objects too."}))
		run(fixture.Exec("DROP INDEX pc_root_idx CASCADE", 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pc_local'", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_local' OR relname='pc_root_idx'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x'),(1,'x')", 2))
	})
	partitionConstraintCase(t, "table_subtree_future_clone", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(run, "UNIQUE(k,payload)")
		var root, sibling int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pc_root_k_payload_key'").Scan(&root); err != nil {
			t.Fatal(err)
		}
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pc_b_k_payload_key'").Scan(&sibling); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("DROP TABLE pc_a", 0))
		run(fixture.Select("SELECT relname,oid=$1::OID AS same FROM pg_class WHERE relname='pc_root_k_payload_key'", []string{"relname", "same"}, []string{"NAME", "BOOL"}, [][]any{{"pc_root_k_payload_key", true}}, root))
		run(fixture.Select("SELECT relname,oid=$1::OID AS same FROM pg_class WHERE relname='pc_b_k_payload_key'", []string{"relname", "same"}, []string{"NAME", "BOOL"}, [][]any{{"pc_b_k_payload_key", true}}, sibling))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_a' OR relname='pc_a_k_payload_key'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root FOR VALUES FROM(1) TO(3)", 0))
		run(partitionConstraintEdge("pc_a_k_payload_key", "pc_root_k_payload_key", "pc_a"))
	})
}

func TestReferencePartitionConstraintsTransactions(t *testing.T) {
	partitionConstraintCase(t, "attach_drop_savepoint", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		setup := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintManualTree(setup)
		a, _ := fixture.LockConnections(t, p.Ctx, b.DB)
		defer a.ExecContext(p.Ctx, "ROLLBACK")
		run := func(s fixture.Step) { s.Prepared = prepared; fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		var before int64
		if err := a.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pc_a_idx'").Scan(&before); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("SAVEPOINT before_attach", 0))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		run(fixture.Exec("ROLLBACK TO before_attach", 0))
		run(fixture.Select("SELECT deptype FROM pg_depend WHERE objid='pc_a_idx'::regclass::OID AND deptype='P'", []string{"deptype"}, []string{"CHAR"}, nil))
		run(fixture.Select("SELECT oid=$1::OID AS same FROM pg_class WHERE relname='pc_a_idx'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, before))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_b_idx", 0))
		run(partitionConstraintIndexState("pc_root_idx", true))
		run(fixture.Exec("SAVEPOINT before_drop", 0))
		run(fixture.Exec("DROP INDEX pc_root_idx", 0))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_root_idx' OR relname='pc_a_idx' OR relname='pc_b_idx'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("ROLLBACK TO before_drop", 0))
		run(partitionConstraintIndexState("pc_root_idx", true))
		run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		run(referenceRangeError("DROP INDEX pc_a_idx", "2BP01", "cannot drop index pc_a_idx because index pc_root_idx requires it", fixture.Diagnostic{Hint: "You can drop index pc_root_idx instead."}))
		run(fixture.ImplicitError("SELECT k FROM pc_root", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
		run(fixture.Exec("ROLLBACK TO before_drop", 0))
		run(fixture.Exec("COMMIT", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x')", 1))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'x')", "pc_a", "pc_a_idx", "Key (k, payload)=(1, x) already exists."))
		run(fixture.Exec("BEGIN READ ONLY", 0))
		run(referenceRangeError("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", "25006", "cannot execute ALTER INDEX in a read-only transaction", fixture.Diagnostic{}))
		run(fixture.Exec("ROLLBACK", 0))
	})
}

func TestReferencePartitionConstraintsDropPrecedence(t *testing.T) {
	// Both modes preserve the parent-required backing index; the independently
	// observed dependency error must not be replaced with inferred CASCADE.
	for _, mode := range []string{"RESTRICT", "CASCADE"} {
		partitionConstraintCase(t, "local_attached_constraint_"+mode, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root(CONSTRAINT pc_local UNIQUE(k,payload)) DEFAULT", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX pc_root_idx ON ONLY pc_root(k,payload)", 0))
			run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_local", 0))
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
			run(referenceRangeError("ALTER TABLE pc_a DROP CONSTRAINT pc_local "+mode, "2BP01", "cannot drop index pc_local because index pc_root_idx requires it", fixture.Diagnostic{Hint: "You can drop index pc_root_idx instead."}))
			run(partitionConstraintIndexState("pc_root_idx", true))
			run(partitionConstraintEdge("pc_local", "pc_root_idx", "pc_a"))
			run(fixture.Select("SELECT conparentid=0::OID AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conname='pc_local'", []string{"no_parent", "conislocal", "coninhcount"}, []string{"BOOL", "BOOL", "INT2"}, [][]any{{true, true, int64(0)}}))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_a", "pc_local", "Key (k, payload)=(1, sentinel) already exists."))
			run(fixture.Exec("DROP INDEX pc_root_idx CASCADE", 0))
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
		})
	}
	partitionConstraintCase(t, "later_missing_constraint_is_atomic", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintTree(run, "PRIMARY KEY(k,payload)")
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
		var original int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_constraint WHERE conname='pc_root_pkey'").Scan(&original); err != nil {
			t.Fatal(err)
		}
		run(referenceRangeError("ALTER TABLE pc_root DROP CONSTRAINT pc_root_pkey,DROP CONSTRAINT pc_missing", "42704", `constraint "pc_missing" of relation "pc_root" does not exist`, fixture.Diagnostic{}))
		run(fixture.Select("SELECT oid=$1::OID AS same FROM pg_constraint WHERE conname='pc_root_pkey'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, original))
		run(partitionConstraintEdge("pc_a_pkey", "pc_root_pkey", "pc_a"))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'sentinel')", "pc_a", "pc_a_pkey", "Key (k, payload)=(1, sentinel) already exists."))
		run(fixture.Exec("ALTER TABLE pc_root DROP CONSTRAINT pc_root_pkey", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'sentinel')", 1))
	})
}
