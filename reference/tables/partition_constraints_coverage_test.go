package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsCoverage(t *testing.T) {
	for _, c := range []struct{ name, declaration, kind, missing string }{
		{"unique_first", "UNIQUE(b)", "UNIQUE", "a"},
		{"unique_second", "UNIQUE(a)", "UNIQUE", "b"},
		{"unique_both", "UNIQUE(payload)", "UNIQUE", "a"},
		{"primary_first", "PRIMARY KEY(b)", "PRIMARY KEY", "a"},
		{"primary_second", "PRIMARY KEY(a)", "PRIMARY KEY", "b"},
	} {
		partitionConstraintCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(partitionConstraintMissing("CREATE TABLE pc_bad(a INT,b INT,payload TEXT,"+c.declaration+") PARTITION BY RANGE(a,b)", "pc_bad", c.kind, c.missing))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_bad' OR relname='pc_bad_a_key' OR relname='pc_bad_b_key' OR relname='pc_bad_pkey'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Exec("CREATE TABLE pc_bad(a INT,b INT,payload TEXT,UNIQUE(b,a,payload)) PARTITION BY RANGE(a,b)", 0))
			run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_bad DEFAULT", 0))
			run(fixture.Exec("INSERT INTO pc_bad VALUES(1,2,'x'),(2,2,'x')", 2))
		})
	}
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		partitionConstraintCase(t, "expression_"+kind, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(referenceRangeError("CREATE TABLE pc_expr(k INT,"+kind+"(k)) PARTITION BY RANGE((k+1))", "0A000", "unsupported "+kind+" constraint with partition key definition", fixture.Diagnostic{Detail: kind + " constraints cannot be used when partition keys include expressions."}))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_expr'", []string{"relname"}, []string{"NAME"}, nil))
		})
	}
	partitionConstraintCase(t, "include_not_key", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(k INT,payload TEXT) PARTITION BY RANGE(k)", 0))
		run(partitionConstraintMissing("CREATE UNIQUE INDEX pc_bad ON pc_root(payload) INCLUDE(k)", "pc_root", "UNIQUE", "k"))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_bad'", []string{"relname"}, []string{"NAME"}, nil))
	})
	partitionConstraintCase(t, "effective_collation", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec(`CREATE TABLE pc_root(k TEXT COLLATE "C",payload INT) PARTITION BY RANGE(k)`, 0))
		run(partitionConstraintMissing(`CREATE UNIQUE INDEX pc_bad ON pc_root(k COLLATE "POSIX")`, "pc_root", "UNIQUE", "k"))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_bad'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Exec(`CREATE UNIQUE INDEX pc_good ON pc_root(k COLLATE "C")`, 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES('x',1)", 1))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES('x',2)", "pc_leaf", "pc_leaf_k_idx", "Key (k)=(x) already exists."))
	})
}

func TestReferencePartitionConstraintsCoveragePrecedence(t *testing.T) {
	for _, kind := range []string{"UNIQUE", "PRIMARY KEY"} {
		partitionConstraintCase(t, "descendant_"+kind, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			run(fixture.Exec("CREATE TABLE pc_root(a INT,b INT,"+kind+"(a)) PARTITION BY RANGE(a)", 0))
			run(partitionConstraintMissing("CREATE TABLE pc_mid PARTITION OF pc_root FOR VALUES FROM(1) TO(3) PARTITION BY RANGE(b)", "pc_mid", kind, "b"))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pc_mid' OR relname='pc_mid_pkey' OR relname='pc_mid_a_key'", []string{"relname"}, []string{"NAME"}, nil))
			run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pc_mid_pkey' OR conname='pc_mid_a_key'", []string{"conname"}, []string{"NAME"}, nil))
			run(referenceRangeError("CREATE TABLE pc_probe(v pc_mid)", "42704", `type "pc_mid" does not exist`, fixture.Diagnostic{Position: "25"}))
			run(fixture.Exec("CREATE TABLE pc_mid PARTITION OF pc_root FOR VALUES FROM(1) TO(3)", 0))
			run(fixture.Exec("INSERT INTO pc_root VALUES(1,7)", 1))
		})
	}
	partitionConstraintCase(t, "local_key_omits_ancestor", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(a INT,b INT,payload TEXT) PARTITION BY RANGE(a)", 0))
		run(fixture.Exec("CREATE TABLE pc_mid PARTITION OF pc_root(CONSTRAINT pc_mid_key UNIQUE(b,payload)) FOR VALUES FROM(1) TO(3) PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_mid(CONSTRAINT pc_local UNIQUE(payload)) DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,1,'x')", 1))
		run(partitionConstraintDuplicate("INSERT INTO pc_root VALUES(2,2,'x')", "pc_leaf", "pc_local", "Key (payload)=(x) already exists."))
	})
}

