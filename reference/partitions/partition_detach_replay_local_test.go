package reference_test

import (
	"bytes"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePartitionDetachReplayLocal(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		t.Run(kind, func(t *testing.T) {
			partitionDetachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				run(fixture.Exec("CREATE TABLE pd_lc_root(k INT NOT NULL) PARTITION BY RANGE(k)", 0))
				run(fixture.Exec("CREATE TABLE pd_lc_child(k INT CONSTRAINT pd_lc_child_nn NOT NULL,CONSTRAINT pd_lc_local "+kind+"(k))", 0))
				run(fixture.Exec("INSERT INTO pd_lc_child VALUES(1)", 1))
				run(fixture.Exec("ALTER TABLE pd_lc_root ATTACH PARTITION pd_lc_child FOR VALUES FROM(1) TO(3)", 0))
				run(fixture.Exec("CREATE TABLE pd_lc_unindexed PARTITION OF pd_lc_root FOR VALUES FROM(3) TO(5)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX pd_lc_root_idx ON ONLY pd_lc_root(k)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX pd_lc_attached ON pd_lc_child(k)", 0))
				run(fixture.Exec("ALTER INDEX pd_lc_root_idx ATTACH PARTITION pd_lc_attached", 0))
				run(fixture.Prepared(fixture.Exec("ALTER TABLE pd_lc_root DETACH PARTITION pd_lc_unindexed", 0)))
				run(fixture.Exec("CREATE TABLE pd_lc_refs(ref REGCLASS DEFAULT 'pd_lc_local'::regclass)", 0))
				run(fixture.Exec("INSERT INTO pd_lc_refs DEFAULT VALUES", 1))
				run(fixture.Exec("CREATE VIEW pd_lc_provider_view AS SELECT ref='pd_lc_local'::regclass AS same FROM pd_lc_refs", 0))
				partitionDetachLocalState(t, p, b.DB, b.Recorder, kind)
				// Original construction creates the local backing first. Neither
				// DETACH nor failed writes can change that own enforcing identity.
				run(partitionConstraintDuplicate("INSERT INTO pd_lc_root VALUES(1)", "pd_lc_child", "pd_lc_local", "Key (k)=(1) already exists."))
				if b.PG {
					partitionDetachLocalFuture(t, p, b.DB, b.Recorder, "pd_lc_local")
					return
				}
				var dump bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
					t.Fatal(err)
				}
				fresh := fixture.NewFixture(t, false, true)
				if err := stuntgres.Restore(fresh.Ctx, fresh.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				partitionDetachLocalState(t, fresh, fresh.Memory, fresh.MemoryRecord, kind)
				// Faithful delayed-local SQL creates the captured attached
				// backing first, then the independent local constraint backing.
				partitionDetachLocalFuture(t, fresh, fresh.Memory, fresh.MemoryRecord, "pd_lc_attached")
				if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
					t.Fatal(err)
				}
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
					t.Fatal(err)
				}
				partitionDetachLocalState(t, p, b.DB, b.Recorder, kind)
				if p.Postgres != nil {
					partitionAttachReplay(t, p, dump.String(), func(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, pg bool) {
						partitionDetachLocalState(t, p, x, r, kind)
						partitionDetachLocalFuture(t, p, x, r, "pd_lc_attached")
					})
				}
				partitionDetachLocalFuture(t, p, b.DB, b.Recorder, "pd_lc_attached")
			})
		})
	}
}

func partitionDetachLocalState(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, kind string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	run(partitionConstraintIndexState("pd_lc_root_idx", false))
	run(partitionConstraintEdge("pd_lc_attached", "pd_lc_root_idx", "pd_lc_child"))
	run(partitionDetachTopEdges("pd_lc_local", false))
	run(fixture.Select("SELECT i.relname,i.relispartition,x.indisunique,x.indisprimary,x.indisvalid FROM pg_class i JOIN pg_index x ON x.indexrelid=i.oid WHERE x.indrelid='pd_lc_child'::regclass::OID ORDER BY i.relname", []string{"relname", "relispartition", "indisunique", "indisprimary", "indisvalid"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"pd_lc_attached", true, true, false, true}, {"pd_lc_local", false, true, kind == "PRIMARY KEY", true}}))
	run(fixture.Select("SELECT conindid='pd_lc_local'::regclass::OID AS own_backing,conparentid=0 AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pd_lc_child'::regclass::OID AND conname='pd_lc_local'", []string{"own_backing", "no_parent", "conislocal", "coninhcount"}, []string{"BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{true, true, true, int64(0)}}))
	run(fixture.Select("SELECT count(*) FROM pg_depend d JOIN pg_constraint c ON c.oid=d.refobjid WHERE d.classid='pg_class'::regclass::OID AND d.objid='pd_lc_local'::regclass::OID AND d.refclassid='pg_constraint'::regclass::OID AND c.conname='pd_lc_local' AND d.deptype='i'", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
	run(fixture.Prepared(referenceRangeRows("SELECT k FROM pd_lc_root", 1)))
	run(fixture.Select("SELECT same FROM pd_lc_provider_view", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
	run(fixture.Select("SELECT ref='pd_lc_local'::regclass AS same FROM pd_lc_refs", []string{"same"}, []string{"BOOL"}, [][]any{{true}}))
}

func partitionDetachLocalFuture(t *testing.T, p *fixture.Pair, x fixture.Executor, r *fixture.Recorder, winner string) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, x, r, s) }
	run(partitionConstraintDuplicate("INSERT INTO pd_lc_root VALUES(1)", "pd_lc_child", winner, "Key (k)=(1) already exists."))
	run(fixture.Exec("ALTER TABLE pd_lc_root DETACH PARTITION pd_lc_child", 0))
	run(fixture.Exec("DROP TABLE pd_lc_root", 0))
	run(referenceRangeRows("SELECT k FROM pd_lc_child", 1))
	run(partitionConstraintDuplicate("INSERT INTO pd_lc_child VALUES(1)", "pd_lc_child", winner, "Key (k)=(1) already exists."))
	run(fixture.ImplicitError("INSERT INTO pd_lc_child VALUES(NULL)", fixture.Diagnostic{Code: "23502", Message: `null value in column "k" of relation "pd_lc_child" violates not-null constraint`, Detail: "Failing row contains (null).", Schema: "public", Table: "pd_lc_child", Column: "k"}))
	run(fixture.Exec("INSERT INTO pd_lc_refs DEFAULT VALUES", 1))
	run(fixture.Select("SELECT count(*) FROM pd_lc_refs WHERE ref='pd_lc_local'::regclass", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
	run(fixture.Exec("DROP VIEW pd_lc_provider_view", 0))
	run(fixture.Exec("DROP TABLE pd_lc_refs", 0))
	run(fixture.Exec("ALTER TABLE pd_lc_child DROP CONSTRAINT pd_lc_local", 0))
	run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='pd_lc_local'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
	run(partitionConstraintDuplicate("INSERT INTO pd_lc_child VALUES(1)", "pd_lc_child", "pd_lc_attached", "Key (k)=(1) already exists."))
	run(referenceRangeRows("SELECT k FROM pd_lc_child", 1))
}
