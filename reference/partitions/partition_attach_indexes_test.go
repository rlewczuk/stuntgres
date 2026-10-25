package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePartitionAttachIndexes(t *testing.T) {
	cases := []struct {
		name, root, child string
		reuse             bool
	}{
		{"constraint_equivalent", "CONSTRAINT pa_index_root_key UNIQUE(k,payload)", "ALTER TABLE pa_index_child ADD CONSTRAINT pa_index_existing UNIQUE(k,payload)", true},
		{"plain_equivalent", "", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k,payload)", true},
		{"plain_for_constraint", "CONSTRAINT pa_index_root_key UNIQUE(k,payload)", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k,payload)", false},
		{"missing", "CONSTRAINT pa_index_root_key UNIQUE(k,payload)", "", false},
		{"include_mismatch", "", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k,payload) INCLUDE(extra)", false},
		{"predicate_mismatch", "", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k,payload) WHERE k>0", false},
		{"opfamily_mismatch", "", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k,payload text_pattern_ops)", false},
		{"null_policy_mismatch", "", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k,payload) NULLS NOT DISTINCT", false},
		{"sort_presentation", "", "CREATE UNIQUE INDEX pa_index_existing ON pa_index_child(k DESC NULLS FIRST,payload DESC NULLS LAST)", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					constraint := ""
					if c.root != "" {
						constraint = "," + c.root
					}
					run(fixture.Exec("CREATE TABLE pa_index_root(k INT,payload TEXT,extra INT"+constraint+") PARTITION BY RANGE(k)", 0))
					parent := "pa_index_root_key"
					if c.root == "" {
						parent = "pa_index_root_idx"
						run(fixture.Exec("CREATE UNIQUE INDEX "+parent+" ON pa_index_root(k,payload)", 0))
					}
					run(fixture.Exec("CREATE TABLE pa_index_child(extra INT,payload TEXT,k INT)", 0))
					run(fixture.Exec("INSERT INTO pa_index_child VALUES(9,'one',1),(8,'two',2)", 2))
					run(fixture.Exec("CREATE INDEX pa_index_local_hash ON pa_index_child USING hash(extra)", 0))
					var oid int64
					if c.child != "" {
						run(fixture.Exec(c.child, 0))
						if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pa_index_existing'").Scan(&oid); err != nil {
							t.Fatal(err)
						}
					}
					run(fixture.Prepared(fixture.Exec("ALTER TABLE pa_index_root ATTACH PARTITION pa_index_child FOR VALUES FROM(1) TO(3)", 0)))
					childIndex := "pa_index_child_k_payload_key"
					if c.root == "" {
						childIndex = "pa_index_child_k_payload_idx"
					}
					if c.reuse {
						childIndex = "pa_index_existing"
					}
					run(partitionConstraintEdge(childIndex, parent, "pa_index_child"))
					run(partitionConstraintIndexState(childIndex, true))
					if c.child != "" {
						run(fixture.Select("SELECT oid=$1::OID AS same FROM pg_class WHERE relname='pa_index_existing'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oid))
					}
					count := int64(2)
					if c.child != "" && !c.reuse {
						count = 3
					}
					run(fixture.Select("SELECT COUNT(*) FROM pg_index WHERE indrelid='pa_index_child'::regclass::OID", []string{"count"}, []string{"INT8"}, [][]any{{count}}))
					run(fixture.Select("SELECT k,payload,extra FROM pa_index_root ORDER BY k", []string{"k", "payload", "extra"}, []string{"INT4", "TEXT", "INT4"}, [][]any{{int64(1), "one", int64(9)}, {int64(2), "two", int64(8)}}))
					// Enforcement is real over the populated pre-attachment store.
					enforcing := childIndex
					if c.child != "" {
						enforcing = "pa_index_existing"
					} // Earlier equivalent-key local authority wins disclosure.
					run(partitionConstraintDuplicate("INSERT INTO pa_index_root VALUES(1,'one',7)", "pa_index_child", enforcing, "Key (k, payload)=(1, one) already exists."))
					if c.root != "" {
						run(fixture.Select("SELECT c.conparentid=p.oid AS immediate,c.conislocal,c.coninhcount FROM pg_constraint c JOIN pg_constraint p ON p.conname='pa_index_root_key' WHERE c.conindid='"+childIndex+"'::regclass::OID", []string{"immediate", "conislocal", "coninhcount"}, []string{"BOOL", "BOOL", "INT2"}, [][]any{{true, false, int64(1)}}))
					}
				})
			}
		})
	}
	partitionAttachIndexBuildFailures(t)
	partitionAttachNestedIndexes(t)
}

