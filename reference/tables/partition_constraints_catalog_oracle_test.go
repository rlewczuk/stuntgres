package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionConstraintsNonuniqueCatalog(t *testing.T) {
	partitionConstraintCase(t, "check_and_not_null", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_catalog_root(k INT CONSTRAINT pc_catalog_nn NOT NULL, CONSTRAINT pc_catalog_ck CHECK(k>0)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_catalog_leaf PARTITION OF pc_catalog_root DEFAULT", 0))
		run(fixture.Select("SELECT conname,contype,conindid=0::OID AS no_index,conparentid=0::OID AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pc_catalog_root'::regclass::OID ORDER BY conname",
			[]string{"conname", "contype", "no_index", "no_parent", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "INT2"},
			[][]any{{"pc_catalog_ck", "c", true, true, true, int64(0)}, {"pc_catalog_nn", "n", true, true, true, int64(0)}}))
		run(fixture.Select("SELECT conname,contype,conindid=0::OID AS no_index,conparentid=0::OID AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pc_catalog_leaf'::regclass::OID ORDER BY conname",
			[]string{"conname", "contype", "no_index", "no_parent", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "INT2"},
			[][]any{{"pc_catalog_ck", "c", true, true, false, int64(1)}, {"pc_catalog_nn", "n", true, true, false, int64(1)}}))
	})
	partitionConstraintCase(t, "ordinary_foreign_key", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_catalog_target(k INT PRIMARY KEY)", 0))
		run(fixture.Exec("CREATE TABLE pc_catalog_ref(k INT CONSTRAINT pc_catalog_fk REFERENCES pc_catalog_target(k))", 0))
		run(fixture.Select("SELECT conindid='pc_catalog_target_pkey'::regclass::OID AS backing,conparentid=0::OID AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conname='pc_catalog_fk'",
			[]string{"backing", "no_parent", "conislocal", "coninhcount"}, []string{"BOOL", "BOOL", "BOOL", "INT2"}, [][]any{{true, true, true, int64(0)}}))
	})
	partitionConstraintCase(t, "merged_local_declarations", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_catalog_root(k INT CONSTRAINT pc_catalog_nn NOT NULL, CONSTRAINT pc_catalog_ck CHECK(k>0)) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_catalog_leaf PARTITION OF pc_catalog_root(k CONSTRAINT pc_catalog_nn NOT NULL, CONSTRAINT pc_catalog_ck CHECK(k>0)) DEFAULT", 0))
		run(fixture.Select("SELECT conname,contype,conindid=0::OID AS no_index,conparentid=0::OID AS no_parent,conislocal,coninhcount FROM pg_constraint WHERE conrelid='pc_catalog_leaf'::regclass::OID ORDER BY conname",
			[]string{"conname", "contype", "no_index", "no_parent", "conislocal", "coninhcount"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "INT2"},
			[][]any{{"pc_catalog_ck", "c", true, true, false, int64(1)}, {"pc_catalog_nn", "n", true, true, true, int64(1)}}))
	})
}

func TestReferencePartitionConstraintsIncompleteReuse(t *testing.T) {
	partitionConstraintCase(t, "recursive_parent_reuses_partial_child", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_reuse_root(k INT,payload INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_reuse_mid PARTITION OF pc_reuse_root DEFAULT PARTITION BY RANGE(payload)", 0))
		run(fixture.Exec("CREATE TABLE pc_reuse_leaf PARTITION OF pc_reuse_mid DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_reuse_mid_idx ON ONLY pc_reuse_mid(k,payload)", 0))
		run(partitionConstraintIndexState("pc_reuse_mid_idx", false))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_reuse_root_idx ON pc_reuse_root(k,payload)", 0))
		run(partitionConstraintEdge("pc_reuse_mid_idx", "pc_reuse_root_idx", "pc_reuse_mid"))
		run(partitionConstraintIndexState("pc_reuse_root_idx", false))
		run(partitionConstraintIndexState("pc_reuse_mid_idx", false))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_reuse_leaf_k_payload_idx'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec("INSERT INTO pc_reuse_root VALUES(1,1),(1,1)", 2))
	})
}
