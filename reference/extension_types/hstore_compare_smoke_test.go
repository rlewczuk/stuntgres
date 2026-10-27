package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceHstoreComparisonFeatureSmoke(t *testing.T) {
	t.Run("typed_comparison_hash_sort_indexes_diagnostics", func(t *testing.T) {
		hstoreComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			hstoreInstall(t, p, b, c)
			hstoreComparisonTable(t, p, b, c)
			for _, q := range []string{
				"CREATE INDEX hs_cmp_btree ON hs_cmp(v)",
				"CREATE INDEX hs_cmp_hash ON hs_cmp USING hash(v)",
				"CREATE TABLE hs_cmp_diag_empty(v hstore)",
			} {
				hstoreRun(t, p, b, c, fixture.Exec(q, 0))
			}
			for _, s := range []fixture.Step{
				fixture.Select("SELECT 'b=>2,a=>1'::hstore='a=>1,b=>2'::hstore AS eq,hstore_cmp('a=>bc'::hstore,'ab=>c'::hstore) AS cmp,NULL::hstore='a=>NULL'::hstore AS missing", []string{"eq", "cmp", "missing"}, []string{"BOOL", "INT4", "BOOL"}, [][]any{{true, int64(-1), nil}}),
				hstoreComparisonIDs("SELECT id FROM hs_cmp ORDER BY v ASC NULLS LAST,id", 4, 6, 5, 1, 2, 7, 3, 8),
				hstoreComparisonIDs("SELECT id FROM hs_cmp WHERE v='b=>2,a=>1'::hstore ORDER BY id", 1, 2),
			} {
				hstoreRun(t, p, b, c, s)
				hstoreRun(t, p, b, c, fixture.Prepared(s))
			}
			for _, s := range hstoreComparisonCatalogChecks("public") {
				hstoreRun(t, p, b, c, s)
			}
			hstoreComparisonScalarChecks(t, p, b, c)
			hstoreComparisonHeldConsumer(t, p, b, c)
			hstoreComparisonUniqueSetup(t, p, b, c)
			hstoreComparisonUniqueChecks(t, p, b, c)
			hstoreComparisonDiagnosticChecks(t, p, b, c)
		})
	})
	t.Run("same_driver_restore_actual_dump_replay", func(t *testing.T) { hstoreComparisonControlPhase(t, "same") })
	t.Run("fresh_driver_restore", func(t *testing.T) { hstoreComparisonControlPhase(t, "fresh") })
}
