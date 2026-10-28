package reference_test

import (
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func detachConcurrentAncestry(t *testing.T) {
	for _, held := range []bool{false, true} {
		t.Run("ancestry/"+map[bool]string{false: "direct", true: "held"}[held], func(t *testing.T) {
			detachConcurrentScenario(t, func(p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
				for _, q := range []string{
					"CREATE TABLE dc_parent(k INT NOT NULL,j INT NOT NULL,payload TEXT,CONSTRAINT dc_pk PRIMARY KEY(k,j),CONSTRAINT dc_positive CHECK(k>0)) PARTITION BY RANGE(k)",
					"CREATE TABLE dc_keep PARTITION OF dc_parent FOR VALUES FROM(1) TO(2)",
					"CREATE TABLE dc_leave PARTITION OF dc_parent FOR VALUES FROM(2) TO(4) PARTITION BY RANGE(j)",
					"CREATE TABLE dc_leaf PARTITION OF dc_leave FOR VALUES FROM(0) TO(5)",
					"ALTER TABLE dc_leave ADD CONSTRAINT dc_local_check CHECK(j>0)",
					"CREATE UNIQUE INDEX dc_invalid ON ONLY dc_parent(k,j,payload)",
					"CREATE UNIQUE INDEX dc_mid_invalid ON ONLY dc_leave(k,j,payload)",
					"CREATE UNIQUE INDEX dc_leaf_extra ON dc_leaf(k,j,payload)",
					"ALTER INDEX dc_mid_invalid ATTACH PARTITION dc_leaf_extra",
					"ALTER INDEX dc_invalid ATTACH PARTITION dc_mid_invalid",
					"CREATE UNIQUE INDEX dc_local_tree ON dc_leave(k,j,payload)",
					"CREATE INDEX dc_leaf_local ON dc_leaf(payload)",
				} {
					run(fixture.Exec(q, 0))
				}
				run(fixture.Exec("INSERT INTO dc_parent VALUES(1,1,'keep'),(2,2,'leave')", 2))
				idsQ := "SELECT oid::BIGINT,conname,conindid::BIGINT FROM pg_constraint WHERE conrelid='dc_leave'::regclass OR conrelid='dc_leaf'::regclass ORDER BY oid"
				before := partitionDetachCapture(t, p, b.DB, idsQ)
				detachConcurrentPending(t, p, b, held)
				// Phase one may add exactly one bound CHECK, but existing constraints and
				// backing identities must not be reminted. Check each retained source row.
				after := partitionDetachCapture(t, p, b.DB, idsQ)
				for _, old := range before {
					found := false
					for _, now := range after {
						if reflect.DeepEqual(old, now) {
							found = true
							break
						}
					}
					if !found {
						t.Fatal("pending reminted inherited constraint", old, after)
					}
				}
				run(partitionConstraintEdge("dc_leave_pkey", "dc_pk", "dc_leave"))
				run(partitionConstraintEdge("dc_mid_invalid", "dc_invalid", "dc_leave"))
				run(partitionConstraintIndexState("dc_invalid", false))
				detachConcurrentExecute(t, p, b, b.DB, "ALTER TABLE dc_parent DETACH PARTITION dc_leave FINALIZE", held)
				run(partitionDetachTopEdges("dc_leave_pkey", false))
				run(partitionDetachTopEdges("dc_mid_invalid", false))
				run(partitionConstraintEdge("dc_leaf_pkey", "dc_leave_pkey", "dc_leaf"))
				run(partitionConstraintEdge("dc_leaf_extra", "dc_mid_invalid", "dc_leaf"))
				run(partitionConstraintIndexState("dc_invalid", false))
				run(partitionConstraintIndexState("dc_mid_invalid", true))
				run(partitionConstraintIndexState("dc_local_tree", true))
				run(detachConcurrentPlainIndex("dc_leaf_local"))
				run(fixture.Select("SELECT conislocal,coninhcount,conparentid=0 AS no_parent FROM pg_constraint WHERE conrelid='dc_leave'::regclass AND conname='dc_leave_pkey'", []string{"conislocal", "coninhcount", "no_parent"}, []string{"BOOL", "INT2", "BOOL"}, [][]any{{true, int64(0), true}}))
				run(fixture.Exec("DROP TABLE dc_parent", 0))
				run(partitionConstraintDuplicate("INSERT INTO dc_leave VALUES(2,2,'duplicate')", "dc_leaf", "dc_leaf_pkey", "Key (k, j)=(2, 2) already exists."))
				run(fixture.Exec("INSERT INTO dc_leave VALUES(3,3,'new')", 1))
				run(detachConcurrentRows("dc_leave", 2, 3))
				run(fixture.Exec("ALTER TABLE dc_leave DROP CONSTRAINT dc_leave_pkey", 0))
				run(fixture.Exec("DROP INDEX dc_local_tree", 0))
				run(fixture.Exec("DROP TABLE dc_leave", 0))
			})
		})
	}
}
