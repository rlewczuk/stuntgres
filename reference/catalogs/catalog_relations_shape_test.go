package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

var catalogRelationsColumns = []string{"fktable", "fkcols", "pktable", "pkcols", "is_array", "is_opt"}
var catalogRelationsTypes = []string{"REGCLASS", "_TEXT", "REGCLASS", "_TEXT", "BOOL", "BOOL"}

func catalogRelationsShape(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
	for _, prepared := range []bool{false, true} {
		for _, suffix := range []string{" WHERE false", " LIMIT 0"} {
			s := fixture.Select("SELECT * FROM pg_catalog.pg_get_catalog_foreign_keys()"+suffix, catalogRelationsColumns, catalogRelationsTypes, nil)
			s.Prepared = prepared
			s.CheckMetadata = fixture.CatalogRelationsMetadata(b.PG)
			run(s)
		}
		s := fixture.Select("SELECT pg_get_catalog_foreign_keys() AS link LIMIT 0", []string{"link"}, []string{"RECORD"}, nil)
		s.Prepared = prepared
		s.CheckMetadata = fixture.CatalogRelationsMetadata(b.PG)
		run(s)
		s = catalogRelationsInventoryStep("(SELECT (pg_get_catalog_foreign_keys()).*) AS expanded")
		s.Prepared = prepared
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		s = catalogRelationsInventoryStep("pg_get_catalog_foreign_keys() AS links")
		s.Prepared = prepared
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		s = fixture.Select("SELECT a::oid,b,c::oid,d,e,f FROM pg_get_catalog_foreign_keys() AS links(a,b,c,d,e,f)", []string{"a", "b", "c", "d", "e", "f"}, []string{"OID", "_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}, catalogRelationsRelease219)
		s.Bag = true
		s.Prepared = prepared
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		s = fixture.Select("SELECT src::oid,fkcols,pktable::oid,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys() AS links(src)", []string{"src", "fkcols", "pktable", "pkcols", "is_array", "is_opt"}, []string{"OID", "_TEXT", "OID", "_TEXT", "BOOL", "BOOL"}, catalogRelationsRelease219)
		s.Bag = true
		s.Prepared = prepared
		fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
		// These two relations already have genuine catalog descriptors on native.
		s = fixture.Select("SELECT fktable,fkcols,pktable,pkcols,is_array,is_opt FROM pg_get_catalog_foreign_keys() WHERE fktable::oid=1255 AND fkcols=ARRAY['pronamespace']", catalogRelationsColumns, catalogRelationsTypes, [][]any{{"pg_proc", "{pronamespace}", "pg_namespace", "{oid}", false, false}})
		s.Prepared = prepared
		s.CheckMetadata = fixture.CatalogRelationsMetadata(b.PG)
		run(s)
	}
	ordinalRows := make([][]any, 219)
	for i := range ordinalRows {
		ordinalRows[i] = []any{int64(i + 1)}
	}
	run(fixture.Select("SELECT ordinality FROM pg_get_catalog_foreign_keys() WITH ORDINALITY ORDER BY ordinality", []string{"ordinality"}, []string{"INT8"}, ordinalRows))
	s := catalogRelationsInventoryStep("pg_get_catalog_foreign_keys() WITH ORDINALITY")
	fixture.PgDescriptionExecutorRows(t, p, b, b.DB, s)
	run(fixture.Select("SELECT count(*) AS total,count(n) AS series_count,count(fktable) AS links FROM ROWS FROM (pg_get_catalog_foreign_keys(),generate_series(1,221)) AS z(fktable,fkcols,pktable,pkcols,is_array,is_opt,n)", []string{"total", "series_count", "links"}, []string{"INT8", "INT8", "INT8"}, [][]any{{int64(221), int64(221), int64(219)}}))
	run(fixture.Select("SELECT fktable,pktable,n FROM ROWS FROM (pg_get_catalog_foreign_keys(),generate_series(1,221)) AS z(fktable,fkcols,pktable,pkcols,is_array,is_opt,n) WHERE n>219 ORDER BY n", []string{"fktable", "pktable", "n"}, []string{"REGCLASS", "REGCLASS", "INT4"}, [][]any{{nil, nil, int64(220)}, {nil, nil, int64(221)}}))
}

func TestReferenceCatalogRelationsShape(t *testing.T) {
	p := fixture.NewScenario(t)
	catalogRelationsBackends(t, p, func(t *testing.T, b fixture.Backend) { catalogRelationsShape(t, p, b) })
}
