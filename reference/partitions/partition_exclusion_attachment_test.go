package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionAttachment(t *testing.T) {
	for _, operation := range []string{"attach", "recursive_add"} {
		for _, localKeys := range []string{partitionExclusionKeys, "during WITH &&"} {
			partitionExclusionCase(t, operation+"/"+localKeys, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
				run := partitionConstraintRun(t, p, b, prepared)
				run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
				if operation == "attach" {
					run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
					run(fixture.Exec("CREATE TABLE pe_candidate(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_local EXCLUDE USING gist("+localKeys+"))", 0))
				} else {
					run(fixture.Exec("CREATE TABLE pe_candidate PARTITION OF pe_root DEFAULT", 0))
					run(fixture.Exec("ALTER TABLE pe_candidate ADD CONSTRAINT pe_local EXCLUDE USING gist("+localKeys+")", 0))
				}
				run(fixture.Exec("INSERT INTO pe_candidate VALUES(1,'10.0.0.1','[1,5)')", 1))
				var constraintOID, indexOID int64
				if err := b.DB.QueryRowContext(p.Ctx, "SELECT oid,conindid FROM pg_constraint WHERE conrelid='pe_candidate'::regclass::OID AND conname='pe_local'").Scan(&constraintOID, &indexOID); err != nil {
					t.Fatal(err)
				}
				if operation == "attach" {
					run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_candidate DEFAULT", 0))
				} else {
					run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
				}
				run(fixture.Select("SELECT oid=$1::OID AS same_constraint,conindid=$2::OID AS same_index,conparentid=0 AS independent,conislocal FROM pg_constraint WHERE conrelid='pe_candidate'::regclass::OID AND conname='pe_local'", []string{"same_constraint", "same_index", "independent", "conislocal"}, []string{"BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true, true}}, constraintOID, indexOID))
				run(fixture.Select("SELECT c.conname,c.conislocal,p.conname AS parent,c.conindid<>$1::OID AS fresh FROM pg_constraint c JOIN pg_constraint p ON p.oid=c.conparentid WHERE c.conrelid='pe_candidate'::regclass::OID AND c.contype='x'", []string{"conname", "conislocal", "parent", "fresh"}, []string{"NAME", "BOOL", "NAME", "BOOL"}, [][]any{{"pe_candidate_tenant_during_excl", false, "pe_ex", true}}, indexOID))
				run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
				run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pe_candidate'::regclass::OID AND contype='x' ORDER BY conname", []string{"conname"}, []string{"NAME"}, [][]any{{"pe_local"}}))
				keys, proposed, existing := "tenant, during", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"
				if localKeys == "during WITH &&" {
					keys, proposed, existing = "during", "[3,7)", "[1,5)"
				}
				run(fixture.ErrorDiagnostic("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", fixture.ExclusionReferenceConflict("pe_candidate", "pe_local", keys, proposed, existing)))
				run(partitionExclusionIDs(1))
			})
		}
	}
	partitionExclusionCase(t, "explicit_matching_index_rejected", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_root(tenant INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_child PARTITION OF pe_root DEFAULT", 0))
		// ONLY avoids auto-cloning a child provider that would mask comparison
		// with the earlier 'another index already attached' diagnostic.
		run(fixture.Exec("ALTER TABLE ONLY pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
		run(fixture.Exec("ALTER TABLE pe_child ADD CONSTRAINT pe_child_ex EXCLUDE USING gist("+partitionExclusionKeys+")", 0))
		graph := fixture.Select("SELECT t.relname,c.conname,c.conparentid=0 AS independent,c.conislocal,i.relkind FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid WHERE t.relname IN ('pe_root','pe_child') AND c.contype='x' ORDER BY t.relname,c.conname", []string{"relname", "conname", "independent", "conislocal", "relkind"}, []string{"NAME", "NAME", "BOOL", "BOOL", "CHAR"}, [][]any{{"pe_child", "pe_child_ex", true, true, "i"}, {"pe_root", "pe_ex", true, true, "I"}})
		run(graph)
		run(fixture.Exec("INSERT INTO pe_root VALUES('10.0.0.1','[1,5)')", 1))
		conflict := partitionExclusionConflict("INSERT INTO pe_root VALUES('10.0.0.1','[3,7)')", "pe_child", "pe_child_ex", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)")
		run(conflict)
		run(referenceRangeError("ALTER INDEX pe_ex ATTACH PARTITION pe_child_ex", "42P17", `cannot attach index "pe_child_ex" as a partition of index "pe_ex"`, fixture.Diagnostic{Detail: "The index definitions do not match."}))
		run(graph)
		run(conflict)
		run(fixture.Select("SELECT tenant,during FROM pe_root", []string{"tenant", "during"}, []string{"INET", "INT4RANGE"}, [][]any{{"10.0.0.1", "[1,5)"}}))
	})
	partitionExclusionCase(t, "detach_reattach_preserves_local_tree", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_low", 0))
		run(fixture.Select("SELECT conparentid=0 AS independent,conislocal FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND contype='x'", []string{"independent", "conislocal"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}))
		run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_low FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
		run(fixture.Select("SELECT conname,conparentid=0 AS independent FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND contype='x' ORDER BY conname", []string{"conname", "independent"}, []string{"NAME", "BOOL"}, [][]any{{"pe_low_tenant_during_excl", true}, {"pe_low_tenant_during_excl1", false}}))
		run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
		run(fixture.Exec("INSERT INTO pe_low VALUES(1,'10.0.0.1','[1,5)')", 1))
		run(partitionExclusionConflict("INSERT INTO pe_low VALUES(2,'10.0.0.1','[3,7)')", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	})
}
