package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionMetadata(t *testing.T) {
	partitionExclusionCase(t, "provider_graph", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(partitionExclusionGraph())
		// GiST's released access-method OID is 783. pg_am/pg_inherits are
		// outside the native catalog surface; inspect the actual backing and
		// immediate constraint ancestry without requiring either catalog.
		providers := fixture.Select("SELECT t.relname,i.relam::OID AS relam,c.contype='x' AS is_exclusion,x.indisunique,x.indisvalid,x.indisready,c.conkey::TEXT AS keys,c.conexclop[1]='=(inet,inet)'::regoperator::OID AS equality,c.conexclop[2]='&&(anyrange,anyrange)'::regoperator::OID AS overlap FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid JOIN pg_class i ON i.oid=c.conindid JOIN pg_index x ON x.indexrelid=i.oid WHERE t.relname IN ('pe_root','pe_low','pe_other') AND c.contype='x' ORDER BY t.relname",
			[]string{"relname", "relam", "is_exclusion", "indisunique", "indisvalid", "indisready", "keys", "equality", "overlap"}, []string{"NAME", "OID", "BOOL", "BOOL", "BOOL", "BOOL", "TEXT", "BOOL", "BOOL"},
			[][]any{{"pe_low", int64(783), true, false, true, true, "{2,3}", true, true}, {"pe_other", int64(783), true, false, true, true, "{2,3}", true, true}, {"pe_root", int64(783), true, false, true, true, "{2,3}", true, true}})
		providers.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, providers)
		run(fixture.Select("SELECT child.relname,parent.relname AS parent FROM pg_constraint c JOIN pg_constraint p ON p.oid=c.conparentid JOIN pg_class child ON child.oid=c.conindid JOIN pg_class parent ON parent.oid=p.conindid WHERE parent.relname='pe_ex' ORDER BY child.relname", []string{"relname", "parent"}, []string{"NAME", "NAME"}, [][]any{{"pe_low_tenant_during_excl", "pe_ex"}, {"pe_other_tenant_during_excl", "pe_ex"}}))
		run(fixture.Select("SELECT pg_get_constraintdef(oid) AS definition FROM pg_constraint WHERE conrelid='pe_root'::regclass::OID AND conname='pe_ex'", []string{"definition"}, []string{"TEXT"}, [][]any{{"EXCLUDE USING gist (tenant inet_ops WITH =, during WITH &&)"}}))
		run(fixture.Select("SELECT pg_get_indexdef('pe_ex'::regclass::OID) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX pe_ex ON ONLY public.pe_root USING gist (tenant inet_ops, during)"}}))
		// Actual typed columns, not cast-to-text substitutes, expose the API types.
		s := fixture.Select("SELECT id,tenant,during FROM pe_root WHERE FALSE", []string{"id", "tenant", "during"}, []string{"INT4", "INET", "INT4RANGE"}, nil)
		s.CheckMetadata = referenceRangeMetadata(b.PG, s.Columns, s.Types, []bool{true, true, true})
		run(s)
	})
	partitionExclusionCase(t, "deferrable_clones", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "DEFERRABLE INITIALLY DEFERRED")
		run(fixture.Select("SELECT t.relname,c.condeferrable,c.condeferred FROM pg_constraint c JOIN pg_class t ON t.oid=c.conrelid WHERE t.relname IN ('pe_root','pe_low','pe_other') AND c.contype='x' ORDER BY t.relname", []string{"relname", "condeferrable", "condeferred"}, []string{"NAME", "BOOL", "BOOL"}, [][]any{{"pe_low", true, true}, {"pe_other", true, true}, {"pe_root", true, true}}))
	})
	partitionExclusionCase(t, "ordinary_network_operator_identity", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(fixture.Exec("CREATE TABLE pe_network(tenant INET,CONSTRAINT pe_network_ex EXCLUDE USING gist(tenant inet_ops WITH &&))", 0))
		operator := fixture.Select("SELECT c.conexclop[1]='&&(inet,inet)'::regoperator::OID AS actual_operator,o.oprleft::OID AS oprleft,o.oprright::OID AS oprright,o.oprresult::OID AS oprresult,o.oprcom::OID AS oprcom,o.oprnegate::OID AS oprnegate,o.oprcanhash,o.oprcanmerge FROM pg_constraint c JOIN pg_operator o ON o.oid=c.conexclop[1] WHERE c.conrelid='pe_network'::regclass::OID AND c.conname='pe_network_ex'",
			[]string{"actual_operator", "oprleft", "oprright", "oprresult", "oprcom", "oprnegate", "oprcanhash", "oprcanmerge"}, []string{"BOOL", "OID", "OID", "OID", "OID", "OID", "BOOL", "BOOL"},
			[][]any{{true, int64(869), int64(869), int64(16), int64(3552), int64(0), false, false}})
		operator.Prepared = prepared
		fixture.PgDescriptionRows(t, p, b, operator)
		run(fixture.Exec("INSERT INTO pe_network VALUES('10.0.0.0/24'),('10.0.1.0/24')", 2))
		run(fixture.ErrorDiagnostic("INSERT INTO pe_network VALUES('10.0.0.1')", fixture.ExclusionReferenceConflict("pe_network", "pe_network_ex", "tenant", "10.0.0.1", "10.0.0.0/24")))
	})
	partitionExclusionCase(t, "copied_inherited_provider_is_local", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("CREATE TABLE pe_copy(LIKE pe_low INCLUDING INDEXES)", 0))
		run(fixture.Select("SELECT conislocal,conparentid=0 AS independent,connoinherit FROM pg_constraint WHERE conrelid='pe_copy'::regclass::OID AND contype='x'", []string{"conislocal", "independent", "connoinherit"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
		run(fixture.Exec("INSERT INTO pe_copy VALUES(1,'10.0.0.1','[1,5)')", 1))
		run(partitionExclusionConflict("INSERT INTO pe_copy VALUES(2,'10.0.0.1','[3,7)')", "pe_copy", "pe_copy_tenant_during_excl", "10.0.0.1, [3,7)", "10.0.0.1, [1,5)"))
	})
}
