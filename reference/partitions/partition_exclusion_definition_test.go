package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionExclusionDefinition(t *testing.T) {
	for _, c := range []struct{ name, columns, keys, partition, missing string }{
		{"missing", "tenant INET,during INT4RANGE", "during WITH &&", "tenant", "tenant"},
		{"include_only", "tenant INET,during INT4RANGE", "during WITH &&) INCLUDE(tenant", "tenant", "tenant"},
		{"multikey_missing", "tenant INET,site INET,during INT4RANGE", partitionExclusionKeys, "tenant,site", "site"},
	} {
		partitionExclusionCase(t, c.name, func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
			run := partitionConstraintRun(t, p, b, prepared)
			q := "CREATE TABLE pe_bad(" + c.columns + ",CONSTRAINT pe_bad_ex EXCLUDE USING gist(" + c.keys + ")) PARTITION BY RANGE(" + c.partition + ")"
			run(partitionConstraintMissing(q, "pe_bad", "EXCLUDE", c.missing))
			run(fixture.Select("SELECT relname FROM pg_class WHERE relname='pe_bad'", []string{"relname"}, []string{"NAME"}, nil))
			// Failed definitions must release both the table and constraint names.
			run(fixture.Exec("CREATE TABLE pe_bad(tenant INET,during INT4RANGE,CONSTRAINT pe_bad_ex EXCLUDE USING gist("+partitionExclusionKeys+")) PARTITION BY RANGE(tenant)", 0))
		})
	}
	partitionExclusionCase(t, "non_equality", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(referenceRangeError("CREATE TABLE pe_bad(tenant INET,during INT4RANGE,EXCLUDE USING gist(tenant inet_ops WITH &&,during WITH &&)) PARTITION BY RANGE(tenant)", "0A000", `cannot match partition key to index on column "tenant" using non-equal operator "&&"`, fixture.Diagnostic{}))
	})
	partitionExclusionCase(t, "expression_partition_key", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(referenceRangeError("CREATE TABLE pe_bad(tenant INET,during INT4RANGE,EXCLUDE USING gist(tenant inet_ops WITH =,during WITH &&)) PARTITION BY RANGE((COALESCE(tenant,tenant)))", "0A000", "unsupported EXCLUDE constraint with partition key definition", fixture.Diagnostic{Detail: "EXCLUDE constraints cannot be used when partition keys include expressions."}))
	})
	partitionExclusionCase(t, "inet_requires_explicit_class", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(referenceRangeError("CREATE TABLE pe_bad(tenant INET,during INT4RANGE,EXCLUDE USING gist(tenant WITH =,during WITH &&)) PARTITION BY RANGE(tenant)", "42704", `data type inet has no default operator class for access method "gist"`, fixture.Diagnostic{Hint: "You must specify an operator class for the index or define a default operator class for the data type."}))
	})
	partitionExclusionCase(t, "binding_before_coverage", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		run(referenceRangeError("CREATE TABLE pe_bad(tenant INET,during INT4RANGE,EXCLUDE USING gist(during absent_ops WITH &&)) PARTITION BY RANGE(tenant)", "42704", `operator class "absent_ops" does not exist for access method "gist"`, fixture.Diagnostic{}))
	})
	partitionExclusionCase(t, "named_and_generated", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		partitionExclusionTree(run, "")
		run(fixture.Exec("CREATE TABLE pe_unnamed(tenant INET,during INT4RANGE,EXCLUDE USING gist("+partitionExclusionKeys+")) PARTITION BY RANGE(tenant)", 0))
		run(fixture.Select("SELECT conname FROM pg_constraint WHERE conrelid='pe_unnamed'::regclass::OID AND contype='x'", []string{"conname"}, []string{"NAME"}, [][]any{{"pe_unnamed_tenant_during_excl"}}))
		run(partitionExclusionGraph())
	})
}