func TestReferencePartitionConstraintsClones(t *testing.T) {
	partitionConstraintCase(t, "immediate_identity_and_future", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pc_root(a INT,b INT,payload TEXT,CONSTRAINT pc_key PRIMARY KEY(a,b)) PARTITION BY RANGE(a)", 0))
		run(fixture.Exec("CREATE TABLE pc_mid PARTITION OF pc_root FOR VALUES FROM(1) TO(3) PARTITION BY RANGE(b)", 0))
		run(fixture.Exec("CREATE TABLE pc_leaf PARTITION OF pc_mid(CONSTRAINT pc_local UNIQUE(payload)) FOR VALUES FROM(1) TO(3)", 0))
		check := func(table, index, parent string, local bool, count int64, kind string) {
			parentTest := "c.conparentid=0::OID"
			if parent != "" {
				parentTest = "c.conparentid=p.oid"
			}
			query := "SELECT c.conname,c.conindid=i.oid AS backing," + parentTest + " AS parent,c.conislocal,c.coninhcount,i.relkind,i.relispartition,c.oid>0::OID AS positive_constraint,i.oid>0::OID AS positive_index,c.oid<>i.oid AS distinct_ids FROM pg_constraint c JOIN pg_class i ON i.oid=c.conindid"
			if parent != "" {
				query += " JOIN pg_constraint p ON p.conname='" + parent + "'"
			}
			query += " WHERE c.conrelid='" + table + "'::regclass::OID AND c.contype='p'"
			run(fixture.Select(query, []string{"conname", "backing", "parent", "conislocal", "coninhcount", "relkind", "relispartition", "positive_constraint", "positive_index", "distinct_ids"}, []string{"NAME", "BOOL", "BOOL", "BOOL", "INT2", "CHAR", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{index, true, true, local, count, kind, parent != "", true, true, true}}))
		}
		check("pc_root", "pc_key", "", true, 0, "I")
		check("pc_mid", "pc_mid_pkey", "pc_key", false, 1, "I")
		check("pc_leaf", "pc_leaf_pkey", "pc_mid_pkey", false, 1, "i")
		run(partitionConstraintEdge("pc_mid_pkey", "pc_key", "pc_mid"))
		run(partitionConstraintEdge("pc_leaf_pkey", "pc_mid_pkey", "pc_leaf"))
		run(fixture.Select("SELECT conname,conparentid=0::OID AS local_parent,conislocal,coninhcount FROM pg_constraint WHERE conname='pc_local'", []string{"conname", "local_parent", "conislocal", "coninhcount"}, []string{"NAME", "BOOL", "BOOL", "INT2"}, [][]any{{"pc_local", true, true, int64(0)}}))
		run(fixture.Exec("ALTER INDEX pc_leaf_pkey RENAME TO pc_custom", 0))
		check("pc_leaf", "pc_custom", "pc_mid_pkey", false, 1, "i")
		run(fixture.Exec("CREATE TABLE pc_future PARTITION OF pc_mid FOR VALUES FROM(3) TO(5)", 0))
		check("pc_future", "pc_future_pkey", "pc_mid_pkey", false, 1, "i")
		run(fixture.Exec("INSERT INTO pc_root VALUES(1,1,'x'),(1,3,'y')", 2))
		run(partitionConstraintDuplicate("INSERT INTO pc_mid VALUES(1,1,'z')", "pc_leaf", "pc_custom", "Key (a, b)=(1, 1) already exists."))
	})
}