func partitionAttachIndexBuildFailures(t *testing.T) {
	for _, nnd := range []bool{false, true} {
		t.Run(func() string {
			if nnd {
				return "nnd_duplicate"
			}
			return "duplicate"
		}(), func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
					policy := ""
					payload := "'one'"
					detail := "Key (k, payload)=(1, one) is duplicated."
					if nnd {
						policy = " NULLS NOT DISTINCT"
						payload = "NULL"
						detail = "Key (k, payload)=(1, null) is duplicated."
					}
					run(fixture.Exec("CREATE TABLE pa_build_root(k INT,payload TEXT,CONSTRAINT pa_build_key UNIQUE"+policy+"(k,payload)) PARTITION BY RANGE(k)", 0))
					run(fixture.Exec("CREATE TABLE pa_build_child(k INT,payload TEXT)", 0))
					run(fixture.Exec("INSERT INTO pa_build_child VALUES(1,"+payload+"),(1,"+payload+")", 2))
					q := "ALTER TABLE pa_build_root ATTACH PARTITION pa_build_child FOR VALUES FROM(1) TO(3)"
					run(fixture.ImplicitError(q, fixture.Diagnostic{Code: "23505", Message: `could not create unique index "pa_build_child_k_payload_key"`, Detail: detail, Schema: "public", Table: "pa_build_child", Constraint: "pa_build_child_k_payload_key"}))
					run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_build_child'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
					run(fixture.Select("SELECT COUNT(*) FROM pg_class WHERE relname='pa_build_child_k_payload_key'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
					run(fixture.Select("SELECT COUNT(*) FROM pa_build_child", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
					run(fixture.Select("SELECT k,payload FROM pa_build_root", []string{"k", "payload"}, []string{"INT4", "TEXT"}, nil))
					if !nnd {
						// A genuine failed concurrent build retains an invalid candidate.
						run(fixture.ImplicitError("CREATE UNIQUE INDEX CONCURRENTLY pa_build_invalid ON pa_build_child(k,payload)", fixture.Diagnostic{Code: "23505", Message: `could not create unique index "pa_build_invalid"`, Detail: detail, Schema: "public", Table: "pa_build_child", Constraint: "pa_build_invalid"}))
						run(fixture.Select("SELECT indisvalid,indisready,indislive FROM pg_index WHERE indexrelid='pa_build_invalid'::regclass::OID", []string{"indisvalid", "indisready", "indislive"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{false, false, true}}))
						run(fixture.Exec("DELETE FROM pa_build_child", 2))
						run(fixture.Exec("INSERT INTO pa_build_child VALUES(1,'one')", 1))
						run(fixture.Exec(q, 0))
						run(partitionConstraintEdge("pa_build_child_k_payload_key", "pa_build_key", "pa_build_child"))
						run(fixture.Select("SELECT relispartition FROM pg_class WHERE relname='pa_build_invalid'", []string{"relispartition"}, []string{"BOOL"}, [][]any{{false}}))
						run(fixture.Select("SELECT indisvalid FROM pg_index WHERE indexrelid='pa_build_invalid'::regclass::OID", []string{"indisvalid"}, []string{"BOOL"}, [][]any{{false}}))
					}
				})
			}
		})
	}
}

func partitionAttachNestedIndexes(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run("nested/"+b.Name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
			run(fixture.Exec("CREATE TABLE pa_tree_root(k INT,j INT,CONSTRAINT pa_tree_key PRIMARY KEY(k,j)) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pa_tree_mid(j INT NOT NULL,k INT NOT NULL,CONSTRAINT pa_tree_mid_key PRIMARY KEY(k,j)) PARTITION BY RANGE(j)", 0))
			run(fixture.Exec("CREATE TABLE pa_tree_leaf PARTITION OF pa_tree_mid DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pa_tree_leaf VALUES(9,1)", 1))
			run(fixture.Exec("ALTER TABLE pa_tree_root ATTACH PARTITION pa_tree_mid FOR VALUES FROM(1) TO(3)", 0))
			run(partitionConstraintEdge("pa_tree_mid_key", "pa_tree_key", "pa_tree_mid"))
			run(partitionConstraintEdge("pa_tree_leaf_pkey", "pa_tree_mid_key", "pa_tree_leaf"))
			run(partitionConstraintIndexState("pa_tree_key", true))
			run(partitionConstraintIndexState("pa_tree_mid_key", true))
			run(partitionConstraintDuplicate("INSERT INTO pa_tree_root VALUES(1,9)", "pa_tree_leaf", "pa_tree_leaf_pkey", "Key (k, j)=(1, 9) already exists."))
		})
	}
}
