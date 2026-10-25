package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePartitionConstraintsCatalogMetadata(t *testing.T) {
	partitionConstraintCase(t, "empty_narrow_catalog_fields", func(t *testing.T, p *fixture.Pair, b fixture.Backend, prepared bool) {
		run := partitionConstraintRun(t, p, b, prepared)
		for _, catalog := range []struct {
			query        string
			names, types []string
		}{
			{"SELECT conindid,conparentid,conislocal,coninhcount FROM pg_constraint WHERE FALSE", []string{"conindid", "conparentid", "conislocal", "coninhcount"}, []string{"OID", "OID", "BOOL", "INT2"}},
			{"SELECT reloftype,relispartition FROM pg_class WHERE FALSE", []string{"reloftype", "relispartition"}, []string{"OID", "BOOL"}},
			{"SELECT indexrelid,indrelid,indisunique,indisprimary,indisvalid,indisready,indislive,indnullsnotdistinct FROM pg_index WHERE FALSE", []string{"indexrelid", "indrelid", "indisunique", "indisprimary", "indisvalid", "indisready", "indislive", "indnullsnotdistinct"}, []string{"OID", "OID", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL", "BOOL"}},
		} {
			step := fixture.Select(catalog.query, catalog.names, catalog.types, nil)
			step.CheckMetadata = referenceRangeMetadata(b.PG, catalog.names, catalog.types, make([]bool, len(catalog.names)))
			run(step)
		}
	})
}
