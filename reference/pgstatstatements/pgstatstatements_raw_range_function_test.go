package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsRawRangeFunctionIdentity(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		// REL_18_6 parsenodes.h RangeFunction selects is_rowsfrom even for
		// a single member. CREATE VIEW retains this raw query, unlike an
		// executed SELECT's analyzed function range entry. Both views must
		// still execute the same genuine regexp source with TEXT metadata.
		const prefix = "CREATE VIEW pgss_part.pgss_raw_regexp_rows AS SELECT * FROM "
		queries := []string{
			prefix + "regexp_split_to_table('left,right',',') AS tokens(value)",
			prefix + "ROWS FROM(regexp_split_to_table('left,right',',')) AS tokens(value)",
		}
		s.work(fixture.Exec("SET pg_stat_statements.track_utility=on", 0))
		s.counts(prefix+"%", 0, 0, 0)
		for i, query := range queries {
			step := fixture.Exec(query, 0)
			if i == 1 {
				step = fixture.Prepared(step)
			}
			s.work(step)
			s.both(fixture.Select("SELECT value FROM pgss_part.pgss_raw_regexp_rows ORDER BY value", []string{"value"}, []string{"TEXT"}, [][]any{{"left"}, {"right"}}))
			// Disabled-observer teardown lets both measured commands target
			// the exact same name; object names cannot explain their split.
			s.exec("DROP VIEW pgss_part.pgss_raw_regexp_rows")
		}
		s.entry(queries[0], 1, 0)
		s.entry(queries[1], 1, 0)
		s.counts(prefix+"%", 2, 2, 0)
		s.both(fixture.Select("SELECT count(DISTINCT queryid) AS identities FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"identities"}, []string{"INT8"}, [][]any{{int64(2)}}, prefix+"%"))
	})
}
