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
				compatible := localKeys == partitionExclusionKeys
				count := int64(0)
				if compatible {
					count = 1
				}
				run(fixture.Select("SELECT oid=$1::OID AS same_constraint,conindid=$2::OID AS same_index,conparentid=0 AS independent,conislocal,coninhcount,connoinherit FROM pg_constraint WHERE conrelid='pe_candidate'::regclass::OID AND conname='pe_local'", []string{"same_constraint", "same_index", "independent", "conislocal", "coninhcount", "connoinherit"}, []string{"BOOL", "BOOL", "BOOL", "BOOL", "INT2", "BOOL"}, [][]any{{true, true, !compatible, !compatible, count, true}}, constraintOID, indexOID))
				name, fresh := "pe_candidate_tenant_during_excl", true
				if compatible {
					name, fresh = "pe_local", false
				}
				run(fixture.Select("SELECT c.conname,c.conislocal,p.conname AS parent,c.conindid<>$1::OID AS fresh FROM pg_constraint c JOIN pg_constraint p ON p.oid=c.conparentid WHERE c.conrelid='pe_candidate'::regclass::OID AND c.contype='x'", []string{"conname", "conislocal", "parent", "fresh"}, []string{"NAME", "BOOL", "NAME", "BOOL"}, [][]any{{name, false, "pe_ex", fresh}}, indexOID))
				if compatible {
					run(partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", "pe_candidate", "pe_local", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
				}
				run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
				if compatible {
					run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pe_candidate'::regclass::OID AND contype='x'", []string{"conname"}, []string{"NAME"}, nil))
					run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.1','[3,7)')", 1))
					run(partitionExclusionIDs(1, 2))
					return
				}
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
	partitionExclusionCase(t, "explicit_matching_index", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
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
		ids := partitionAdoptionIdentity(t, p, b.DB, "pe_child")
		run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_child_ex", 0))
		run(fixture.Exec("ALTER INDEX pe_ex ATTACH PARTITION pe_child_ex", 0))
		run(partitionAdoptionSame("pe_child", ids))
		run(partitionAdoptionGraph([][]any{
			partitionAdoptionRow("pe_child", "pe_child_ex", "pe_ex", false, true, "i", true),
			partitionAdoptionRow("pe_root", "pe_ex", nil, true, true, "I", true),
		}))
		run(conflict)
		run(fixture.Select("SELECT tenant,during FROM pe_root", []string{"tenant", "during"}, []string{"INET", "INT4RANGE"}, [][]any{{"10.0.0.1", "[1,5)"}}))
	})
	partitionExclusionCase(t, "detach_reattach_preserves_local_tree", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("ALTER TABLE pe_root DETACH PARTITION pe_low", 0))
		ids := partitionAdoptionIdentity(t, p, b.DB, "pe_low")
		run(fixture.Select("SELECT conparentid=0 AS independent,conislocal,connoinherit FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND contype='x'", []string{"independent", "conislocal", "connoinherit"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, false}}))
		run(fixture.Exec("ALTER TABLE pe_root ATTACH PARTITION pe_low FOR VALUES FROM('10.0.0.0') TO('10.0.0.128')", 0))
		run(partitionAdoptionSame("pe_low", ids))
		run(fixture.Select("SELECT conname,conparentid=0 AS independent,connoinherit FROM pg_constraint WHERE conrelid='pe_low'::regclass::OID AND contype='x' ORDER BY conname", []string{"conname", "independent", "connoinherit"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"pe_low_tenant_during_excl", false, false}}))
		run(fixture.Exec("ALTER TABLE pe_root DROP CONSTRAINT pe_ex", 0))
		run(fixture.Exec("INSERT INTO pe_low VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.1','[3,7)')", 2))
	})
}
