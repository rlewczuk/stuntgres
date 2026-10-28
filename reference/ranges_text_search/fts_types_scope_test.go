package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSTypesScope(t *testing.T) {
	ftsTypesScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT oid::bigint,typname,typarray::bigint FROM pg_type WHERE oid IN (3614,3615) ORDER BY oid", []string{"oid", "typname", "typarray"}, []string{"INT8", "NAME", "INT8"}, [][]any{{int64(3614), "tsvector", int64(3643)}, {int64(3615), "tsquery", int64(3645)}}))
		// PostgreSQL supplies adjacent matching/array/index capabilities. These
		// native-only rejection witnesses do not claim parity for those features.
		if b.PG {
			return
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE fts_scope(v tsvector,q tsquery)", 0))
		for _, query := range []string{
			"CREATE TABLE fts_array(v tsvector[])", "CREATE TABLE fts_query_array(q tsquery[])",
			"SELECT 'a'::tsvector @@ 'a'::tsquery",
			"SELECT 'a'::tsvector = 'a'::tsvector", "SELECT 'a'::tsquery = 'a'::tsquery",
			"CREATE INDEX fts_vector_index ON fts_scope(v)", "CREATE INDEX fts_query_index ON fts_scope(q)",
		} {
			result, err := b.DB.ExecContext(p.Ctx, query)
			if err == nil || result != nil {
				t.Fatalf("excluded capability succeeded: %s result=%v err=%v", query, result, err)
			}
		}
	})
}
