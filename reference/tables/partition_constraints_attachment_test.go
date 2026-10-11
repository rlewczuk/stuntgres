package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsAttachment(t *testing.T) {
	partitionConstraintCase(t, "completion_and_repeat", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionConstraintManualTree(run)
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_b_idx", 0))
		run(partitionConstraintIndexState("pc_root_idx", true))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
		run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x'),(3,'x')", 2))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(1,'x')", "pc_a", "pc_a_idx", "Key (k, payload)=(1, x) already exists."))
	})
	partitionConstraintCase(t, "nested_validity", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(a INT,b INT) PARTITION BY RANGE(a)", 0))
		run(fixture.Exec("CREATE TABLE pc_mid PARTITION OF pc_root DEFAULT PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_mid DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_root_idx ON ONLY pc_root(a,b)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_mid_idx ON ONLY pc_mid(a,b)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_leaf_idx ON pc_leaf(a,b)", 0))
		run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_mid_idx", 0))
		run(partitionConstraintIndexState("pc_mid_idx", false))
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(fixture.Exec("ALTER INDEX pc_mid_idx ATTACH PARTITION pc_leaf_idx", 0))
		run(partitionConstraintIndexState("pc_mid_idx", true))
		run(partitionConstraintIndexState("pc_root_idx", true))
		run(partitionConstraintEdge("pc_leaf_idx", "pc_mid_idx", "pc_leaf"))
		run(partitionConstraintEdge("pc_mid_idx", "pc_root_idx", "pc_mid"))
	})
	partitionConstraintCase(t, "childless_and_future_incomplete", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_empty(k INT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_empty_idx ON ONLY pc_empty(k)", 0))
		run(partitionConstraintIndexState("pc_empty_idx", true))
		partitionConstraintManualTree(run)
		// ONLY leaves existing children unattached, not future children uncloned.
		run(fixture.Exec("CREATE TABLE pc_future PARTITION OF pc_root FOR VALUES FROM(5) TO(7)", 0))
		run(partitionConstraintIndexState("pc_root_idx", false))
		run(partitionConstraintEdge("pc_future_k_payload_idx", "pc_root_idx", "pc_future"))
		run(fixture.Exec("INSERT INTO pc_root VALUES(5,'x')", 1))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(5,'x')", "pc_future", "pc_future_k_payload_idx", "Key (k, payload)=(5, x) already exists."))
	})
	partitionConstraintCase(t, "automatic_reuse", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root DEFAULT", 0))
		run(fixture.Exec("CREATE UNIQUE INDEX pc_existing ON pc_a(k,payload)", 0))
		var oid int64
		if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid FROM pg_class WHERE relname='pc_existing'").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("CREATE UNIQUE INDEX pc_root_idx ON pc_root(k,payload)", 0))
		run(fixture.Select("SELECT oid=$1::OID AS same FROM pg_class WHERE relname='pc_existing'", []string{"same"}, []string{"BOOL"}, [][]any{{true}}, oid))
		run(partitionConstraintEdge("pc_existing", "pc_root_idx", "pc_a"))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_a_k_payload_idx'", []string{"relname"}, []string{"NAME"}, nil))
	})
}

func TestReferencePartitionConstraintsAttachmentDiagnostics(t *testing.T) {
	for _, c := range []struct{ name, child, state, detail string }{
		{"unrelated", "pc_other_idx", "55000", `Index "pc_other_idx" is not an index on any partition of table "pc_root".`},
		{"second", "pc_second_idx", "55000", `Another index is already attached for partition "pc_a".`},
		{"other_parent", "pc_b_idx", "55000", `Index "pc_b_idx" is already attached to another index.`},
		{"key_order", "pc_wrong_idx", "42P17", "The index definitions do not match."},
	} {
		partitionConstraintCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			partitionConstraintManualTree(run)
			run(fixture.Exec("ALTER INDEX pc_root_idx ATTACH PARTITION pc_a_idx", 0))
			switch c.name {
			case "unrelated":
				run(fixture.Exec("CREATE TABLE pc_other(k INT,payload TEXT)", 0))
				run(fixture.Exec("CREATE UNIQUE INDEX pc_other_idx ON pc_other(k,payload)", 0))
			case "second":
				run(fixture.Exec("CREATE UNIQUE INDEX pc_second_idx ON pc_a(k,payload)", 0))
			case "other_parent":
				run(fixture.Exec("CREATE UNIQUE INDEX pc_other_root_idx ON ONLY pc_root(k,payload)", 0))
				run(fixture.Exec("ALTER INDEX pc_other_root_idx ATTACH PARTITION pc_b_idx", 0))
			case "key_order":
				run(fixture.Exec("CREATE UNIQUE INDEX pc_wrong_idx ON pc_b(payload,k)", 0))
			}
			run(referenceRangeError("ALTER INDEX pc_root_idx ATTACH PARTITION "+c.child, c.state, `cannot attach index "`+c.child+`" as a partition of index "pc_root_idx"`, fixture.Diagnostic{Detail: c.detail}))
			run(partitionConstraintIndexState("pc_root_idx", false))
			run(partitionConstraintEdge("pc_a_idx", "pc_root_idx", "pc_a"))
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,'x'),(3,'y')", 2))
			run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(3,'y')", "pc_b", "pc_b_idx", "Key (k, payload)=(3, y) already exists."))
		})
	}
}

func TestReferencePartitionConstraintsAttachmentEquivalence(t *testing.T) {
	for _, c := range []struct {
		name, definition string
		accept           bool
	}{
		{"descending_nulls", "(k DESC NULLS FIRST,payload DESC NULLS LAST)", true},
		{"nnd", "NULLS NOT DISTINCT", false},
		{"include", "(k,payload) INCLUDE(extra)", false},
		{"predicate", "(k,payload) WHERE k>0", false},
		{"nonunique", "(k,payload)", false},
		{"opfamily", "(k,payload text_pattern_ops)", false},
		{"collation", `(k,payload COLLATE "C")`, false},
	} {
		partitionConstraintCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT,extra INT) PARTITION BY RANGE(k)", 0))
			run(fixture.Exec("CREATE TABLE pc_a PARTITION OF pc_root DEFAULT", 0))
			run(fixture.Exec("CREATE UNIQUE INDEX pc_root_idx ON ONLY pc_root(k,payload)", 0))
			definition := c.definition
			if c.name == "nnd" {
				definition = "(k,payload) NULLS NOT DISTINCT"
			}
			prefix := "CREATE UNIQUE INDEX pc_child_idx ON pc_a"
			if c.name == "nonunique" {
				prefix = "CREATE INDEX pc_child_idx ON pc_a"
			}
			run(fixture.Exec(prefix+definition, 0))
			query := "ALTER INDEX pc_root_idx ATTACH PARTITION pc_child_idx"
			if c.accept {
				run(fixture.Exec(query, 0))
				run(partitionConstraintEdge("pc_child_idx", "pc_root_idx", "pc_a"))
			} else {
				run(referenceRangeError(query, "42P17", `cannot attach index "pc_child_idx" as a partition of index "pc_root_idx"`, fixture.Diagnostic{Detail: "The index definitions do not match."}))
			}
			run(partitionConstraintIndexState("pc_root_idx", c.accept))
		})
	}
}
