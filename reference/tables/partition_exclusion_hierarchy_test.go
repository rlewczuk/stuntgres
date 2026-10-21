package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionHierarchy(t *testing.T) {
	partitionExclusionCase(t, "future_descendant_coverage", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,site INET,during INT4RANGE,CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")) PARTITION BY RANGE(tenant)", 0))
		run(partitionConstraintMissing("CREATE TABLE pe_mid PARTITION OF pe_root DEFAULT PARTITION BY RANGE(site)", "pe_mid", "EXCLUDE", "site"))
		run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pe_mid'", []string{"relname"}, []string{"NAME"}, nil))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pe_root'::regclass::OID AND contype='x'", []string{"conname"}, []string{"NAME"}, [][]any{{"pe_ex"}}))
		run(fixture.Exec("CREATE TABLE pe_mid PARTITION OF pe_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','192.0.2.1','[1,5)')", 1))
		run(partitionExclusionConflict("INSERT INTO pe_root VALUES(2,'10.0.0.1','192.0.2.2','[3,7)')", "pe_mid", "pe_mid_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	})
	partitionExclusionCase(t, "attach_nested_uncovered", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,site INET,during INT4RANGE,CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_mid(id INT,tenant INET,site INET,during INT4RANGE) PARTITION BY RANGE(site)", 0))
		run(fixture.Exec("CREATE TABLE pe_leaf PARTITION OF pe_mid DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pe_mid VALUES(1,'10.0.0.1','192.0.2.1','[1,5)')", 1))
		rootGraph := fixture.Select("SELECT conname,conparentid=0 AS independent,conislocal FROM pg_constraint WHERE conrelid='pe_root'::regclass::OID AND contype='x'", []string{"conname", "independent", "conislocal"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"pe_ex", true, true}})
		run(rootGraph)
		candidateGraph := fixture.Select("SELECT c.conname FROM pg_constraint c WHERE c.conrelid IN ('pe_mid'::regclass::OID,'pe_leaf'::regclass::OID) AND c.contype='x'", []string{"conname"}, []string{"NAME"}, nil)
		run(candidateGraph)
		run(partitionConstraintMissing("ALTER TABLE pe_root ATTACH PARTITION pe_mid DEFAULT", "pe_mid", "EXCLUDE", "site"))
		run(rootGraph)
		run(candidateGraph)
		run(fixture.Select("SELECT id FROM pe_root", []string{"id"}, []string{"INT4"}, nil))
		run(fixture.Select("SELECT id FROM pe_mid ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}}))
		// Reusing DEFAULT proves failed attachment did not reserve its bound;
		// the old root provider must still clone and enforce on a valid leaf.
		run(fixture.Exec("CREATE TABLE pe_ok PARTITION OF pe_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pe_root VALUES(2,'10.0.0.1','192.0.2.1','[1,5)')", 1))
		run(partitionExclusionConflict("INSERT INTO pe_root VALUES(3,'10.0.0.1','192.0.2.2','[3,7)')", "pe_ok", "pe_ok_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	})
	partitionExclusionCase(t, "recursive_add_and_scoped_local", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INET,site INET,during INT4RANGE) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_mid PARTITION OF pe_root DEFAULT PARTITION BY RANGE(site)", 0))
		run(fixture.Exec("CREATE TABLE pe_leaf PARTITION OF pe_mid DEFAULT", 0))
		// A declaration local to the intermediate subtree need not cover tenant.
		run(fixture.Exec("ALTER TABLE pe_mid ADD CONSTRAINT pe_local EXCLUDE USING gist(site inet_ops WITH =,during WITH &&)", 0))
		run(partitionConstraintMissing("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist("+partitionExclusionKeys+")", "pe_mid", "EXCLUDE", "site"))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pe_root'::regclass::OID AND contype='x'", []string{"conname"}, []string{"NAME"}, nil))
		run(fixture.Exec("ALTER TABLE pe_root ADD CONSTRAINT pe_ex EXCLUDE USING gist(tenant inet_ops WITH =,site inet_ops WITH =,during WITH &&)", 0))
		run(fixture.Select("SELECT t.relname,c.conname,p.conname AS parent FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid LEFT JOIN pg_constraint p ON p.oid=c.conparentid WHERE t.relname IN ('pe_root','pe_mid','pe_leaf') AND c.contype='x' ORDER BY t.relname,c.conname", []string{"relname", "conname", "parent"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"pe_leaf", "pe_leaf_site_during_excl", "pe_local"}, {"pe_leaf", "pe_leaf_tenant_site_during_excl", "pe_mid_tenant_site_during_excl"}, {"pe_mid", "pe_local", nil}, {"pe_mid", "pe_mid_tenant_site_during_excl", "pe_ex"}, {"pe_root", "pe_ex", nil}}))
	})
	partitionExclusionCase(t, "equality_family/LIST", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		query := "CREATE TABLE pe_root(id INT,tenant INET,during INT4RANGE,CONSTRAINT pe_ex EXCLUDE USING gist(" + partitionExclusionKeys + ")) PARTITION BY LIST(tenant)"
		run(fixture.Exec(query, 0))
		run(fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_root DEFAULT", 0))
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,'10.0.0.1','[1,5)'),(2,'10.0.0.2','[3,7)')", 2))
		run(partitionExclusionConflict("INSERT INTO pe_root VALUES(3,'10.0.0.1','[3,7)')", "pe_low", "pe_low_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	})
	partitionExclusionCase(t, "equality_family/HASH", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		// Existing INT hash partitioning proves equality-family coverage without
		// requiring an unrelated INET hash provider. RANGE cases prove overlap.
		run(fixture.Exec("CREATE TABLE pe_root(id INT,tenant INT,CONSTRAINT pe_ex EXCLUDE USING btree(tenant WITH =)) PARTITION BY HASH(tenant)", 0))
		run(fixture.Exec("CREATE TABLE pe_low PARTITION OF pe_root FOR VALUES WITH(MODULUS 1,REMAINDER 0)", 0))
		run(fixture.Select("SELECT c.conname,p.conname AS parent,c.conexclop[1]='=(integer,integer)'::regoperator::OID AS equality FROM pg_constraint c LEFT JOIN pg_constraint p ON p.oid=c.conparentid WHERE c.conrelid IN ('pe_root'::regclass::OID,'pe_low'::regclass::OID) AND c.contype='x' ORDER BY c.conname", []string{"conname", "parent", "equality"}, []string{"NAME", "NAME", "BOOL"}, [][]any{{"pe_ex", nil, true}, {"pe_low_tenant_excl", "pe_ex", true}}))
		run(fixture.Exec("INSERT INTO pe_root VALUES(1,10),(2,20)", 2))
		run(fixture.ErrorDiagnostic("INSERT INTO pe_root VALUES(3,10)", fixture.ExclusionReferenceConflict("pe_low", "pe_low_tenant_excl", "tenant", "10", "10")))
		run(partitionExclusionIDs(1, 2))
	})
}
