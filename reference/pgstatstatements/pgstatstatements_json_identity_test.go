package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations follow REL_18_6 transformJsonConstructorOutput,
// transformJsonFuncExpr and the selected JsonExpr fields, not native hashes.
func TestReferencePgStatStatementsJSONIdentity(t *testing.T) {
	cases := []struct {
		name, first, second, firstType, secondType string
		firstValue, secondValue                    any
		groups                                     int64
	}{
		{"constructor_default", "JSON_ARRAY(1)", "JSON_ARRAY(2 RETURNING json)", "JSON", "JSON", "[1]", "[2]", 1},
		{"value_default", "JSON_VALUE('{}'::jsonb,'$')", "JSON_VALUE('{}'::jsonb,'$' RETURNING text)", "TEXT", "TEXT", nil, nil, 1},
		{"query_wrapper", "JSON_QUERY('{}'::jsonb,'$')", "JSON_QUERY('{}'::jsonb,'$' WITHOUT WRAPPER)", "JSONB", "JSONB", "{}", "{}", 2},
		{"query_returning", "JSON_QUERY('{}'::jsonb,'$')", "JSON_QUERY('{}'::jsonb,'$' RETURNING jsonb)", "JSONB", "JSONB", "{}", "{}", 2},
		{"query_quotes", "JSON_QUERY('{}'::jsonb,'$')", "JSON_QUERY('{}'::jsonb,'$' KEEP QUOTES)", "JSONB", "JSONB", "{}", "{}", 1},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pgssCase(t, true, func(s *pgssScenario) {
				s.exec("CREATE TABLE pgss_part.pgss_json_identity(k integer)")
				s.run(fixture.Exec("INSERT INTO pgss_part.pgss_json_identity VALUES(1)", 1))
				// The effective FROM alias is a released PG18 identity field and
				// isolates this stream from other cases' cluster-retained history.
				suffix := " AS pgss_json_identity FROM pgss_part.pgss_json_identity pgss_json_" + tc.name
				pattern := "SELECT JSON_%" + suffix
				s.counts(pattern, 0, 0, 0)
				s.work(fixture.Select("SELECT "+tc.first+suffix, []string{"pgss_json_identity"}, []string{tc.firstType}, [][]any{{tc.firstValue}}))
				s.work(fixture.Prepared(fixture.Select("SELECT "+tc.second+suffix, []string{"pgss_json_identity"}, []string{tc.secondType}, [][]any{{tc.secondValue}})))
				s.counts(pattern, tc.groups, 2, 2)
				s.both(fixture.Select("SELECT count(DISTINCT queryid) AS identities FROM pgss_part.pg_stat_statements WHERE query LIKE $1 AND userid=(SELECT oid FROM pg_roles WHERE rolname=current_user) AND dbid=(SELECT oid FROM pg_database WHERE datname=current_database())", []string{"identities"}, []string{"INT8"}, [][]any{{tc.groups}}, pattern))
			})
		})
	}
}
