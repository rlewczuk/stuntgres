package reference_test

import (
    _ "embed"
    "testing"
    "stuntgres/reference/fixture"
)

// Exact pg_get_viewdef observations from the pinned PostgreSQL 18.6 fixture.
// Independently retained SQL is never assembled from native view fragments.
//go:embed testdata/attribute_statistics_pg_stats_default_false.sql
var attributeStatsViewDefaultFalse string

//go:embed testdata/attribute_statistics_pg_stats_default_true.sql
var attributeStatsViewDefaultTrue string

//go:embed testdata/attribute_statistics_pg_stats_quoted_false.sql
var attributeStatsViewQuotedFalse string

//go:embed testdata/attribute_statistics_pg_stats_quoted_true.sql
var attributeStatsViewQuotedTrue string

func TestReferenceAttributeStatisticsViewDefinition(t *testing.T) {
    withAttributeStats(t, func(s attributeStatsScenario) {
        for _, tc := range []struct {
            quoted, pretty bool
            want string
        }{
            {false, false, attributeStatsViewDefaultFalse},
            {false, true, attributeStatsViewDefaultTrue},
            {true, false, attributeStatsViewQuotedFalse},
            {true, true, attributeStatsViewQuotedTrue},
        } {
            if tc.quoted { s.exec("SET quote_all_identifiers=on") } else { s.exec("SET quote_all_identifiers=off") }
            s.run(fixture.Select("SELECT pg_get_viewdef('pg_catalog.pg_stats'::regclass,$1::boolean) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{tc.want}}, tc.pretty))
        }
    })
}
