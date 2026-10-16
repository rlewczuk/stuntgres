package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionCollation(t *testing.T) {
	partitionExclusionCase(t, "coverage_and_clones", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		// A deterministic user-owned copy of C has the same comparison behavior
		// but a different identity. Coverage must match the collation OID, not
		// merely equivalent sorting behavior or the same attribute/operator.
		run(fixture.Exec(`CREATE COLLATION pec_owned FROM pg_catalog."C"`, 0))
		run(partitionConstraintMissing(`CREATE TABLE pec_root(k TEXT COLLATE "C",v TEXT COLLATE pec_owned,CONSTRAINT pec_ex EXCLUDE USING btree(k COLLATE pec_owned WITH =,v WITH =)) PARTITION BY RANGE(k)`, "pec_root", "EXCLUDE", "k"))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pec_root','pec_ex') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pec_ex'", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Exec(`CREATE TABLE pec_root(k TEXT COLLATE "C",v TEXT COLLATE pec_owned,CONSTRAINT pec_ex EXCLUDE USING btree(k COLLATE "C" WITH =,v COLLATE pec_owned WITH =)) PARTITION BY RANGE(k)`, 0))
		graph := func(rows [][]any) fixture.Step {
			return fixture.Select("SELECT t.relname,c.conname,p.conname AS parent,c.conislocal,c.coninhcount,c.connoinherit,c.condeferrable,c.condeferred,i.relkind,i.relam=403 AS btree FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid LEFT JOIN pg_constraint p ON p.oid=c.conparentid WHERE t.relname IN ('pec_root','pec_mid','pec_leaf','pec_sub','pec_subleaf') AND c.contype='x' ORDER BY t.relname,c.conname", []string{"relname", "conname", "parent", "conislocal", "coninhcount", "connoinherit", "condeferrable", "condeferred", "relkind", "btree"}, []string{"NAME", "NAME", "NAME", "BOOL", "INT2", "BOOL", "BOOL", "BOOL", "CHAR", "BOOL"}, rows)
		}
		root := []any{"pec_root", "pec_ex", nil, true, int64(0), true, false, false, "I", true}
		run(graph([][]any{root}))
		// Future descendant cloning must recheck the descendant partition
		// collation, even though both EXCLUDE equality operators are correct.
		run(partitionConstraintMissing(`CREATE TABLE pec_mid PARTITION OF pec_root FOR VALUES FROM('a') TO('m') PARTITION BY RANGE(v COLLATE "C")`, "pec_mid", "EXCLUDE", "v"))
		run(graph([][]any{root}))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pec_mid','pec_mid_k_v_excl') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conname='pec_mid_k_v_excl'", []string{"conname"}, []string{"NAME"}, nil))
		// Reusing the rejected names proves no stale relation/index reservation.
		run(fixture.Exec(`CREATE TABLE pec_mid PARTITION OF pec_root FOR VALUES FROM('a') TO('m') PARTITION BY RANGE(v COLLATE pec_owned)`, 0))
		run(fixture.Exec("CREATE TABLE pec_leaf PARTITION OF pec_mid DEFAULT", 0))
		leaf := []any{"pec_leaf", "pec_leaf_k_v_excl", "pec_mid_k_v_excl", false, int64(1), false, false, false, "i", true}
		mid := []any{"pec_mid", "pec_mid_k_v_excl", "pec_ex", false, int64(1), false, false, false, "I", true}
		before := [][]any{leaf, mid, root}
		run(graph(before))
		run(fixture.Exec("INSERT INTO pec_root VALUES('b','x')", 1))
		conflict := func(query, table, constraint, values string) fixture.Step {
			return fixture.ErrorDiagnostic(query, fixture.ExclusionReferenceConflict(table, constraint, "k, v", values, values))
		}
		run(conflict("INSERT INTO pec_root VALUES('b','x')", "pec_leaf", "pec_leaf_k_v_excl", "b, x"))
		// ATTACH must validate an existing populated subtree before publishing
		// any inherited constraint, while preserving its independent local one.
		run(fixture.Exec(`CREATE TABLE pec_sub(k TEXT COLLATE "C",v TEXT COLLATE pec_owned) PARTITION BY RANGE(v COLLATE "C")`, 0))
		run(fixture.Exec("CREATE TABLE pec_subleaf PARTITION OF pec_sub(CONSTRAINT pec_local EXCLUDE USING btree(k WITH =,v WITH =)) DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pec_sub VALUES('n','y')", 1))
		local := []any{"pec_subleaf", "pec_local", nil, true, int64(0), true, false, false, "i", true}
		failed := [][]any{leaf, mid, root, local}
		run(graph(failed))
		run(partitionConstraintMissing("ALTER TABLE pec_root ATTACH PARTITION pec_sub FOR VALUES FROM('m') TO('z')", "pec_sub", "EXCLUDE", "v"))
		run(graph(failed))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname IN ('pec_sub_k_v_excl','pec_subleaf_k_v_excl') ORDER BY relname", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT k,v FROM pec_root ORDER BY k,v", []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"b", "x"}}))
		run(fixture.Select("SELECT k,v FROM pec_sub ORDER BY k,v", []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"n", "y"}}))
		run(conflict("INSERT INTO pec_sub VALUES('n','y')", "pec_subleaf", "pec_local", "n, y"))
		run(conflict("INSERT INTO pec_root VALUES('b','x')", "pec_leaf", "pec_leaf_k_v_excl", "b, x"))
		// Recreate the candidate with matching coverage; the failed ATTACH's
		// inherited names must now be available for a fresh, complete graph.
		run(fixture.Exec("DROP TABLE pec_sub", 0))
		run(fixture.Exec(`CREATE TABLE pec_sub(k TEXT COLLATE "C",v TEXT COLLATE pec_owned) PARTITION BY RANGE(v COLLATE pec_owned)`, 0))
		run(fixture.Exec("CREATE TABLE pec_subleaf PARTITION OF pec_sub DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pec_sub VALUES('n','y')", 1))
		run(fixture.Exec("ALTER TABLE pec_root ATTACH PARTITION pec_sub FOR VALUES FROM('m') TO('z')", 0))
		sub := []any{"pec_sub", "pec_sub_k_v_excl", "pec_ex", false, int64(1), false, false, false, "I", true}
		subleaf := []any{"pec_subleaf", "pec_subleaf_k_v_excl", "pec_sub_k_v_excl", false, int64(1), false, false, false, "i", true}
		run(graph([][]any{leaf, mid, root, sub, subleaf}))
		run(conflict("INSERT INTO pec_root VALUES('n','y')", "pec_subleaf", "pec_subleaf_k_v_excl", "n, y"))
		run(fixture.Exec("INSERT INTO pec_root VALUES('b','y'),('n','x')", 2))
		run(fixture.Select("SELECT k,v FROM pec_root ORDER BY k,v", []string{"k", "v"}, []string{"TEXT", "TEXT"}, [][]any{{"b", "x"}, {"b", "y"}, {"n", "x"}, {"n", "y"}}))
	})
}
